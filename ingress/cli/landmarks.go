package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/momhq/mom/storage/memory"
)

// countLandmarks returns the number of docs with landmark=true in memDir.
func countLandmarks(memDir string) int {
	entries, _ := os.ReadDir(memDir)
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		doc, err := memory.LoadDoc(filepath.Join(memDir, e.Name()))
		if err != nil {
			continue
		}
		if doc.Landmark {
			n++
		}
	}
	return n
}
