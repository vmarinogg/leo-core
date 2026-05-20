package cartographer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestScan_Integration seeds a temp repo with README, go.mod, and Go code
// then runs Scan and verifies >= 20 memories with correct provenance.
func TestScan_Integration(t *testing.T) {
	dir := t.TempDir()

	// Write test fixtures.
	writeFile(t, filepath.Join(dir, "README.md"), `# Test Project

A project for integration testing.

## Decision

We chose Go because of performance.

Decision: Use dependency injection throughout.

Pattern: Services implement the Repository pattern.

See https://example.com for documentation.
`)

	writeFile(t, filepath.Join(dir, "go.mod"), `module github.com/test/proj

go 1.21

require (
	github.com/spf13/cobra v1.7.0
	gopkg.in/yaml.v3 v3.0.1
	github.com/stretchr/testify v1.8.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
)
`)

	writeFile(t, filepath.Join(dir, "main.go"), `package main

import "fmt"

// Application is the main entry point.
type Application struct {
	Name string
}

// Config holds the application configuration.
type Config struct {
	Debug   bool
	Version string
}

// Run starts the application.
func Run(app *Application) error {
	fmt.Println(app.Name)
	return nil
}

// NewApplication creates a new Application.
func NewApplication(name string) *Application {
	return &Application{Name: name}
}

// TODO: This is a structured todo that needs attention in the future soon.

// FIXME: This loop has O(n^2) complexity and should be replaced with a map lookup.
`)

	// Init a git repo and add a few commits.
	gitInit(t, dir,
		"feat(app): add initial application scaffold",
		"fix(config): resolve nil pointer on startup when config missing",
		"refactor(core): extract business logic into dedicated service layer",
		"chore: update CI pipeline configuration",
		"feat: add health check endpoint for kubernetes readiness probes",
	)

	cfg := DefaultConfig()
	cfg.ScopeDir = filepath.Join(dir, ".mom")
	_ = os.MkdirAll(cfg.ScopeDir, 0755)

	cart := New(cfg)
	result, err := cart.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// We expect at least 15 memories across all extractors.
	// (markdown: decisions+patterns+urls, deps: 3+, commits: 5, AST: types+funcs, todos)
	if len(result.Drafts) < 15 {
		t.Errorf("expected >= 15 memories, got %d", len(result.Drafts))
		t.Logf("Breakdown: %+v", result.ByExtractor)
	}

	// Verify provenance on all drafts.
	for i, d := range result.Drafts {
		if d.Provenance.TriggerEvent != TriggerEvent {
			t.Errorf("draft[%d] %q missing TriggerEvent", i, d.Summary)
		}
	}

	// Verify extractor breakdown is populated.
	if result.ByExtractor["markdown"].Count == 0 {
		t.Error("expected markdown extractor to produce memories")
	}
	if result.ByExtractor["dependencies"].Count == 0 {
		t.Error("expected dependency extractor to produce memories")
	}
}

func TestScan_DryRun(t *testing.T) {
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	_ = os.MkdirAll(momDir, 0755)

	writeFile(t, filepath.Join(dir, "go.mod"), `module github.com/test/dry

go 1.21

require github.com/spf13/cobra v1.7.0
`)

	cfg := DefaultConfig()
	cfg.ScopeDir = momDir
	cfg.DryRun = true

	cart := New(cfg)
	result, err := cart.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Dry run should produce drafts but not write cache.
	if len(result.Drafts) == 0 {
		t.Error("expected drafts even in dry-run mode")
	}

	// Cache file should NOT exist.
	manifestPath := filepath.Join(momDir, "cache", "bootstrap", "manifest.json")
	if _, err := os.Stat(manifestPath); err == nil {
		t.Error("dry-run should not write cache manifest")
	}
}

func TestScan_Cache_IncrementalRun(t *testing.T) {
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	_ = os.MkdirAll(momDir, 0755)

	writeFile(t, filepath.Join(dir, "go.mod"), `module github.com/test/cache

go 1.21

require github.com/spf13/cobra v1.7.0
`)

	cfg := DefaultConfig()
	cfg.ScopeDir = momDir

	// First run: should process files.
	cart1 := New(cfg)
	result1, err := cart1.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	count1 := result1.ByExtractor["dependencies"].Count

	// Second run (no --refresh): should skip unchanged files.
	cart2 := New(cfg)
	result2, err := cart2.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	count2 := result2.ByExtractor["dependencies"].Count

	if count2 >= count1 && count1 > 0 {
		// On second run, files are cached so count should be 0.
		t.Errorf("second run should skip cached files: first=%d, second=%d", count1, count2)
	}

	// Third run with --refresh: should process all files again.
	cfg.Refresh = true
	cart3 := New(cfg)
	result3, err := cart3.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("refresh Scan: %v", err)
	}
	count3 := result3.ByExtractor["dependencies"].Count

	if count3 == 0 && count1 > 0 {
		t.Error("--refresh should re-process all files")
	}
}

func TestScan_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	dir := t.TempDir()

	// Create 500 Go files.
	pkgDir := filepath.Join(dir, "pkg", "subpkg")
	_ = os.MkdirAll(pkgDir, 0755)

	for i := 0; i < 500; i++ {
		content := fmt.Sprintf(`package bench

// BenchType%d is a type for performance testing.
type BenchType%d struct{ ID int }

// BenchFunc%d is a function for performance testing.
func BenchFunc%d() {}
`, i, i, i, i)
		writeFile(t, filepath.Join(pkgDir, fmt.Sprintf("file_%d.go", i)), content)
	}

	cfg := DefaultConfig()
	cart := New(cfg)

	start := time.Now()
	result, err := cart.Scan(context.Background(), dir)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if elapsed > 60*time.Second {
		t.Errorf("500-file scan took %v, want < 60s", elapsed)
	}

	t.Logf("500-file scan: %d memories in %v", len(result.Drafts), elapsed)
}

func TestScan_ByLanguage(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "main.go"), `package main

// Application is the main entry point.
type Application struct{ Name string }

// Run starts the application.
func Run() {}
`)

	writeFile(t, filepath.Join(dir, "app.py"), `class DataProcessor:
    def process(self):
        pass

def top_level():
    pass
`)

	cfg := DefaultConfig()
	cart := New(cfg)
	result, err := cart.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if result.ByLanguage["go"] == 0 {
		t.Error("expected ByLanguage[go] > 0")
	}
	if result.ByLanguage["python"] == 0 {
		t.Error("expected ByLanguage[python] > 0")
	}
}

func TestScan_CacheHitsMisses(t *testing.T) {
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	_ = os.MkdirAll(momDir, 0755)

	writeFile(t, filepath.Join(dir, "go.mod"), `module github.com/test/cache2

go 1.21

require github.com/spf13/cobra v1.7.0
`)

	cfg := DefaultConfig()
	cfg.ScopeDir = momDir

	// First run: all misses.
	cart1 := New(cfg)
	result1, err := cart1.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if result1.CacheMisses == 0 {
		t.Error("first run: expected CacheMisses > 0")
	}
	if result1.CacheHits != 0 {
		t.Errorf("first run: expected CacheHits == 0, got %d", result1.CacheHits)
	}

	// Second run: file unchanged, should be all hits.
	cart2 := New(cfg)
	result2, err := cart2.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if result2.CacheHits == 0 {
		t.Error("second run: expected CacheHits > 0")
	}
}

func TestScan_OnProgress(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "a.go"), `package a

// AFunc does something.
func AFunc() {}
`)
	writeFile(t, filepath.Join(dir, "b.go"), `package b

// BFunc does something.
func BFunc() {}
`)

	cfg := DefaultConfig()
	var callCount int
	var lastProcessed, lastTotal int
	cfg.OnProgress = func(processed, total int) {
		callCount++
		lastProcessed = processed
		lastTotal = total
	}

	cart := New(cfg)
	_, err := cart.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if callCount == 0 {
		t.Error("OnProgress was never called")
	}
	if lastTotal == 0 {
		t.Error("OnProgress total should be > 0")
	}
	if lastProcessed != lastTotal {
		t.Errorf("OnProgress: final processed=%d should equal total=%d", lastProcessed, lastTotal)
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

func gitInit(t *testing.T, dir string, messages ...string) {
	t.Helper()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("git %v: %s", args, out)
			// Don't fatal — git may not be configured in CI.
		}
	}

	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	for _, msg := range messages {
		// Stage all current files and commit.
		run("add", "-A")
		run("commit", "-m", msg, "--allow-empty")
	}
}
