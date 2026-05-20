package cartographer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// CacheEntry records the last-seen state of a file.
type CacheEntry struct {
	SHA256        string `json:"sha256"`
	LastScannedAt string `json:"last_scanned_at"`
	DraftCount    int    `json:"draft_count"`
}

// Cache is a file-level SHA256 cache stored at <momDir>/cache/bootstrap/manifest.json.
type Cache struct {
	mu       sync.RWMutex
	manifest map[string]CacheEntry
	path     string // absolute path to manifest.json
}

// NewCache creates a Cache for the given momDir. If momDir is empty or the
// manifest does not exist, an empty in-memory cache is returned.
func NewCache(momDir string) *Cache {
	c := &Cache{
		manifest: make(map[string]CacheEntry),
	}
	if momDir == "" {
		return c
	}

	dir := filepath.Join(momDir, "cache", "bootstrap")
	c.path = filepath.Join(dir, "manifest.json")

	data, err := os.ReadFile(c.path)
	if err != nil {
		return c // file does not exist yet; start empty
	}

	var m map[string]CacheEntry
	if err := json.Unmarshal(data, &m); err == nil {
		c.manifest = m
	}
	return c
}

// Get returns the CacheEntry for the given file path, if present.
func (c *Cache) Get(path string) (CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.manifest[path]
	return e, ok
}

// Set records a CacheEntry for the given file path.
func (c *Cache) Set(path string, entry CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.manifest[path] = entry
}

// Save persists the manifest to disk. It creates parent directories as needed.
func (c *Cache) Save() error {
	if c.path == "" {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c.manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(c.path, data, 0644)
}

// Reset clears the in-memory manifest (used by --refresh).
func (c *Cache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.manifest = make(map[string]CacheEntry)
}
