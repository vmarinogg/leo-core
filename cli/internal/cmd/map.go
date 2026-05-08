package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/momhq/mom/cli/internal/adapters/storage"
	"github.com/momhq/mom/cli/internal/cartographer"
	"github.com/momhq/mom/cli/internal/gardener"
	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/scope"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
)

var mapCmd = &cobra.Command{
	Use:    "map",
	Short:  "Scan existing code, docs, and commits to seed the memory",
	Hidden: true, // Cartographer-driven seeding is on hold pending v0.40 rework — see #240. Command kept callable for users with existing scripts; hidden from `mom --help` to stop pointing new users at low-quality output.
	Long: `Map scans the chosen directory for code, markdown, dependency
manifests, and commit history to create initial memories.

By default it writes to the nearest .mom/ found by walking up from the
scan directory. Use --scope to override the target .mom/ location.`,
	RunE: runBootstrap,
}

func registerMapFlags(cmd *cobra.Command) {
	cmd.Flags().String("path", "", "Directory to scan (default: current directory)")
	cmd.Flags().Bool("refresh", false, "Re-scan all files, ignoring the SHA256 cache")
	cmd.Flags().Bool("dry-run", false, "Show what would be written without persisting")
	cmd.Flags().Int("commit-depth", 200, "Number of recent commits to scan")
	cmd.Flags().Int64("max-file-size", 2, "Skip files larger than this many MB")
	cmd.Flags().String("scope", "", "Target scope label (user/org/repo/workspace/custom)")
	cmd.Flags().Bool("no-graph", false, "Skip opening the memory graph in the browser after bootstrap")
}

func init() {
	registerMapFlags(mapCmd)
}

func runBootstrap(cmd *cobra.Command, _ []string) error {
	scanPath, _ := cmd.Flags().GetString("path")
	refresh, _ := cmd.Flags().GetBool("refresh")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	commitDepth, _ := cmd.Flags().GetInt("commit-depth")
	maxFileSizeMB, _ := cmd.Flags().GetInt64("max-file-size")
	scopeLabel, _ := cmd.Flags().GetString("scope")
	noGraph, _ := cmd.Flags().GetBool("no-graph")

	if scanPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		scanPath = cwd
	}

	scanPath, err := filepath.Abs(scanPath)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	// Resolve target .mom/ directory.
	var targetScope scope.Scope
	var found bool

	if scopeLabel != "" {
		targetScope, found = scope.FindByLabel(scanPath, scopeLabel)
		if !found {
			return fmt.Errorf("no .mom/ with scope %q found from %s", scopeLabel, scanPath)
		}
	} else {
		targetScope, found = scope.NearestWritable(scanPath)
		if !found {
			return fmt.Errorf("no .mom/ directory found — run 'mom init' first")
		}
	}

	cfg := cartographer.DefaultConfig()
	cfg.CommitDepth = commitDepth
	cfg.MaxFileSizeMB = maxFileSizeMB
	cfg.Refresh = refresh
	cfg.DryRun = dryRun

	// For user/org scopes, discover child repos and scan each into its own .mom/.
	if targetScope.Label == "user" || targetScope.Label == "org" {
		return runMultiRepoBootstrap(cmd, scanPath, targetScope, cfg, dryRun)
	}

	cfg.ScopeDir = targetScope.Path

	p := ux.NewPrinter(cmd.OutOrStdout())
	isTTY := ux.IsTTY(cmd.OutOrStdout())

	// Wire up spinner and progress callback when running interactively.
	var sp *ux.Spinner
	if isTTY {
		sp = ux.NewSpinner(os.Stderr)
		sp.Start("Scanning")
		cfg.OnProgress = func(processed, total int) {
			sp.Update(fmt.Sprintf("Scanning (%d / %d files)", processed, total))
		}
	}

	cart := cartographer.New(cfg)

	if !isTTY {
		p.Textf("Scanning %s", scanPath)
	}
	if dryRun {
		p.Muted("  (dry-run: no memories will be written)")
	}

	result, err := cart.Scan(cmd.Context(), scanPath)

	if sp != nil {
		sp.Stop()
	}

	if isTTY {
		p.Textf("Scanning %s", scanPath)
	}

	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Print per-extractor results.
	printBootstrapProgress(p, result)

	// Write memories unless dry-run.
	written := 0
	if !dryRun && len(result.Drafts) > 0 {
		w, writeErr := writeDrafts(result.Drafts, targetScope.Path)
		if writeErr != nil {
			p.Warnf("write error: %v", writeErr)
		}
		written = w

	}

	p.Blank()
	if dryRun {
		p.Textf("Total: %d memories would be seeded in %.1fs.",
			len(result.Drafts), result.Duration().Seconds())
	} else {
		p.Textf("Total: %d memories seeded in %.1fs.",
			written, result.Duration().Seconds())
	}

	// After a real write, attempt landmark computation when corpus is large enough.
	if !dryRun {
		memDir := filepath.Join(targetScope.Path, "memory")
		cacheDir := filepath.Join(targetScope.Path, "cache")
		totalDocs := countMemoryDocs(memDir)

		// Always write tag graph for incremental future updates.
		_ = gardener.WriteTagGraph(memDir, cacheDir)

		if totalDocs >= gardener.MinDocsForLandmarks {
			n, err := gardener.ComputeLandmarks(memDir, 2.0)
			if err == nil {
				_ = n
				landmarkCount := countLandmarks(memDir)
				p.Checkf("%d landmarks identified", landmarkCount)

				// Gardener writes directly to JSON; reindex to sync SQLite.
				reIdx := storage.NewIndexedAdapter(targetScope.Path)
				_ = reIdx.Reindex()
				_ = reIdx.Close()
			}
		}

		// Build and open the memory graph unless suppressed.
		if !noGraph {
			data, graphErr := gardener.BuildGraphData(memDir, 50)
			if graphErr == nil && data.Stats.TotalDocs > 0 {
				outPath := filepath.Join(os.TempDir(), "mom-memory-graph.html")
				if writeErr := gardener.WriteGraphHTML(data, outPath); writeErr == nil {
					p.Checkf("Graph written to %s", outPath)
					if openErr := openBrowser(outPath); openErr != nil {
						p.Muted("  Open the file in your browser to view the graph.")
					}
				}
			}
		}
	}

	p.Blank()
	p.Bold("Suggested first questions")
	p.Chevron("\"What does this project do?\"")
	p.Chevron("\"Which dependencies drive the core behavior?\"")
	p.Chevron("\"What was the last major refactor about?\"")

	// Emit telemetry.
	emitter := herald.New(targetScope.Path, true)
	emitter.EmitCaptureEvent(herald.CaptureEvent{
		CaptureID:        fmt.Sprintf("bootstrap-%d", time.Now().UnixMilli()),
		TS:               time.Now().UTC().Format(time.RFC3339),
		ExtractorModel:   "cartographer",
		ExtractorVersion: "v0.8.0",
		MemoriesProposed: len(result.Drafts),
		MemoriesAccepted: written,
		Tags:             []string{"bootstrap"},
		Summary:          fmt.Sprintf("bootstrap scan of %s", filepath.Base(scanPath)),
	})

	return nil
}

// runMultiRepoBootstrap handles bootstrap for user/org scopes by scanning
// each child repo that has its own .mom/ independently, outputting per-repo
// progress grouped by repo name.
func runMultiRepoBootstrap(cmd *cobra.Command, scanPath string, targetScope scope.Scope, cfg cartographer.Config, dryRun bool) error {
	// Discover the parent dir: it's the directory containing the .mom/ (go one level up from .mom/).
	parentDir := filepath.Dir(targetScope.Path)

	// Find child repos: immediate children with .git/ (may or may not have .mom/).
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		return fmt.Errorf("reading parent dir: %w", err)
	}

	type repoEntry struct {
		root   string
		momDir string
	}
	var repos []repoEntry

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		child := filepath.Join(parentDir, e.Name())
		gitPath := filepath.Join(child, ".git")
		momPath := filepath.Join(child, ".mom")

		gitInfo, gitErr := os.Stat(gitPath)
		if gitErr != nil || !gitInfo.IsDir() {
			continue // not a git repo
		}

		momInfo, momErr := os.Stat(momPath)
		if momErr != nil || !momInfo.IsDir() {
			mp := ux.NewPrinter(cmd.OutOrStdout())
			mp.Warnf("%s — no .mom/ found, skipping (run 'mom init' in this repo first)", child)
			continue
		}

		repos = append(repos, repoEntry{root: child, momDir: momPath})
	}

	mp := ux.NewPrinter(cmd.OutOrStdout())
	if len(repos) == 0 {
		mp.Muted("No initialized child repos found. Run 'mom init' in each repo first.")
		return nil
	}

	mp.Textf("Multi-repo bootstrap: %d repos under %s", len(repos), parentDir)
	if dryRun {
		mp.Muted("  (dry-run: no memories will be written)")
	}

	totalProposed := 0
	totalWritten := 0

	isTTY := ux.IsTTY(cmd.OutOrStdout())

	for _, repo := range repos {
		repoName := filepath.Base(repo.root)
		mp.Blank()
		mp.Bold(fmt.Sprintf("  [%s]", repoName))

		repoCfg := cfg
		repoCfg.ScopeDir = repo.momDir

		var repoSp *ux.Spinner
		if isTTY {
			repoSp = ux.NewSpinner(os.Stderr)
			repoSp.Start(fmt.Sprintf("Scanning %s", repoName))
			repoCfg.OnProgress = func(processed, total int) {
				repoSp.Update(fmt.Sprintf("Scanning %s (%d / %d files)", repoName, processed, total))
			}
		}

		cart := cartographer.New(repoCfg)

		result, err := cart.Scan(cmd.Context(), repo.root)

		if repoSp != nil {
			repoSp.Stop()
		}

		if err != nil {
			mp.Warnf("scan error: %v", err)
			continue
		}

		printBootstrapProgress(mp, result)
		totalProposed += len(result.Drafts)

		if !dryRun && len(result.Drafts) > 0 {
			w, writeErr := writeDrafts(result.Drafts, repo.momDir)
			if writeErr != nil {
				mp.Warnf("write error: %v", writeErr)
			}
			totalWritten += w
			mp.Textf("    %d memories seeded in %.1fs.", w, result.Duration().Seconds())
		} else if dryRun {
			mp.Textf("    %d memories would be seeded.", len(result.Drafts))
		}
	}

	mp.Blank()
	if dryRun {
		mp.Textf("Total: %d memories would be seeded across %d repos.", totalProposed, len(repos))
	} else {
		mp.Textf("Total: %d memories seeded across %d repos.", totalWritten, len(repos))
	}

	return nil
}

// printBootstrapProgress prints the per-extractor breakdown and cache summary.
func printBootstrapProgress(p *ux.Printer, result *cartographer.Result) {
	order := []struct {
		key   string
		label string
	}{
		{"markdown", "Markdown"},
		{"dependencies", "Dependencies"},
		{"commits", "Commits"},
		{"todo-fixme", "TODO/FIXME"},
		{"ast", "AST"},
	}

	for _, item := range order {
		er, ok := result.ByExtractor[item.key]
		if !ok {
			continue
		}

		p.Checkf("%-16s — %3d memories", item.label, er.Count)

		// For AST, print per-language breakdown if we have data.
		if item.key == "ast" && len(result.ByLanguage) > 0 {
			langs := make([]string, 0, len(result.ByLanguage))
			for lang := range result.ByLanguage {
				langs = append(langs, lang)
			}
			sort.Strings(langs)
			for _, lang := range langs {
				count := result.ByLanguage[lang]
				displayLang := canonicalLanguageLabel(lang)
				p.Muted(fmt.Sprintf("    %-14s %d memories", displayLang, count))
			}
		}
	}

	// Cache summary line.
	total := result.CacheHits + result.CacheMisses
	if total > 0 {
		p.Checkf("%d cached · processing %d new", result.CacheHits, result.CacheMisses)
	}
}

// canonicalLanguageLabel returns a display-friendly label for an AST language tag.
func canonicalLanguageLabel(lang string) string {
	switch lang {
	case "go":
		return "Go"
	case "python":
		return "Python"
	case "javascript":
		return "JavaScript"
	case "typescript":
		return "TypeScript"
	case "tsx":
		return "TSX"
	case "rust":
		return "Rust"
	case "java":
		return "Java"
	case "ruby":
		return "Ruby"
	case "c":
		return "C"
	case "cpp":
		return "C++"
	case "csharp":
		return "C#"
	default:
		return lang
	}
}

// writeDrafts persists draft memories via IndexedAdapter (JSON + SQLite index).
// Returns the count of successfully written memories.
func writeDrafts(drafts []cartographer.Draft, momDir string) (int, error) {
	memDir := filepath.Join(momDir, "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		return 0, fmt.Errorf("creating memory dir: %w", err)
	}

	now := time.Now().UTC()
	var docs []*storage.Doc
	for _, d := range drafts {
		id := draftID(d)
		content := d.Content
		if content == nil {
			content = make(map[string]any)
		}
		content["summary"] = d.Summary

		docs = append(docs, &storage.Doc{
			ID:             id,
			Scope:          "project",
			Tags:           d.Tags,
			Created:        now,
			CreatedBy:      "cartographer",
			PromotionState: "draft",
			Classification: "INTERNAL",
			Content:        content,
		})
	}

	idx := storage.NewIndexedAdapter(momDir)
	defer idx.Close()

	if err := idx.BulkWrite(docs); err != nil {
		return 0, fmt.Errorf("bulk write: %w", err)
	}
	return len(docs), nil
}

// draftID generates a short, deterministic ID for a draft memory.
func draftID(d cartographer.Draft) string {
	raw := d.Summary + ":" + d.Provenance.SourceFile
	h := cartographer.DraftHash(raw)
	return "mem-" + h[:12]
}

// countMemoryDocs returns the total number of .json files in memDir.
func countMemoryDocs(memDir string) int {
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			n++
		}
	}
	return n
}
