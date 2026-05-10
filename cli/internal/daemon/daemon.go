// Package daemon manages platform-specific background services for MOM's
// filesystem watcher. It installs two services per project:
//
//   - Layer 0: a persistent daemon running `mom watch` with fsnotify (real-time)
//   - Layer 1: a periodic timer running `mom watch --sweep` every 2 minutes (fallback)
//
// On macOS this uses launchd (~/Library/LaunchAgents/), on Linux systemd user
// units (~/.config/systemd/user/). Both are idempotent — calling Install twice
// updates the service files and reloads.
package daemon

import (
	"time"
)

// ServiceConfig holds the info needed to generate and manage service files.
type ServiceConfig struct {
	// ProjectDir is the absolute path to the project root (parent of .mom/).
	ProjectDir string
	// MomDir is the absolute path to the .mom/ directory.
	MomDir string
	// Harnesses is the list of enabled harnesses (kept for reference, not used by daemon).
	Harnesses []string
	// MomBinary is the absolute path to the mom binary.
	MomBinary string
}

// ServiceHealth describes the state of a daemon+timer pair.
type ServiceHealth struct {
	DaemonRunning bool
	TimerActive   bool
	DaemonLabel   string
	TimerLabel    string
}

// Health describes the current state of the daemon and timer services for a project.
type Health struct {
	Services     []ServiceHealth
	LastActivity time.Time
	Platform     string // "launchd", "systemd", "unsupported"
}

// GlobalServiceConfig holds info for the single global daemon.
type GlobalServiceConfig struct {
	// MomBinary is the path to the mom binary (use symlink path, not resolved).
	MomBinary string
}
