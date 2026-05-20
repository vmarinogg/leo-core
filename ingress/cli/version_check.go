package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const githubReleasesURL = "https://api.github.com/repos/momhq/mom/releases/latest"

type versionCache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

func versionCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mom", "cache", "version-check.json")
}

// checkVersionCache reads the cached version check result.
// Returns a warning string if the cache is fresh (<24h) and latest > installed.
// Returns empty string otherwise.
func checkVersionCache() string {
	path := versionCachePath()
	if path == "" {
		return ""
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var cache versionCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return ""
	}

	// Cache expired — do not warn, let the async refresh handle it
	if time.Since(cache.CheckedAt) >= 24*time.Hour {
		return ""
	}

	if cache.LatestVersion == "" {
		return ""
	}

	if semverGreater(cache.LatestVersion, Version) {
		return fmt.Sprintf("MOM %s available. Run `brew upgrade mom` or `mom self-update`", cache.LatestVersion)
	}

	return ""
}

// refreshVersionCacheAsync fires a goroutine that fetches the latest release
// from GitHub and writes the cache. All errors are silently dropped.
func refreshVersionCacheAsync() {
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(githubReleasesURL)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return
		}

		var payload struct {
			TagName string `json:"tag_name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return
		}

		latest := strings.TrimPrefix(payload.TagName, "v")
		if latest == "" {
			return
		}

		cache := versionCache{
			CheckedAt:     time.Now().UTC(),
			LatestVersion: latest,
		}

		data, err := json.Marshal(cache)
		if err != nil {
			return
		}

		path := versionCachePath()
		if path == "" {
			return
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return
		}

		_ = os.WriteFile(path, data, 0644)
	}()
}

// semverGreater returns true if a > b using simple major.minor.patch comparison.
func semverGreater(a, b string) bool {
	ap := parseSemver(a)
	bp := parseSemver(b)

	for i := 0; i < 3; i++ {
		if ap[i] > bp[i] {
			return true
		}
		if ap[i] < bp[i] {
			return false
		}
	}
	return false
}

// parseSemver parses a version string like "1.2.3" or "v1.2.3" into [major, minor, patch].
func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var result [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		result[i] = n
	}
	return result
}
