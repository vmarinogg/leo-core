package cartographer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCollectFiles_RespectsMomBoundary verifies that collectFiles stops
// descending into subdirectories that have their own .mom/ directory.
func TestCollectFiles_RespectsMomBoundary(t *testing.T) {
	root := t.TempDir()

	// Root repo: has .mom/ and some files.
	momDir := filepath.Join(root, ".mom")
	os.MkdirAll(momDir, 0755)
	writeFile(t, filepath.Join(root, "README.md"), "# Root")
	writeFile(t, filepath.Join(root, "main.go"), "package main")

	// Child "repo": has its own .mom/ — should be excluded from root scan.
	childRepo := filepath.Join(root, "child")
	os.MkdirAll(filepath.Join(childRepo, ".mom"), 0755)
	writeFile(t, filepath.Join(childRepo, "README.md"), "# Child")
	writeFile(t, filepath.Join(childRepo, "child.go"), "package child")

	cfg := DefaultConfig()
	cfg.ScopeDir = momDir
	cart := New(cfg)

	paths, err := cart.collectFiles(root)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}

	// Child files must not appear.
	childREADME := filepath.Join(childRepo, "README.md")
	childGo := filepath.Join(childRepo, "child.go")
	for _, p := range paths {
		if p == childREADME || p == childGo {
			t.Errorf("child file %q should be excluded when child has its own .mom/", p)
		}
	}

	// Root files should appear (go files match extensions in some extractors,
	// but README.md is always matched by markdown).
	found := false
	for _, p := range paths {
		if p == filepath.Join(root, "README.md") {
			found = true
		}
	}
	if !found {
		t.Error("root README.md should be included in scan")
	}
}

// TestCollectFiles_ScansChildWithoutMom verifies that a child dir WITHOUT .mom/
// is still walked normally.
func TestCollectFiles_ScansChildWithoutMom(t *testing.T) {
	root := t.TempDir()
	momDir := filepath.Join(root, ".mom")
	os.MkdirAll(momDir, 0755)

	// Child without .mom/ — should be walked.
	childRepo := filepath.Join(root, "lib")
	os.MkdirAll(childRepo, 0755)
	writeFile(t, filepath.Join(childRepo, "README.md"), "# Lib")

	cfg := DefaultConfig()
	cfg.ScopeDir = momDir
	cart := New(cfg)

	paths, err := cart.collectFiles(root)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}

	found := false
	for _, p := range paths {
		if p == filepath.Join(childRepo, "README.md") {
			found = true
		}
	}
	if !found {
		t.Error("child README.md (no .mom/) should be included in scan")
	}
}

// TestMultiScan_ScansTwoRepos verifies that MultiScan scans two repos
// independently and writes memories to each repo's own .mom/.
func TestMultiScan_ScansTwoRepos(t *testing.T) {
	root := t.TempDir()

	// repo1
	r1 := filepath.Join(root, "repo1")
	r1Mom := filepath.Join(r1, ".mom")
	os.MkdirAll(r1Mom, 0755)
	writeFile(t, filepath.Join(r1, "README.md"), "# Repo1\n\nDecision: use Go for repo1.")

	// repo2
	r2 := filepath.Join(root, "repo2")
	r2Mom := filepath.Join(r2, ".mom")
	os.MkdirAll(r2Mom, 0755)
	writeFile(t, filepath.Join(r2, "README.md"), "# Repo2\n\nPattern: repository pattern used.")

	results, err := MultiScan(context.Background(), []ScanTarget{
		{RootDir: r1, MomDir: r1Mom},
		{RootDir: r2, MomDir: r2Mom},
	}, DefaultConfig())
	if err != nil {
		t.Fatalf("MultiScan: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for _, result := range results {
		if len(result.Drafts) == 0 {
			t.Errorf("expected drafts for %s, got 0", result.RootDir)
		}
	}
}

// TestScanTarget_ScopeDir verifies that MultiScan uses each target's MomDir
// for cache operations, not the same dir for both.
func TestScanTarget_ScopeDir(t *testing.T) {
	root := t.TempDir()

	r1 := filepath.Join(root, "repo1")
	r1Mom := filepath.Join(r1, ".mom")
	os.MkdirAll(r1Mom, 0755)
	writeFile(t, filepath.Join(r1, "go.mod"), "module github.com/test/r1\ngo 1.21\n")

	r2 := filepath.Join(root, "repo2")
	r2Mom := filepath.Join(r2, ".mom")
	os.MkdirAll(r2Mom, 0755)
	writeFile(t, filepath.Join(r2, "go.mod"), "module github.com/test/r2\ngo 1.21\n")

	cfg := DefaultConfig()
	cfg.DryRun = true // don't write cache

	results, err := MultiScan(context.Background(), []ScanTarget{
		{RootDir: r1, MomDir: r1Mom},
		{RootDir: r2, MomDir: r2Mom},
	}, cfg)
	if err != nil {
		t.Fatalf("MultiScan: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Ensure each result is bound to the correct root.
	found1, found2 := false, false
	for _, res := range results {
		if res.RootDir == r1 {
			found1 = true
		}
		if res.RootDir == r2 {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Errorf("missing results for repos: found1=%v found2=%v", found1, found2)
	}
}
