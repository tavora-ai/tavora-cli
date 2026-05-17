package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
	"github.com/tavora-ai/tavora-cli/internal/codefirst/scaffold"
	"github.com/tavora-ai/tavora-cli/internal/codefirst/source"
	"github.com/tavora-ai/tavora-cli/internal/codefirst/validate"
)

// Code-first verbs. The implementation notes in
// tavora-go/docs/code-first-agents-concept.md drive the contract;
// see the "CLI UX" section there.

// --- tavora init ---

var (
	initProjectSlug string // --project — matches the cloud-side Project slug
	initAPIURL      string
	initForce       bool
	initDir         string
	initDryRun      bool
)

var codefirstInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a tavora/ project folder (manifest, agent, persona, skill, eval, .gitignore)",
	Long: `tavora init creates a tavora/ folder under the current directory
containing a working starter project: tavora.jsonc, one agent
(agents/support/) with persona, skills, and an eval case, plus a
.gitignore that hides .runs/.

This is the entry point for the code-first authoring path. Edit the
files, then run tavora dev to sync a dev draft and tavora deploy to
cut an immutable published version.

Existing files are preserved unless --force is set.`,
	Example: `  tavora init
  tavora init --project acme-support
  tavora init --dir ./vendor/tavora --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := runInitFlow(initDir, initProjectSlug, initForce, initDryRun)
		if err != nil {
			return err
		}
		if initDryRun || root == "" {
			return nil
		}
		fmt.Println()
		fmt.Println("Next steps:")
		fmt.Printf("  cd %s\n", root)
		fmt.Println("  tavora dev          # validate, watch, sync a dev draft")
		fmt.Println("  tavora deploy       # promote the draft to a published version")
		return nil
	},
}

// runInitFlow is the shared scaffold+bind path for both `tavora init`
// and `tavora dev`'s offer-to-init fallback. Returns the absolute
// root of the scaffolded folder, or "" when --dry-run printed the
// plan and didn't write anything. Caller decides whether to print
// the "Next steps:" footer.
func runInitFlow(rootHint, appSlug string, force, dryRun bool) (string, error) {
	root := rootHint
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = filepath.Join(cwd, "tavora")
	}
	project := appSlug
	if project == "" {
		// If the user is already logged in (api key configured), the
		// truthful default is the slug of the project that key is bound
		// to — that's where source-sync will actually route. Falling
		// back to the directory name was a pre-deployments choice
		// and confuses users who init inside `<repo>/backend-go/`.
		project = tryGetProjectSlug()
	}
	if project == "" {
		project = filepath.Base(filepath.Dir(root))
		if project == "." || project == "/" || project == "" {
			project = "tavora-project"
		}
	}
	opt := scaffold.Options{
		Root:        root,
		ProjectName: project,
		APIURL:      initAPIURL,
		Force:       force,
	}
	if dryRun {
		for _, f := range scaffold.Plan(opt) {
			status("would write %s", filepath.Join(root, f.RelPath))
		}
		return "", nil
	}
	written, err := scaffold.Write(opt)
	if err != nil {
		return "", err
	}
	if len(written) == 0 {
		status("no files written (already present — pass --force to overwrite)")
		return root, nil
	}
	for _, p := range written {
		status("wrote %s", p)
	}

	// Best-effort cloud bind: mint (or reuse) the user's dev
	// deployment and write its slug to <root>/.env.local. Failures
	// aren't fatal — `tavora dev` will fall back to the server's
	// resolver auto-create on first sync.
	if err := bindCloudDeployment(root); err != nil {
		status("cloud bind skipped: %v", err)
		status("run `tavora login` then `tavora dev` to bind on first sync.")
	} else {
		status("wrote %s", filepath.Join(root, ".env.local"))
	}
	return root, nil
}

// --- tavora dev ---

var (
	devDir     string
	devOnce    bool
	devNoSync  bool
	devVerbose bool
	devNoInit  bool
)

var codefirstDevCmd = &cobra.Command{
	Use:   "dev",
	Short: "Watch tavora/, validate on every change, and sync a dev draft",
	Long: `tavora dev is the inner-loop command. It watches the tavora/
folder, debounces file changes, validates every revision, and syncs
a mutable dev draft to your account so playground invocations and
SDK calls targeting the draft pick up the new behavior.

Pass --once to do a single validate + sync (useful for CI). Pass
--no-sync to validate locally without touching the server.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := loadProjectOrFail(devDir)
		if err != nil {
			// If the failure is "no tavora.jsonc found anywhere", offer
			// to scaffold one in-process so the user doesn't have to
			// quit, run init, and re-run dev. Skipped in CI / non-TTY /
			// when --no-init is set, so scripts get the clean failure.
			if !isMissingTavoraFolderErr(err) || devNoInit || !isInteractive() {
				return err
			}
			fmt.Fprintf(os.Stderr, "No tavora/ folder found at or above this directory.\n")
			if !promptYesNo("Scaffold one here with `tavora init`?") {
				return fmt.Errorf("aborted; run `tavora init` first (or pass --no-init to skip this prompt)")
			}
			root, initErr := runInitFlow("", "", false, false)
			if initErr != nil {
				return initErr
			}
			if devDir == "" {
				devDir = root
			}
			fmt.Println()
			p, err = loadProjectOrFail(devDir)
			if err != nil {
				return err
			}
		}
		// Stamp the project root as the sync source for this
		// process. The transport reads CurrentSyncSource on every
		// outgoing request and sets X-Tavora-Source, letting the
		// server detect "two tavora/ folders authoring the same
		// agent" and warn back.
		CurrentSyncSource = p.Root

		// Warn loudly when the user invoked `tavora dev` (no --no-sync)
		// but no API key is configured — otherwise the "sync skipped"
		// status looks identical to --no-sync and they wonder why the
		// server never sees their agents.
		if !devNoSync && client == nil {
			status("warning: no API key configured — running in local-only mode (run `tavora login` or set TAVORA_API_KEY to enable sync)")
		}
		effectiveNoSync := devNoSync || client == nil

		// In watch mode we deliberately swallow the validate error
		// from the first pass so the user can keep editing toward
		// green. --once exits with a non-zero code so CI / scripts
		// still see the failure.
		firstErr := runValidateAndSync(p, effectiveNoSync, devVerbose)
		if devOnce {
			return firstErr
		}
		if firstErr != nil {
			status("%v — keep editing; the watcher will retry on every save", firstErr)
		}
		return watchAndSync(p, effectiveNoSync, devVerbose)
	},
}

// --- tavora deploy ---

var (
	deployDir    string
	deployAgent  string
	deployDryRun bool
)

var codefirstDeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Promote the current dev draft to an immutable published version",
	Long: `tavora deploy validates the local tavora/ folder, syncs a fresh
draft, then cuts an immutable published version. Without arguments
the whole project deploys; pass --agent <id> to deploy just one.

--dry-run skips the deploy step; useful as a standalone validate.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := loadProjectOrFail(deployDir)
		if err != nil {
			return err
		}
		issues := validate.Project(p)
		printIssues(p, issues)
		if validate.HasFatal(issues) {
			return fmt.Errorf("deploy refused: %d fatal validation issue(s)", validate.CountFatal(issues))
		}
		manifest := buildManifest(p)
		if deployDryRun {
			status("dry-run: would deploy manifest with %d agent(s), source hash %s", len(manifest.Agents), short(manifest.SourceHash))
			return nil
		}
		if client == nil {
			return fmt.Errorf("no API key configured — run 'tavora login' first or use --dry-run")
		}

		// See dev RunE for the rationale; deploy pre-syncs then
		// promotes, so it also wants source-flip detection.
		CurrentSyncSource = p.Root

		sdkManifest := toSDKManifest(manifest, p)
		if _, err := client.SourceSync(globalCtx(), sdkManifest); err != nil {
			return fmt.Errorf("pre-deploy sync failed: %w\n  hint: backend must accept /api/sdk/source-sync before deploy can run", err)
		}
		input := tavora.SourceDeployInput{
			Project:      p.Manifest.Project,
			LocalAgentID: deployAgent,
		}
		out, err := client.SourceDeploy(globalCtx(), input)
		if err != nil {
			return err
		}
		for _, a := range out.Agents {
			status("deployed %s → %s (version %s, semver %s)", a.LocalID, a.AgentID, a.VersionID, a.Semver)
		}
		return nil
	},
}

// --- tavora config show ---

var configShowCmd = &cobra.Command{
	Use:   "show [agent]",
	Short: "Print an agent's resolved config (post env-var substitution, post-merge)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := loadProjectOrFail("")
		if err != nil {
			return err
		}
		var target *source.Agent
		if len(args) == 1 {
			for _, a := range p.Agents {
				if a.Config.ID == args[0] {
					target = a
					break
				}
			}
			if target == nil {
				return fmt.Errorf("agent %q not found in project (have: %s)", args[0], agentIDs(p))
			}
			return printResolvedAgent(target)
		}
		// No arg: print every agent
		for _, a := range p.Agents {
			fmt.Printf("# agent: %s (%s)\n", a.Config.ID, a.Config.Name)
			if err := printResolvedAgent(a); err != nil {
				return err
			}
			fmt.Println()
		}
		return nil
	},
}

// configCmd is the parent for `tavora config show` etc. Other
// subcommands (set / get / unset) can land here later.
var codefirstConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect resolved project configuration",
}

// --- shared helpers ---

func loadProjectOrFail(dir string) (*source.Project, error) {
	if dir == "" {
		cwd, _ := os.Getwd()
		dir = cwd
	}
	p, err := source.Load(dir)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// isMissingTavoraFolderErr reports whether the error from source.Load
// is the "walked up to / and never found tavora.jsonc" case — the
// only error worth offering to scaffold around in dev. String-matched
// today because source.Load returns plain fmt.Errorf strings; if the
// source package ever surfaces typed errors, swap in errors.Is.
func isMissingTavoraFolderErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no tavora.jsonc found")
}

// isInteractive reports whether the CLI can prompt the user. False
// when stdin isn't a TTY (piped input, CI runners) or when CI=true
// is set explicitly so scripts get clean failures instead of hanging
// on a read.
func isInteractive() bool {
	if os.Getenv("CI") != "" {
		return false
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// promptYesNo asks a Y/n question on stdin/stderr and returns true
// for empty input, "y", or "yes" (case-insensitive). Default is Y;
// the prompt prints "[Y/n]" so the user can confirm with Enter.
func promptYesNo(question string) bool {
	fmt.Fprintf(os.Stderr, "%s [Y/n]: ", question)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

func runValidateAndSync(p *source.Project, noSync bool, verbose bool) error {
	issues := validate.Project(p)
	printIssues(p, issues)
	if validate.HasFatal(issues) {
		return fmt.Errorf("validation failed: %d fatal issue(s)", validate.CountFatal(issues))
	}
	manifest := buildManifest(p)
	if noSync {
		status("validation OK (%d agent(s), source %s, sync skipped)", len(p.Agents), manifest.SourceHash[7:19])
		return nil
	}

	sdkManifest := toSDKManifest(manifest, p)
	result, err := client.SourceSync(globalCtx(), sdkManifest)
	if err != nil {
		// 422 carries the server's validation issues in the response
		// body; surface them inline so the user sees what to fix.
		// Other errors get the legacy "endpoint may not exist yet"
		// hint — useful when running against an older server.
		if issues, ok := extractServerIssues(err); ok {
			for _, i := range issues {
				fmt.Fprintf(os.Stderr, "[%s] %s\n  %s\n", padSeverity(i.Severity), serverIssueLocation(i), i.Message)
				if i.Hint != "" {
					fmt.Fprintf(os.Stderr, "  hint: %s\n", i.Hint)
				}
			}
			return fmt.Errorf("source-sync rejected: %d server validation issue(s)", len(issues))
		}
		return fmt.Errorf("source-sync failed: %w\n  hint: backend may not yet expose /api/sdk/source-sync — use --no-sync to validate locally", err)
	}
	for _, i := range result.ServerIssues {
		fmt.Fprintf(os.Stderr, "[%s] %s\n  %s\n", padSeverity(i.Severity), serverIssueLocation(i), i.Message)
		if i.Hint != "" {
			fmt.Fprintf(os.Stderr, "  hint: %s\n", i.Hint)
		}
	}
	status("synced: draft %s, %d agent(s)", short(result.DraftHash), len(result.Agents))
	if verbose {
		out, _ := source.PrettyJSON(result)
		fmt.Println(string(out))
	}
	return nil
}

func short(hash string) string {
	if len(hash) < 19 {
		return hash
	}
	return hash[7:19]
}

// extractServerIssues pulls the server's `issues` array out of a 422
// response. The SDK exposes the raw body fields via APIError.Details;
// SourceSyncHandler returns {"issues": [...]} on validation failure
// with shape {file,line,column,severity,code,message,hint}. We
// JSON-roundtrip the Details["issues"] value into the SDK's typed
// SourceValidationIssue so the caller can format it the same way as
// successful-sync issues.
func extractServerIssues(err error) ([]tavora.SourceValidationIssue, bool) {
	var apiErr *tavora.APIError
	if !errors.As(err, &apiErr) {
		return nil, false
	}
	raw, ok := apiErr.Details["issues"]
	if !ok {
		return nil, false
	}
	buf, mErr := json.Marshal(raw)
	if mErr != nil {
		return nil, false
	}
	var issues []tavora.SourceValidationIssue
	if uErr := json.Unmarshal(buf, &issues); uErr != nil {
		return nil, false
	}
	return issues, len(issues) > 0
}

func padSeverity(s string) string {
	switch s {
	case "fatal":
		return "fatal"
	case "warn":
		return " warn"
	default:
		return s
	}
}

func serverIssueLocation(i tavora.SourceValidationIssue) string {
	if i.Line > 0 {
		if i.Column > 0 {
			return fmt.Sprintf("%s:%d:%d (server)", i.File, i.Line, i.Column)
		}
		return fmt.Sprintf("%s:%d (server)", i.File, i.Line)
	}
	if i.File != "" {
		return fmt.Sprintf("%s (server)", i.File)
	}
	return "(server) " + i.Code
}

func agentIDs(p *source.Project) string {
	ids := make([]string, 0, len(p.Agents))
	for _, a := range p.Agents {
		ids = append(ids, a.Config.ID)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "(none)"
	}
	return strings.Join(ids, ", ")
}

func printResolvedAgent(a *source.Agent) error {
	resolved := resolveForDisplay(a)
	out, err := source.PrettyJSON(resolved)
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// resolvedAgent is the shape we emit from `tavora config show`. It
// includes the parsed config plus the resolved skill paths and
// kinds so an AI tool can confirm "yes, my new skill is bound".
type resolvedAgent struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Model        source.ModelRef `json:"model"`
	Capabilities []string        `json:"capabilities,omitempty"`
	Persona      string          `json:"persona,omitempty"`
	Skills       []resolvedSkill `json:"skills"`
	Indexes      []string        `json:"indexes,omitempty"`
	Evals        []string        `json:"evals,omitempty"`
}

type resolvedSkill struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Binding string `json:"binding"`
}

func resolveForDisplay(a *source.Agent) resolvedAgent {
	skills := make([]resolvedSkill, 0, len(a.Skills))
	for _, s := range a.Skills {
		skills = append(skills, resolvedSkill{
			Kind:    string(s.Kind),
			Path:    s.RelPath,
			Binding: s.BindingRaw,
		})
	}
	evals := make([]string, 0, len(a.Evals))
	for _, e := range a.Evals {
		evals = append(evals, e.RelPath)
	}
	out := resolvedAgent{
		ID:           a.Config.ID,
		Name:         a.Config.Name,
		Model:        a.Config.Model,
		Capabilities: a.Config.Capabilities,
		Persona:      a.Persona,
		Skills:       skills,
		Indexes:      a.Config.Indexes,
		Evals:        evals,
	}
	return out
}

// --- manifest ---

// SyncManifest is the payload tavora dev / tavora deploy send to the
// server. It's content-addressed: hash each file, hash each agent,
// hash the whole project. The server can ask for missing blobs once
// the backend grows that capability.
type SyncManifest struct {
	Project     string          `json:"project"`
	Environment string          `json:"environment,omitempty"`
	SourceHash  string          `json:"sourceHash"`
	Agents      []ManifestAgent `json:"agents"`
	GeneratedAt time.Time       `json:"generatedAt"`
}

type ManifestAgent struct {
	ID         string         `json:"id"`
	SourceHash string         `json:"sourceHash"`
	Files      []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Size    int    `json:"size"`
	Content string `json:"content,omitempty"`
}

// globalCtx returns the rooted context the CLI runs under. We
// rebuild it lazily so the long-running dev loop doesn't share a
// single ctx between cycles.
func globalCtx() context.Context {
	return context.Background()
}

// toSDKManifest converts the CLI's local SyncManifest into the
// shape the SDK sends over the wire. Content bytes are included
// here — when the backend grows content-addressed dedupe it can
// reject duplicates and the CLI can drop the bytes.
func toSDKManifest(local SyncManifest, p *source.Project) tavora.SourceSyncManifest {
	out := tavora.SourceSyncManifest{
		Project:     local.Project,
		Environment: local.Environment,
		SourceHash:  local.SourceHash,
		GeneratedAt: local.GeneratedAt,
	}
	for _, a := range local.Agents {
		// Look up source bytes from the project tree so the manifest
		// is self-contained.
		var bytesByPath map[string][]byte
		for _, pa := range p.Agents {
			if pa.Config.ID == a.ID {
				bytesByPath = pa.SourceBytes
				break
			}
		}
		var files []tavora.SourceFile
		for _, f := range a.Files {
			files = append(files, tavora.SourceFile{
				Path:    f.Path,
				Hash:    f.Hash,
				Size:    f.Size,
				Content: bytesByPath[f.Path],
			})
		}
		out.Agents = append(out.Agents, tavora.SourceAgent{
			ID:         a.ID,
			SourceHash: a.SourceHash,
			Files:      files,
		})
	}
	return out
}

func buildManifest(p *source.Project) SyncManifest {
	m := SyncManifest{
		Project:     p.Manifest.Project,
		GeneratedAt: time.Now().UTC(),
	}
	projectHasher := sha256.New()
	for _, a := range p.Agents {
		agentHasher := sha256.New()
		var files []ManifestFile
		paths := make([]string, 0, len(a.SourceBytes))
		for k := range a.SourceBytes {
			paths = append(paths, k)
		}
		sort.Strings(paths)
		for _, k := range paths {
			b := a.SourceBytes[k]
			h := sha256.Sum256(b)
			hashHex := "sha256:" + hex.EncodeToString(h[:])
			files = append(files, ManifestFile{
				Path: k,
				Hash: hashHex,
				Size: len(b),
				// Content omitted from the manifest by default —
				// the backend resolves on demand once content-addressed
				// upload lands. Local-only printing fills it in below.
			})
			agentHasher.Write([]byte(k))
			agentHasher.Write(b)
		}
		agentHash := "sha256:" + hex.EncodeToString(agentHasher.Sum(nil))
		m.Agents = append(m.Agents, ManifestAgent{
			ID:         a.Config.ID,
			SourceHash: agentHash,
			Files:      files,
		})
		projectHasher.Write([]byte(a.Config.ID))
		projectHasher.Write([]byte(agentHash))
	}
	m.SourceHash = "sha256:" + hex.EncodeToString(projectHasher.Sum(nil))
	return m
}

func init() {
	codefirstInitCmd.Flags().StringVar(&initProjectSlug, "project", "", "Cloud Project slug to bind this folder to (written into tavora.jsonc as the project field)")
	codefirstInitCmd.Flags().StringVar(&initAPIURL, "api-url", "", "API URL written into tavora.jsonc (default: omit; CLI uses ~/.tavora.yaml)")
	codefirstInitCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing files")
	codefirstInitCmd.Flags().StringVar(&initDir, "dir", "", "Directory to scaffold (default: ./tavora)")
	codefirstInitCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "Print the file list without writing")

	codefirstDevCmd.Flags().StringVar(&devDir, "dir", "", "Project directory containing tavora.jsonc (default: search up from cwd)")
	codefirstDevCmd.Flags().BoolVar(&devOnce, "once", false, "Validate + sync a single time and exit")
	codefirstDevCmd.Flags().BoolVar(&devNoSync, "no-sync", false, "Validate locally only — do not contact the server")
	codefirstDevCmd.Flags().BoolVarP(&devVerbose, "verbose", "v", false, "Print extra detail on every cycle")
	codefirstDevCmd.Flags().BoolVar(&devNoInit, "no-init", false, "Don't offer to run tavora init when no tavora/ folder is found (default: prompt interactively)")

	codefirstDeployCmd.Flags().StringVar(&deployDir, "dir", "", "Project directory containing tavora.jsonc")
	codefirstDeployCmd.Flags().StringVar(&deployAgent, "agent", "", "Deploy a single agent by id (default: all agents)")
	codefirstDeployCmd.Flags().BoolVar(&deployDryRun, "dry-run", false, "Validate and build the manifest, but skip the deploy call")

	codefirstConfigCmd.AddCommand(configShowCmd)

	rootCmd.AddCommand(codefirstInitCmd)
	rootCmd.AddCommand(codefirstDevCmd)
	rootCmd.AddCommand(codefirstDeployCmd)
	rootCmd.AddCommand(codefirstConfigCmd)
}
