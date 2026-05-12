package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/momhq/mom/cli/internal/adapters/harness"
	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/config"
	"github.com/momhq/mom/cli/internal/daemon"
	"github.com/momhq/mom/cli/internal/pathutil"
	"github.com/momhq/mom/cli/internal/watcher"
)

// harnessTranscriptDir resolves a Harness's default transcript directory via
// its TranscriptSource implementation. Returns "" if the Harness is unknown
// or has no transcript source.
func harnessTranscriptDir(name string) string {
	reg := harness.NewRegistry("")
	h, ok := reg.Get(name)
	if !ok {
		return ""
	}
	if ts, ok := h.(harness.TranscriptSource); ok {
		return ts.DefaultTranscriptDir()
	}
	return ""
}

func resolveMomContext(cwd string) (projectDir string, momDir string, err error) {
	cwd = pathutil.CanonicalDir(cwd)
	centralDir, err := centralvault.Dir()
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(filepath.Join(centralDir, "config.yaml")); err != nil {
		return "", "", fmt.Errorf("no MOM configuration found from %q — run mom init first", cwd)
	}
	return cwd, centralDir, nil
}

// ensureGlobalDaemon registers the project in the global watch registry and
// ensures the single global daemon is running. Also cleans up legacy per-project agents.
// Skipped when MOM_NO_DAEMON=1 or when running inside a test binary.
func ensureGlobalDaemon(projectRoot, momDir string, harnesses []string) error {
	projectRoot = pathutil.CanonicalDir(projectRoot)
	if os.Getenv("MOM_NO_DAEMON") == "1" {
		return nil
	}

	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	// Skip daemon install when running inside `go test`.
	if strings.HasSuffix(bin, ".test") || strings.Contains(bin, "/_test/") {
		return nil
	}

	// Do NOT resolve symlinks — global daemon uses the symlink path so
	// brew upgrade / package updates are picked up on restart.

	// Register this project in the global registry.
	if err := daemon.RegisterProject(projectRoot, momDir, harnesses); err != nil {
		return fmt.Errorf("registering project: %w", err)
	}

	// Start global daemon if not already running.
	h, err := daemon.StatusGlobal()
	if err == nil && len(h.Services) > 0 && h.Services[0].DaemonRunning {
		// Already running — cleanup legacy and return.
		_ = daemon.CleanupLegacy(projectRoot)
		return nil
	}

	if err := daemon.InstallGlobal(daemon.GlobalServiceConfig{MomBinary: bin}); err != nil {
		return fmt.Errorf("installing global daemon: %w", err)
	}

	_ = daemon.CleanupLegacy(projectRoot)
	return nil
}

// unregisterProject removes a project from the global watch registry,
// cleans up legacy agents, and stops the global daemon if no projects remain.
func unregisterProject(projectRoot, momDir string) error {
	projectRoot = pathutil.CanonicalDir(projectRoot)
	if err := daemon.UnregisterProject(projectRoot); err != nil {
		return fmt.Errorf("unregistering project: %w", err)
	}

	_ = daemon.CleanupLegacy(projectRoot)

	empty, err := daemon.IsRegistryEmpty()
	if err != nil {
		return err
	}
	if empty {
		return daemon.UninstallGlobal()
	}
	return nil
}

// sweepTranscripts runs a one-shot catch-up sweep for all watcher-capable
// harnesses. Best-effort: errors are logged to stderr, never returned.
func sweepTranscripts(projectDir, momDir string) {
	cfg, err := config.Load(momDir)
	if err != nil {
		return
	}

	sources := buildWatcherSources(cfg, projectDir)
	if len(sources) == 0 {
		return
	}

	// Best-effort sweep — open the central vault on the spot. The
	// helper is short-lived so leaving the vault uncloned is fine
	// for this one-shot path.
	bus := newProjectBus(openCentralWorkers())
	w, err := watcher.New(watcher.Config{
		ProjectDir: projectDir,
		MomDir:     momDir,
		Sources:    sources,
		SweepOnly:  true,
		Bus:        bus,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mom] sweep: %v\n", err)
		return
	}
	w.Sweep()
}

// buildWatcherSources builds watcher.Source entries from config for all
// watcher-capable Harnesses.
func buildWatcherSources(cfg *config.Config, projectDir string) []watcher.Source {
	var sources []watcher.Source
	for _, rt := range cfg.EnabledHarnesses() {
		var (
			override string
			adapter  watcher.Adapter
		)
		switch rt {
		case "claude":
			override = cfg.Watcher.TranscriptDir
			adapter = watcher.NewClaudeAdapter()
		case "windsurf":
			override = cfg.Watcher.WindsurfTranscriptDir
			adapter = &watcher.WindsurfAdapter{ProjectDir: projectDir}
		case "pi":
			override = cfg.Watcher.PiTranscriptDir
			adapter = watcher.NewPiAdapter()
		case "codex":
			override = cfg.Watcher.CodexTranscriptDir
			adapter = watcher.NewCodexAdapter()
		default:
			continue
		}
		dir := override
		if dir == "" {
			dir = harnessTranscriptDir(rt)
		}
		if dir == "" {
			continue
		}
		sources = append(sources, watcher.Source{
			Harness:       rt,
			TranscriptDir: dir,
			Adapter:       adapter,
		})
	}
	return sources
}
