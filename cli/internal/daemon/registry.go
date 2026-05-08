package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/momhq/mom/cli/internal/pathutil"
)

// RegistryEntry describes a single MOM-enabled project in the global registry.
type RegistryEntry struct {
	MomDir   string   `json:"momDir"`
	Runtimes []string `json:"runtimes"`
}

// Registry maps absolute project directory paths to their entries.
type Registry map[string]RegistryEntry

// RegistryDir returns ~/.mom/, creating it if needed.
func RegistryDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".mom")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// RegistryPath returns the path to ~/.mom/watch-registry.json.
func RegistryPath() (string, error) {
	dir, err := RegistryDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "watch-registry.json"), nil
}

// LoadRegistry reads the registry from disk. Returns an empty Registry if the file is missing.
func LoadRegistry() (Registry, error) {
	path, err := RegistryPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(Registry), nil
		}
		return nil, err
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parsing registry: %w", err)
	}
	if reg == nil {
		reg = make(Registry)
	}
	return canonicalizeRegistry(reg), nil
}

func canonicalizeRegistry(reg Registry) Registry {
	out := make(Registry, len(reg))
	for projectDir, entry := range reg {
		out[pathutil.CanonicalDir(projectDir)] = entry
	}
	return out
}

// SaveRegistry atomically writes the registry to disk (tmp + rename).
func SaveRegistry(reg Registry) error {
	path, err := RegistryPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// registryLockPath returns ~/.mom/watch-registry.lock.
func registryLockPath() (string, error) {
	dir, err := RegistryDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "watch-registry.lock"), nil
}

// withRegistryLock acquires an exclusive file lock, runs fn, then releases.
func withRegistryLock(fn func() error) error {
	lockPath, err := registryLockPath()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("opening lock file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck

	return fn()
}

// RegisterProject adds or updates a project in the registry (lock → load → upsert → save).
func RegisterProject(projectDir, momDir string, runtimes []string) error {
	canonicalProjectDir := pathutil.CanonicalDir(projectDir)
	return withRegistryLock(func() error {
		reg, err := LoadRegistry()
		if err != nil {
			return err
		}
		for existingProjectDir := range reg {
			if pathutil.CanonicalDir(existingProjectDir) == canonicalProjectDir {
				delete(reg, existingProjectDir)
			}
		}
		reg[canonicalProjectDir] = RegistryEntry{
			MomDir:   momDir,
			Runtimes: runtimes,
		}
		return SaveRegistry(reg)
	})
}

// UnregisterProject removes a project from the registry (lock → load → delete → save).
// If the registry becomes empty, the file is deleted.
func UnregisterProject(projectDir string) error {
	canonicalProjectDir := pathutil.CanonicalDir(projectDir)
	return withRegistryLock(func() error {
		reg, err := LoadRegistry()
		if err != nil {
			return err
		}
		for existingProjectDir := range reg {
			if existingProjectDir == projectDir || pathutil.CanonicalDir(existingProjectDir) == canonicalProjectDir {
				delete(reg, existingProjectDir)
			}
		}
		if len(reg) == 0 {
			path, err := RegistryPath()
			if err != nil {
				return err
			}
			_ = os.Remove(path)
			return nil
		}
		return SaveRegistry(reg)
	})
}

// IsRegistryEmpty returns true if the registry has no entries.
func IsRegistryEmpty() (bool, error) {
	reg, err := LoadRegistry()
	if err != nil {
		return false, err
	}
	return len(reg) == 0, nil
}

// GlobalLogsDir returns ~/.mom/logs/, creating it if needed.
func GlobalLogsDir() (string, error) {
	dir, err := RegistryDir()
	if err != nil {
		return "", err
	}
	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return "", err
	}
	return logsDir, nil
}
