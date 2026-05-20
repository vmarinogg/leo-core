package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// findMomDir walks up from cwd to find a .mom/ directory.
func findMomDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		momCandidate := filepath.Join(dir, ".mom")
		if info, err := os.Stat(momCandidate); err == nil && info.IsDir() {
			if isMomProject(momCandidate) {
				return momCandidate, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no .mom/ directory found — run 'mom init' first")
}

// isMomProject returns true if dir looks like a MOM project directory
// (has config.yaml, memory/, or index.json). This prevents ~/.mom/cache/
// (created by version check) from being mistaken for a project.
func isMomProject(dir string) bool {
	markers := []string{"config.yaml", "memory"}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}
