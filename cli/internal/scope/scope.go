// Package scope handles multi-level .mom/ discovery and write routing.
//
// Walk-up discovery finds every ancestor directory that contains a .mom/ folder,
// starting from cwd and stopping at (but not crossing) $HOME. Symlinks are
// skipped intentionally: following symlinks could create cycles or traverse
// unexpected filesystem trees.
//
// The discovered list is ordered nearest-first (most specific scope first).
// This mirrors how npm/cargo workspace resolution works.
package scope

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/momhq/mom/cli/internal/config"
)

// ValidScopeLabels is the set of accepted scope values in config.yaml.
var ValidScopeLabels = map[string]bool{
	"user":      true,
	"org":       true,
	"repo":      true,
	"workspace": true,
	"custom":    true,
}

// Scope represents a single .mom/ install found during walk-up.
type Scope struct {
	// Path is the absolute path to the .mom/ directory.
	Path string
	// Label is the value of the scope: field in config.yaml.
	// Defaults to "repo" when absent or empty.
	Label string
}

// MemoryCount returns the number of JSON files in the memory/ subdirectory.
// Returns 0 on any error (missing dir, unreadable, etc.).
func (s Scope) MemoryCount() int {
	memDir := filepath.Join(s.Path, "memory")
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

// Walk walks up from cwd and returns every ancestor directory that contains a
// .mom/ subdirectory, ordered nearest-first. It stops at $HOME (exclusive) —
// it never walks above the user's home directory.
//
// Symlinks are skipped: if a directory entry is a symlink, it is not followed.
// This prevents cycles and avoids traversing unexpected paths.
func Walk(cwd string) []Scope {
	home, err := os.UserHomeDir()
	if err != nil {
		// If we can't determine $HOME, fall back to stopping at filesystem root.
		home = string(filepath.Separator)
	}

	var scopes []Scope
	dir := cwd

	for {
		candidate := filepath.Join(dir, ".mom")
		if isRealDir(candidate) {
			label := loadScopeLabel(candidate)
			// $HOME/.mom/ with no explicit scope config defaults to "user".
			if label == "repo" && dir == home {
				label = "user"
			}
			scopes = append(scopes, Scope{Path: candidate, Label: label})
		}

		// Stop after processing $HOME itself (walk includes $HOME but not above).
		if dir == home {
			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			break
		}
		dir = parent
	}

	return scopes
}

// NearestWritable returns the nearest (most specific) scope for cwd.
// If no .mom/ exists, it returns a zero Scope and false.
func NearestWritable(cwd string) (Scope, bool) {
	scopes := Walk(cwd)
	if len(scopes) == 0 {
		return Scope{}, false
	}
	return scopes[0], true
}

// FindByLabel walks from cwd and returns the first scope whose label matches
// the given value. Returns Scope{} and false if none found.
func FindByLabel(cwd, label string) (Scope, bool) {
	for _, s := range Walk(cwd) {
		if s.Label == label {
			return s, true
		}
	}
	return Scope{}, false
}

// ValidateLabel returns an error if label is not in ValidScopeLabels.
func ValidateLabel(label string) error {
	if label == "" || ValidScopeLabels[label] {
		return nil
	}
	return fmt.Errorf("invalid scope %q: must be one of user, org, repo, workspace, custom", label)
}

// loadScopeLabel reads the scope field from config.yaml in momDir.
// Returns "repo" on any error or when the field is absent/empty.
func loadScopeLabel(momDir string) string {
	cfg, err := config.Load(momDir)
	if err != nil {
		return "repo"
	}
	if cfg.Scope == "" {
		return "repo"
	}
	return cfg.Scope
}

// isRealDir returns true if path is a real directory (not a symlink to a dir).
// Symlinks are intentionally excluded.
func isRealDir(path string) bool {
	// Use Lstat to detect symlinks without following them.
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	// Symlinks are skipped.
	if info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return info.IsDir()
}
