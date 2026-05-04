package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
	"google.golang.org/genai"
)

// `tavora rag-eval` is the CI-friendly home for RAG quality testing.
// It replaces the standalone tavora-sdk-go/examples/rag-eval-* programs
// — those were promoted to subcommands here so external users get one
// installable binary instead of vendoring three Go programs.
//
// Two modes today:
//   - formats — does the RAG ingestion pipeline accept and make
//     searchable each supported document format (pdf, md, txt, csv,
//     html)? Uses the kreuzberg corpus from tavora-testdata.
//   - judge — given a corpus with structured ground truth (the
//     invoices fixture in tavora-testdata), ask the RAG endpoint
//     questions and have Gemini score answers against expected values.
//
// Both modes default the corpus location to a sibling clone of
// github.com/tavora-ai/tavora-testdata. Override with --testdata.
//
// A third mode (general retrieval relevance) was on the original
// promotion plan but skipped: the existing example's keyword cases
// are tightly coupled to a specific support-doc corpus that doesn't
// generalize. A canonical case set should land before reviving it.

var ragEvalCmd = &cobra.Command{
	Use:   "rag-eval",
	Short: "Evaluate RAG quality (format coverage and answer accuracy)",
	Long: `RAG quality evaluation suitable for CI gating.

Two modes today:

    tavora rag-eval formats --gate    # ingestion + search across formats
    tavora rag-eval judge   --gate    # answer accuracy via LLM-as-judge

Both default to the tavora-testdata corpus, expected as a sibling clone:

    dev/tavora-ai/
    ├── tavora-tools/      # this binary's source
    └── tavora-testdata/   # corpus + ground truth

Override with --testdata <path>.`,
}

// --- shared flags + helpers ---

var (
	ragTestdata  string
	ragCleanup   bool
	ragVerbose   bool
	ragGate      bool
	ragWaitTime  time.Duration
)

// resolveTestdata mirrors the auto-detect logic the legacy examples
// used: prefer --testdata, fall back to a few sibling-clone candidates.
func resolveTestdata(flagVal string) (string, error) {
	if flagVal != "" {
		return filepath.Abs(flagVal)
	}
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, "..", "tavora-testdata", "extraction"),
		filepath.Join(cwd, "..", "..", "tavora-testdata", "extraction"),
		filepath.Join(cwd, "..", "..", "..", "tavora-testdata", "extraction"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return filepath.Abs(c)
		}
	}
	return "", fmt.Errorf("could not locate tavora-testdata/extraction; pass --testdata <path> (clone github.com/tavora-ai/tavora-testdata as a sibling)")
}

// createEvalStore creates a fresh store named with the mode + timestamp,
// and registers cleanup if --cleanup is set. Returns the store and a
// cleanup func the caller should defer.
func createEvalStore(ctx context.Context, mode string, cleanup bool) (*tavora.Store, func(), error) {
	store, err := client.CreateStore(ctx, tavora.CreateStoreInput{
		Name:        fmt.Sprintf("rag-eval-%s-%s", mode, time.Now().Format("20060102-150405")),
		Description: "Created by `tavora rag-eval " + mode + "`",
	})
	if err != nil {
		return nil, func() {}, fmt.Errorf("creating store: %w", err)
	}
	cleanupFn := func() {}
	if cleanup {
		cleanupFn = func() {
			fmt.Fprintf(os.Stderr, "Cleaning up store %s...\n", store.ID)
			if err := client.DeleteStore(context.Background(), store.ID); err != nil {
				fmt.Fprintf(os.Stderr, "warning: delete store: %v\n", err)
			}
		}
	}
	return store, cleanupFn, nil
}

// waitForDocs polls every doc until it reaches a terminal state or the
// deadline expires. Mutates the input slice's Status / Err fields so
// callers can render per-doc results.
func waitForDocs(ctx context.Context, docIDs []string, statusByID map[string]string, errByID map[string]string, maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		pending := 0
		for _, id := range docIDs {
			if isSettledStatus(statusByID[id]) {
				continue
			}
			d, err := client.GetDocument(ctx, id)
			if err != nil {
				errByID[id] = "status poll: " + err.Error()
				statusByID[id] = "failed"
				continue
			}
			statusByID[id] = d.Status
			if d.ErrorMessage != nil && *d.ErrorMessage != "" {
				errByID[id] = *d.ErrorMessage
			}
			if d.Status == "pending" || d.Status == "processing" {
				pending++
			}
		}
		if pending == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func isSettledStatus(s string) bool {
	return s == "completed" || s == "searchable" || s == "failed"
}

// --- formats mode ---

type ragFormatSpec struct {
	Label string
	Dir   string
	Exts  []string
}

// The formats listed here mirror what tavora-go's RAG extractor
// supports today (internal/rag/extractor.go). Adding a new format is
// a server-side change; updating this list is a follow-up.
var ragFormatSpecs = []ragFormatSpec{
	{"pdf", "pdf", []string{".pdf"}},
	{"markdown", "markdown", []string{".md"}},
	{"text", "text", []string{".txt"}},
	{"csv", "csv", []string{".csv"}},
	{"html", "html", []string{".html", ".htm"}},
}

type ragFormatRecord struct {
	Format   string
	Filename string
	DocID    string
	Status   string
	Err      string
}

type ragFormatReport struct {
	Label      string
	Attempted  int
	Uploaded   int
	Processed  int
	Searchable int
	Errors     []string
}

var ragFormatPerFormat int

var ragEvalFormatsCmd = &cobra.Command{
	Use:   "formats",
	Short: "Verify the RAG pipeline accepts and indexes each supported format",
	Long: `Upload N samples per format from the kreuzberg corpus, wait for
processing, then run a retrieval round-trip per file. Report per-format
coverage of attempted / uploaded / processed / searchable.

CI use:

    tavora rag-eval formats --gate

--gate exits non-zero if any format has zero searchable documents
(complete ingestion failure for that format).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		testdata, err := resolveTestdata(ragTestdata)
		if err != nil {
			return err
		}
		kreuzbergDir := filepath.Join(testdata, "kreuzberg")
		if _, err := os.Stat(kreuzbergDir); err != nil {
			return fmt.Errorf("kreuzberg dir missing at %s: %w", kreuzbergDir, err)
		}
		fmt.Fprintf(os.Stderr, "Testdata: %s\n", testdata)
		fmt.Fprintf(os.Stderr, "Sampling up to %d files per format across %d formats\n\n", ragFormatPerFormat, len(ragFormatSpecs))

		store, cleanup, err := createEvalStore(ctx, "formats", ragCleanup)
		if err != nil {
			return err
		}
		defer cleanup()
		fmt.Fprintf(os.Stderr, "Store: %s\n\n", store.ID)

		var records []ragFormatRecord
		for _, spec := range ragFormatSpecs {
			files, err := sampleFormatFiles(filepath.Join(kreuzbergDir, spec.Dir), spec.Exts, ragFormatPerFormat)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[%s] scan failed: %v\n", spec.Label, err)
				continue
			}
			for _, f := range files {
				rec := ragFormatRecord{Format: spec.Label, Filename: filepath.Base(f)}
				doc, upErr := client.UploadDocument(ctx, tavora.UploadDocumentInput{
					FilePath: f, StoreID: store.ID,
				})
				if upErr != nil {
					rec.Err = upErr.Error()
				} else {
					rec.DocID = doc.ID
				}
				if ragVerbose {
					if rec.Err != "" {
						fmt.Fprintf(os.Stderr, "  [%s] %s upload FAILED: %v\n", spec.Label, rec.Filename, upErr)
					} else {
						fmt.Fprintf(os.Stderr, "  [%s] %s uploaded (%s)\n", spec.Label, rec.Filename, doc.ID)
					}
				}
				records = append(records, rec)
			}
		}
		fmt.Fprintf(os.Stderr, "\nSubmitted %d files; waiting for processing (up to %s)...\n", len(records), ragWaitTime)

		// Status/err keyed by docID so waitForDocs (shared helper) can
		// update without knowing about ragFormatRecord.
		statusByID := map[string]string{}
		errByID := map[string]string{}
		var docIDs []string
		for _, rec := range records {
			if rec.DocID != "" {
				docIDs = append(docIDs, rec.DocID)
			}
		}
		waitForDocs(ctx, docIDs, statusByID, errByID, ragWaitTime)
		for i := range records {
			if records[i].DocID == "" {
				continue
			}
			records[i].Status = statusByID[records[i].DocID]
			if records[i].Err == "" && errByID[records[i].DocID] != "" {
				records[i].Err = errByID[records[i].DocID]
			}
		}

		fmt.Fprintln(os.Stderr, "Running retrieval round-trip...")
		for i, rec := range records {
			if rec.Status != "completed" {
				continue
			}
			if checkSearchable(ctx, store.ID, rec) {
				records[i].Status = "searchable"
			}
		}

		reports := aggregateFormats(records)
		printFormatReport(reports, ragVerbose, records)

		if ragGate {
			missing := []string{}
			for _, r := range reports {
				if r.Searchable == 0 {
					missing = append(missing, r.Label)
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("rag-eval gate failed: %d format(s) had zero searchable docs: %s", len(missing), strings.Join(missing, ", "))
			}
		}
		return nil
	},
}

func sampleFormatFiles(dir string, exts []string, n int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, e := range exts {
		wanted[strings.ToLower(e)] = true
	}
	var picks []string
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if wanted[strings.ToLower(filepath.Ext(ent.Name()))] {
			picks = append(picks, filepath.Join(dir, ent.Name()))
			if len(picks) == n {
				break
			}
		}
	}
	return picks, nil
}

// checkSearchable issues a search using the filename stem and accepts
// a hit if any returned chunk is from this document. Same heuristic
// the rag-eval-formats example used.
func checkSearchable(ctx context.Context, storeID string, rec ragFormatRecord) bool {
	query := strings.ReplaceAll(strings.TrimSuffix(rec.Filename, filepath.Ext(rec.Filename)), "_", " ")
	results, err := client.Search(ctx, tavora.SearchInput{
		Query: query, StoreID: storeID, TopK: 5,
	})
	if err != nil {
		return false
	}
	for _, r := range results {
		if r.DocumentID == rec.DocID {
			return true
		}
	}
	return false
}

func aggregateFormats(records []ragFormatRecord) []ragFormatReport {
	byFormat := map[string]*ragFormatReport{}
	for _, rec := range records {
		rep, ok := byFormat[rec.Format]
		if !ok {
			rep = &ragFormatReport{Label: rec.Format}
			byFormat[rec.Format] = rep
		}
		rep.Attempted++
		if rec.DocID != "" {
			rep.Uploaded++
		}
		if rec.Status == "completed" || rec.Status == "searchable" {
			rep.Processed++
		}
		if rec.Status == "searchable" {
			rep.Searchable++
		}
		if rec.Err != "" {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %s", rec.Filename, ragTruncate(rec.Err, 80)))
		}
	}
	var out []ragFormatReport
	for _, spec := range ragFormatSpecs {
		if rep, ok := byFormat[spec.Label]; ok {
			out = append(out, *rep)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func printFormatReport(reports []ragFormatReport, verbose bool, records []ragFormatRecord) {
	bar := strings.Repeat("─", 70)
	fmt.Println()
	fmt.Println(bar)
	fmt.Printf("%-10s  %-9s  %-9s  %-10s\n", "format", "uploaded", "processed", "searchable")
	fmt.Println(bar)
	totalAtt, totalUp, totalProc, totalSrch := 0, 0, 0, 0
	for _, r := range reports {
		fmt.Printf("%-10s  %d/%d       %d/%d       %d/%d\n",
			r.Label, r.Uploaded, r.Attempted, r.Processed, r.Attempted, r.Searchable, r.Attempted)
		totalAtt += r.Attempted
		totalUp += r.Uploaded
		totalProc += r.Processed
		totalSrch += r.Searchable
	}
	fmt.Println(bar)
	fmt.Printf("%-10s  %d/%d       %d/%d       %d/%d\n",
		"TOTAL", totalUp, totalAtt, totalProc, totalAtt, totalSrch, totalAtt)

	var failing []ragFormatReport
	for _, r := range reports {
		if len(r.Errors) > 0 {
			failing = append(failing, r)
		}
	}
	if len(failing) > 0 {
		fmt.Println("\nErrors:")
		for _, r := range failing {
			fmt.Printf("  [%s]\n", r.Label)
			for _, e := range r.Errors {
				fmt.Printf("    - %s\n", e)
			}
		}
	}
	if verbose {
		fmt.Println("\nPer-file detail:")
		for _, rec := range records {
			fmt.Printf("  [%s] %-40s status=%s\n", rec.Format, rec.Filename, rec.Status)
		}
	}
}

// --- judge mode ---

// fieldQuery pairs a cases.json field with a natural-language question.
// The question is what we ask the RAG; the field tells the judge which
// ground-truth value to score against.
type ragFieldQuery struct {
	Field    string
	Question string
}

var ragFieldQueries = []ragFieldQuery{
	{"vendor", "What is the vendor or company name that issued this invoice?"},
	{"invoice_number", "What is the invoice number on this invoice?"},
	{"total", "What is the total amount due on this invoice?"},
	{"date", "What is the invoice date?"},
	{"currency", "What currency is used on this invoice?"},
}

type ragEvalCase struct {
	File     string                 `json:"file"`
	Expected map[string]interface{} `json:"expected"`
}

type ragJudgment struct {
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

type ragCaseResult struct {
	File        string
	Field       string
	Question    string
	Expected    interface{}
	Answer      string
	Score       int
	JudgeReason string
	Err         string
}

type ragFieldAgg struct {
	Total    int
	Passed   int
	Errors   int
	SumScore int
}

var (
	ragJudgeLimit     int
	ragJudgeThreshold int
	ragJudgeModel     string
)

var ragEvalJudgeCmd = &cobra.Command{
	Use:   "judge",
	Short: "Score RAG answers against structured ground truth using an LLM judge",
	Long: `Upload the invoices fixture from tavora-testdata to a fresh store, ask
the RAG endpoint questions about each invoice (vendor, total, date, etc.),
and have Gemini score each answer 0-10 against the ground-truth value
in cases.json.

Requires three environment variables:

    TAVORA_URL=...
    TAVORA_API_KEY=...
    GEMINI_API_KEY=...

CI use:

    tavora rag-eval judge --gate --pass-threshold 7

--gate exits non-zero if any answer scores below --pass-threshold or
errors out. Defaults: threshold=7, judge-model=gemini-2.5-flash.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		geminiKey := os.Getenv("GEMINI_API_KEY")
		if geminiKey == "" {
			return fmt.Errorf("GEMINI_API_KEY env var is required for the judge mode")
		}
		testdata, err := resolveTestdata(ragTestdata)
		if err != nil {
			return err
		}
		casesPath := filepath.Join(testdata, "cases.json")
		cases, err := loadRagCases(casesPath)
		if err != nil {
			return fmt.Errorf("loading cases: %w", err)
		}
		if ragJudgeLimit > 0 && ragJudgeLimit < len(cases) {
			cases = cases[:ragJudgeLimit]
		}
		fmt.Fprintf(os.Stderr, "Testdata: %s\n", testdata)
		fmt.Fprintf(os.Stderr, "Cases: %d × fields: %d = %d judgments\n\n",
			len(cases), len(ragFieldQueries), len(cases)*len(ragFieldQueries))

		store, cleanup, err := createEvalStore(ctx, "judge", ragCleanup)
		if err != nil {
			return err
		}
		defer cleanup()
		fmt.Fprintf(os.Stderr, "Store: %s\n\n", store.ID)

		docByFile, err := uploadJudgeCorpus(ctx, store.ID, testdata, cases, ragVerbose)
		if err != nil {
			return err
		}

		gc, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey: geminiKey, Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return fmt.Errorf("creating genai client: %w", err)
		}

		fmt.Fprintln(os.Stderr, "Running eval...")
		var results []ragCaseResult
		for _, c := range cases {
			docID := docByFile[c.File]
			if docID == "" {
				fmt.Fprintf(os.Stderr, "  [skip] %s — not processed\n", filepath.Base(c.File))
				continue
			}
			for _, fq := range ragFieldQueries {
				expected, ok := c.Expected[fq.Field]
				if !ok {
					continue
				}
				res := ragCaseResult{
					File: filepath.Base(c.File), Field: fq.Field,
					Question: fq.Question, Expected: expected,
				}
				answer, err := askJudgeRAG(ctx, store.ID, fq.Question)
				if err != nil {
					res.Err = "rag: " + err.Error()
					results = append(results, res)
					continue
				}
				res.Answer = answer
				verdict, err := runJudge(ctx, gc, ragJudgeModel, fq.Question, expected, answer)
				if err != nil {
					res.Err = "judge: " + err.Error()
					results = append(results, res)
					continue
				}
				res.Score = verdict.Score
				res.JudgeReason = verdict.Reason
				results = append(results, res)
				if ragVerbose {
					fmt.Fprintf(os.Stderr, "  [%d/10] %-18s %-18s expected=%v  answer=%q\n",
						res.Score, res.File, res.Field, res.Expected, ragTruncate(res.Answer, 60))
				}
			}
		}

		summary := printJudgeReport(results, ragJudgeThreshold, ragVerbose)

		if ragGate {
			fails := summary.Total - summary.Passed
			if fails > 0 {
				return fmt.Errorf("rag-eval gate failed: %d/%d judgments below threshold or errored", fails, summary.Total)
			}
		}
		return nil
	},
}

func loadRagCases(path string) ([]ragEvalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []ragEvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, err
	}
	return cases, nil
}

func uploadJudgeCorpus(ctx context.Context, storeID, testdataPath string, cases []ragEvalCase, verbose bool) (map[string]string, error) {
	docByFile := map[string]string{}
	var docIDs []string
	for _, c := range cases {
		abs := filepath.Join(testdataPath, c.File)
		doc, err := client.UploadDocument(ctx, tavora.UploadDocumentInput{
			FilePath: abs, StoreID: storeID,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  upload %s: %v\n", c.File, err)
			continue
		}
		docByFile[c.File] = doc.ID
		docIDs = append(docIDs, doc.ID)
		if verbose {
			fmt.Fprintf(os.Stderr, "  uploaded %s → %s\n", c.File, doc.ID)
		}
	}
	fmt.Fprintf(os.Stderr, "Uploaded %d/%d docs; waiting for processing (up to %s)...\n", len(docIDs), len(cases), ragWaitTime)

	statusByID := map[string]string{}
	errByID := map[string]string{}
	waitForDocs(ctx, docIDs, statusByID, errByID, ragWaitTime)
	fmt.Fprintln(os.Stderr)
	return docByFile, nil
}

// askJudgeRAG queries the chat endpoint with RAG enabled against a
// specific store. Single-turn — judge mode doesn't need conversation
// memory, just a one-shot question/answer.
func askJudgeRAG(ctx context.Context, storeID, question string) (string, error) {
	resp, err := client.ChatCompletion(ctx, tavora.ChatCompletionInput{
		Messages: []tavora.ChatMessage{{Role: "user", Content: question}},
		UseRAG:   true, StoreID: storeID,
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return resp.Choices[0].Message.Content, nil
}

// judgePromptTemplate is the prompt the LLM judge sees. The 0-10
// rubric is intentionally generous on formatting (so "$2,657.71" vs
// 2657.71 doesn't fail) but strict on value correctness.
const judgePromptTemplate = `You are evaluating whether an AI assistant's answer correctly reflects a known ground-truth value.

Question asked to the assistant:
%s

Ground truth (expected value):
%v

Assistant's answer:
%s

Scoring guide (0–10):
- 10 = exact match or trivially-equivalent formatting (e.g. "$2,657.71" vs 2657.71)
- 7–9 = correct value with extra prose or minor presentation differences
- 4–6 = partially correct, or the right value appears but contradictory info is also present
- 1–3 = wrong value but recognisable topic
- 0 = unrelated, refusal, or blank

Focus on the VALUE, not the framing. Return ONLY a single JSON object:
{"score": <int 0-10>, "reason": "<1 short sentence>"}`

var judgeJSONRE = regexp.MustCompile(`(?s)\{.*\}`)

func runJudge(ctx context.Context, gc *genai.Client, model, question string, expected interface{}, answer string) (*ragJudgment, error) {
	prompt := fmt.Sprintf(judgePromptTemplate, question, expected, answer)
	resp, err := gc.Models.GenerateContent(ctx, model, genai.Text(prompt), nil)
	if err != nil {
		return nil, err
	}
	text := resp.Text()
	match := judgeJSONRE.FindString(text)
	if match == "" {
		return nil, fmt.Errorf("no JSON in judge response: %s", ragTruncate(text, 200))
	}
	var v ragJudgment
	if err := json.Unmarshal([]byte(match), &v); err != nil {
		return nil, fmt.Errorf("parse judgment: %w (raw: %s)", err, ragTruncate(match, 200))
	}
	if v.Score < 0 {
		v.Score = 0
	}
	if v.Score > 10 {
		v.Score = 10
	}
	return &v, nil
}

type judgeSummary struct {
	Total  int
	Passed int
	Errors int
}

func printJudgeReport(results []ragCaseResult, threshold int, verbose bool) judgeSummary {
	bar := strings.Repeat("─", 80)
	fmt.Println()
	fmt.Println(bar)

	byField := map[string]*ragFieldAgg{}
	for _, r := range results {
		a, ok := byField[r.Field]
		if !ok {
			a = &ragFieldAgg{}
			byField[r.Field] = a
		}
		a.Total++
		if r.Err != "" {
			a.Errors++
			continue
		}
		a.SumScore += r.Score
		if r.Score >= threshold {
			a.Passed++
		}
	}

	fmt.Printf("%-18s  %-8s  %-8s  %-10s  %-8s\n", "field", "passed", "errors", "avg score", "graded")
	fmt.Println(bar)

	fields := make([]string, 0, len(byField))
	for f := range byField {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	var totalPassed, totalTotal, totalErrors, totalGraded, totalScore int
	for _, f := range fields {
		a := byField[f]
		graded := a.Total - a.Errors
		avg := 0.0
		if graded > 0 {
			avg = float64(a.SumScore) / float64(graded)
		}
		fmt.Printf("%-18s  %d/%d       %d         %.2f        %d\n",
			f, a.Passed, a.Total, a.Errors, avg, graded)
		totalPassed += a.Passed
		totalTotal += a.Total
		totalErrors += a.Errors
		totalGraded += graded
		totalScore += a.SumScore
	}
	fmt.Println(bar)
	totalAvg := 0.0
	if totalGraded > 0 {
		totalAvg = float64(totalScore) / float64(totalGraded)
	}
	fmt.Printf("%-18s  %d/%d       %d         %.2f        %d\n",
		"TOTAL", totalPassed, totalTotal, totalErrors, totalAvg, totalGraded)

	var weak []ragCaseResult
	for _, r := range results {
		if r.Err != "" || r.Score < threshold {
			weak = append(weak, r)
		}
	}
	if len(weak) > 0 {
		fmt.Println("\nFailures / low scores:")
		for _, r := range weak {
			if r.Err != "" {
				fmt.Printf("  [ERR] %s %s — %s\n", r.File, r.Field, ragTruncate(r.Err, 100))
				continue
			}
			fmt.Printf("  [%d/10] %s %s\n", r.Score, r.File, r.Field)
			fmt.Printf("      expected=%v  answer=%q\n", r.Expected, ragTruncate(r.Answer, 80))
			fmt.Printf("      judge: %s\n", r.JudgeReason)
		}
	}

	return judgeSummary{Total: totalTotal, Passed: totalPassed, Errors: totalErrors}
}

func ragTruncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func init() {
	// Shared flags both subcommands honor.
	for _, c := range []*cobra.Command{ragEvalFormatsCmd, ragEvalJudgeCmd} {
		c.Flags().StringVar(&ragTestdata, "testdata", "", "Path to tavora-testdata/extraction (default: auto-detect sibling clone)")
		c.Flags().BoolVar(&ragCleanup, "cleanup", true, "Delete the eval store after run completes")
		c.Flags().BoolVarP(&ragVerbose, "verbose", "v", false, "Print per-file / per-judgment detail")
		c.Flags().BoolVar(&ragGate, "gate", false, "Exit non-zero on failure (for CI)")
		c.Flags().DurationVar(&ragWaitTime, "wait", 120*time.Second, "Max time to wait for document processing")
	}

	ragEvalFormatsCmd.Flags().IntVar(&ragFormatPerFormat, "per-format", 2, "Files to sample per format")

	ragEvalJudgeCmd.Flags().IntVar(&ragJudgeLimit, "limit", 0, "Limit to first N invoice cases (0 = all)")
	ragEvalJudgeCmd.Flags().IntVar(&ragJudgeThreshold, "pass-threshold", 7, "Minimum judge score (0-10) to count as a pass")
	ragEvalJudgeCmd.Flags().StringVar(&ragJudgeModel, "judge-model", "gemini-2.5-flash", "Gemini model used by the judge")

	ragEvalCmd.AddCommand(ragEvalFormatsCmd)
	ragEvalCmd.AddCommand(ragEvalJudgeCmd)
}
