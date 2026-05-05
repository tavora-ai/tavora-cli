package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	documentsCmd.AddCommand(documentsListCmd)
	documentsCmd.AddCommand(documentsUploadCmd)
	documentsCmd.AddCommand(documentsGetCmd)
	documentsCmd.AddCommand(documentsWaitCmd)
	documentsCmd.AddCommand(documentsDeleteCmd)
}
