package pathutil

import "path/filepath"

// CanonicalDir returns an absolute, symlink-resolved directory path when possible.
// It falls back to the absolute path (or original input) so callers can use it
// safely for paths that do not exist yet.
func CanonicalDir(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}
