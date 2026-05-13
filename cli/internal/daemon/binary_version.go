package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BinaryVersion is the sentinel record of which binary the global watch
// daemon was installed with. ensureGlobalDaemon uses it to detect when
// the on-disk binary has been upgraded under a running daemon (typical
// failure mode: `brew upgrade mom` swaps the Cellar path while the
// daemon process keeps executing the old fd).
type BinaryVersion struct {
	Path  string    `json:"path"`
	MTime time.Time `json:"mtime"`
}

// BinaryVersionSentinelPath returns the path of the version sentinel.
// Lives next to the watch registry under ~/.mom/.
func BinaryVersionSentinelPath() (string, error) {
	dir, err := RegistryDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon-binary.json"), nil
}

// RecordBinaryVersion writes the sentinel for binaryPath. Resolves
// symlinks before stat'ing so brew-style /opt/homebrew/bin/mom is
// recorded against its current Cellar target — a subsequent brew
// upgrade that re-points the symlink registers as a mismatch.
func RecordBinaryVersion(binaryPath string) error {
	bv, err := snapshotBinary(binaryPath)
	if err != nil {
		return err
	}
	path, err := BinaryVersionSentinelPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(bv, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal binary version: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// BinaryVersionMatches reports whether the sentinel records the same
// resolved path and mtime as binaryPath does right now. Returns false
// when the sentinel is missing, malformed, or records different values
// — every divergence is "needs reinstall."
func BinaryVersionMatches(binaryPath string) (bool, error) {
	path, err := BinaryVersionSentinelPath()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var recorded BinaryVersion
	if err := json.Unmarshal(data, &recorded); err != nil {
		return false, nil // malformed → mismatch
	}
	current, err := snapshotBinary(binaryPath)
	if err != nil {
		return false, err
	}
	return recorded.Path == current.Path && recorded.MTime.Equal(current.MTime), nil
}

// snapshotBinary resolves binaryPath through any symlinks and returns
// the canonical path + mtime.
func snapshotBinary(binaryPath string) (BinaryVersion, error) {
	resolved, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return BinaryVersion{}, fmt.Errorf("resolve %s: %w", binaryPath, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return BinaryVersion{}, fmt.Errorf("stat %s: %w", resolved, err)
	}
	return BinaryVersion{Path: resolved, MTime: info.ModTime().UTC()}, nil
}
