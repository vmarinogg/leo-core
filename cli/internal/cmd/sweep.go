package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/momhq/mom/cli/internal/config"
)

// SweepResult holds the outcome of a sweep operation.
type SweepResult struct {
	Deleted    int
	BytesFreed int64
	Errors     int
}

// sweep deletes old .jsonl files from .mom/raw/ based on retention config.
// Safe: never deletes files modified today, only touches *.jsonl files.
func sweep(momDir string, cfg config.RawMemoriesConfig) SweepResult {
	var result SweepResult

	retentionDays := cfg.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 30
	}

	rawDir := filepath.Join(momDir, "raw")
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return result
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	today := time.Now().Truncate(24 * time.Hour)

	var deleted []string
	for _, e := range entries {
		isCursor := strings.HasPrefix(e.Name(), ".cursor-")
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".jsonl") && !isCursor) {
			continue
		}

		info, err := e.Info()
		if err != nil {
			result.Errors++
			continue
		}

		modTime := info.ModTime()

		// Never delete files modified today.
		if !modTime.Before(today) {
			continue
		}

		// Delete if older than retention period.
		if modTime.Before(cutoff) {
			path := filepath.Join(rawDir, e.Name())
			size := info.Size()
			if err := os.Remove(path); err != nil {
				result.Errors++
				continue
			}
			result.Deleted++
			result.BytesFreed += size
			deleted = append(deleted, fmt.Sprintf("%s (%.1f MB)", e.Name(), float64(size)/(1024*1024)))
		}
	}

	// Log deletions.
	if len(deleted) > 0 {
		logSweep(momDir, deleted, result.BytesFreed)
	}

	return result
}

// logSweep appends deletion records to .mom/logs/sweep.log.
func logSweep(momDir string, deleted []string, totalBytes int64) {
	logsDir := filepath.Join(momDir, "logs")
	_ = os.MkdirAll(logsDir, 0755)
	logFile := filepath.Join(logsDir, "sweep.log")

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	ts := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(f, "%s sweep: deleted %d files, freed %.1f MB\n", ts, len(deleted), float64(totalBytes)/(1024*1024))
	for _, d := range deleted {
		fmt.Fprintf(f, "  - %s\n", d)
	}
}
