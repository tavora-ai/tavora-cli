package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var supportedExts = map[string]bool{
	".pdf": true, ".md": true, ".txt": true, ".csv": true,
}

var documentsCmd = &cobra.Command{
	Use:   "documents",
	Short: "Manage documents",
}

var (
	docsLimit        int
	docsOffset       int
	docsIndexID string
)

var documentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List documents",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := client.ListDocuments(cmd.Context(), tavora.ListDocumentsInput{
			Limit:   docsLimit,
			Offset:  docsOffset,
			IndexID: docsIndexID,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(result)
		}

		fmt.Printf("Total: %d documents\n\n", result.Total)

		if len(result.Data) == 0 {
			fmt.Println("No documents found.")
			return nil
		}

		t := newTable("ID", "FILENAME", "STATUS", "CHUNKS", "SIZE", "CREATED")
		for _, d := range result.Data {
			t.row(d.ID, d.Filename, d.Status,
				fmt.Sprintf("%d", d.ChunkCount),
				formatSize(d.FileSize),
				d.CreatedAt.Format("2006-01-02"))
		}
		return t.flush()
	},
}

var documentsGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get a document by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		doc, err := client.GetDocument(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(doc)
		}

		fields := []kv{
			field("ID", doc.ID),
			field("Status", doc.Status),
			field("Size", formatSize(doc.FileSize)),
			field("Chunks", fmt.Sprintf("%d", doc.ChunkCount)),
			field("Content-Type", doc.ContentType),
		}
		if doc.ErrorMessage != nil {
			fields = append(fields, field("Error", *doc.ErrorMessage))
		}
		fields = append(fields, field("Created", doc.CreatedAt.Format("2006-01-02 15:04:05")))

		detail(fmt.Sprintf("Document: %s", doc.Filename), fields...)
		return nil
	},
}

var uploadIndexID string

var documentsUploadCmd = &cobra.Command{
	Use:   "upload [file-or-dir]",
	Short: "Upload a document or all documents in a directory",
	Long: `Upload a single file or all supported files in a directory.
Supported file types: .pdf, .md, .txt, .csv`,
	Example: `  tavora documents upload report.pdf
  tavora documents upload ./docs/ --store abc123
  tavora documents upload paper.md && tavora documents wait <id>`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]

		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("cannot access %s: %w", path, err)
		}

		if !info.IsDir() {
			return uploadSingleFile(cmd, path)
		}

		// Bulk upload: walk directory for supported files
		var files []string
		err = filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if supportedExts[ext] {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("error scanning directory: %w", err)
		}

		if len(files) == 0 {
			fmt.Println("No supported files found (.pdf, .md, .txt, .csv).")
			return nil
		}

		fmt.Printf("Uploading %d files from %s...\n\n", len(files), path)

		var uploaded, failed int
		for _, f := range files {
			doc, err := client.UploadDocument(cmd.Context(), tavora.UploadDocumentInput{
				FilePath:     f,
				IndexID: uploadIndexID,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "  FAIL  %s: %v\n", filepath.Base(f), err)
				failed++
				continue
			}

			if isJSON() {
				printJSON(doc) //nolint:errcheck
			} else {
				fmt.Printf("  OK    %s (%s)\n", doc.Filename, doc.ID)
			}
			uploaded++
		}

		if !isJSON() {
			fmt.Printf("\nDone: %d uploaded, %d failed\n", uploaded, failed)
		}
		return nil
	},
}

func uploadSingleFile(cmd *cobra.Command, path string) error {
	doc, err := client.UploadDocument(cmd.Context(), tavora.UploadDocumentInput{
		FilePath:     path,
		IndexID: uploadIndexID,
	})
	if err != nil {
		return err
	}

	if isJSON() {
		return printJSON(doc)
	}

	fmt.Printf("Uploaded: %s (%s)\n", doc.Filename, doc.ID)
	fmt.Printf("  Status: %s\n", doc.Status)
	return nil
}

var (
	waitTimeout  time.Duration
	waitInterval time.Duration
)

var documentsWaitCmd = &cobra.Command{
	Use:   "wait [id]",
	Short: "Wait for a document to finish processing",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		deadline := time.Now().Add(waitTimeout)

		for {
			doc, err := client.GetDocument(cmd.Context(), id)
			if err != nil {
				return err
			}

			switch doc.Status {
			case "completed":
				if isJSON() {
					return printJSON(doc)
				}
				fmt.Printf("Document ready: %s (%d chunks)\n", doc.Filename, doc.ChunkCount)
				return nil
			case "error":
				if isJSON() {
					return printJSON(doc)
				}
				msg := "unknown error"
				if doc.ErrorMessage != nil {
					msg = *doc.ErrorMessage
				}
				return fmt.Errorf("document processing failed: %s", msg)
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("timeout: document still %s after %s", doc.Status, waitTimeout)
			}

			status("  Status: %s (waiting...)", doc.Status)
			time.Sleep(waitInterval)
		}
	},
}

var (
	watchIndexID string
	watchOnce    bool
)

var documentsWatchCmd = &cobra.Command{
	Use:   "watch [dir]",
	Short: "Watch a directory and upload new docs as they appear",
	Long: `Long-running watcher. Walks <dir> once to upload anything not
already in the index, then keeps watching for files created or
written and uploads them as they land. Ctrl-C to stop.

Supported extensions: .pdf, .md, .txt, .csv.

De-duplication is by filename: the watcher reads the index's
existing document list at startup and skips files whose basename is
already present. Re-uploading a file by editing it doesn't re-ingest
on the server side — delete the old document first if you need a
re-index.`,
	Example: `  tavora documents watch ./docs --index abc123
  tavora documents watch ./docs --once       # walk + upload, then exit`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		info, err := os.Stat(root)
		if err != nil {
			return fmt.Errorf("cannot access %s: %w", root, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", root)
		}
		return runDocumentsWatch(cmd.Context(), root, watchIndexID, watchOnce)
	},
}

// runDocumentsWatch implements the body of `tavora documents watch`.
// Extracted from the cobra RunE for legibility — long enough to need
// its own scope and we want the function-level comment to spell out
// the seeding + dedupe model the user gets.
func runDocumentsWatch(ctx context.Context, root, indexID string, once bool) error {
	// Seed the known-files set from the index so a re-launch of
	// the watcher doesn't double-upload everything. Filename basis
	// (not content hash) because the server's documents.* surface
	// is name-indexed today.
	known, err := loadKnownFilenames(ctx, indexID)
	if err != nil {
		return fmt.Errorf("seed known docs: %w", err)
	}

	// Pass 1 — walk + upload anything missing.
	var uploaded, skipped, failed int
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if !supportedExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		base := filepath.Base(p)
		if known[base] {
			skipped++
			return nil
		}
		if err := uploadWatchFile(ctx, p, indexID); err != nil {
			fmt.Fprintf(os.Stderr, "  FAIL  %s: %v\n", base, err)
			failed++
			return nil
		}
		known[base] = true
		uploaded++
		fmt.Printf("  uploaded  %s\n", base)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("\ninitial pass: %d uploaded, %d already-known, %d failed\n",
		uploaded, skipped, failed)
	if once {
		return nil
	}

	// Pass 2 — fsnotify watch loop.
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: %w", err)
	}
	defer w.Close()
	if err := addRecursive(w, root); err != nil {
		return err
	}
	fmt.Printf("\nwatching %s (ctrl-c to stop)\n", root)

	// debounce per path — editor saves often arrive as a Create
	// followed by one or more Writes within ~50ms. We coalesce by
	// path to avoid uploading mid-write.
	var mu sync.Mutex
	pending := map[string]*time.Timer{}
	enqueue := func(path string) {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := pending[path]; ok {
			t.Stop()
		}
		pending[path] = time.AfterFunc(400*time.Millisecond, func() {
			mu.Lock()
			delete(pending, path)
			mu.Unlock()
			base := filepath.Base(path)
			if known[base] {
				return
			}
			if !supportedExts[strings.ToLower(filepath.Ext(path))] {
				return
			}
			if err := uploadWatchFile(ctx, path, indexID); err != nil {
				fmt.Fprintf(os.Stderr, "  FAIL  %s: %v\n", base, err)
				return
			}
			known[base] = true
			fmt.Printf("  uploaded  %s\n", base)
		})
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = addRecursive(w, ev.Name)
					continue
				}
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				continue
			}
			enqueue(ev.Name)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "watch error: %v\n", err)
		}
	}
}

// loadKnownFilenames builds the seed set of filenames already known
// to the index (or to the project, when indexID is empty). Paginates
// because the list endpoint caps at 100 per page.
func loadKnownFilenames(ctx context.Context, indexID string) (map[string]bool, error) {
	known := map[string]bool{}
	offset := 0
	for {
		page, err := client.ListDocuments(ctx, tavora.ListDocumentsInput{
			Limit:   100,
			Offset:  offset,
			IndexID: indexID,
		})
		if err != nil {
			return nil, err
		}
		for _, d := range page.Data {
			known[d.Filename] = true
		}
		if !page.HasMore {
			return known, nil
		}
		offset += 100
	}
}

func uploadWatchFile(ctx context.Context, path, indexID string) error {
	_, err := client.UploadDocument(ctx, tavora.UploadDocumentInput{
		FilePath: path,
		IndexID:  indexID,
	})
	return err
}

var documentsDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a document by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.DeleteDocument(cmd.Context(), args[0]); err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}

		fmt.Println("Document deleted.")
		return nil
	},
}

func init() {
	documentsListCmd.Flags().IntVar(&docsLimit, "limit", 50, "Max documents to return")
	documentsListCmd.Flags().IntVar(&docsOffset, "offset", 0, "Offset for pagination")
	documentsListCmd.Flags().StringVar(&docsIndexID, "store", "", "Filter by store ID")

	documentsUploadCmd.Flags().StringVar(&uploadIndexID, "store", "", "Assign to store ID")

	documentsWaitCmd.Flags().DurationVar(&waitTimeout, "timeout", 60*time.Second, "Max time to wait")
	documentsWaitCmd.Flags().DurationVar(&waitInterval, "interval", 2*time.Second, "Poll interval")

	documentsWatchCmd.Flags().StringVar(&watchIndexID, "index", "", "Index to upload into (omit for the project default)")
	documentsWatchCmd.Flags().BoolVar(&watchOnce, "once", false, "Walk + upload missing, then exit (skip the fsnotify loop)")

	documentsCmd.AddCommand(documentsListCmd)
	documentsCmd.AddCommand(documentsUploadCmd)
	documentsCmd.AddCommand(documentsWatchCmd)
	documentsCmd.AddCommand(documentsGetCmd)
	documentsCmd.AddCommand(documentsWaitCmd)
	documentsCmd.AddCommand(documentsDeleteCmd)
}
