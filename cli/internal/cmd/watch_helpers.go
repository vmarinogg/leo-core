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

	// Prune stale pre-v0.40 registry entries before registering this project.
	_, _ = daemon.PruneInvalidRegistry()

	// ADR 0016: require an explicit project binding before adding this
	// directory to the daemon registry. Without this, running `mom init`
	// or `mom upgrade` from $HOME (or any unrelated cwd) would silently
	// promote that directory into a permanently-watched project.
	if _, err := os.Stat(filepath.Join(projectRoot, ".mom-project.yaml")); err != nil {
		return fmt.Errorf("refusing to watch %s: no .mom-project.yaml binding (run `mom project bind <id>`)", projectRoot)
	}

	// Register this project in the global registry.
	if err := daemon.RegisterProject(projectRoot, momDir, harnesses); err != nil {
		return fmt.Errorf("registering project: %w", err)
	}

	// Start global daemon if not already running.
	h, err := daemon.StatusGlobal()
	if err == nil && len(h.Services) > 0 && h.Services[0].DaemonRunning {
		// Daemon process is alive, but a running daemon executes the
		// binary it was launched with — `brew upgrade mom` (or any
		// rebuild) leaves the daemon serving the old code. The sentinel
		// recorded at install time tells us whether the binary on disk
		// has moved. Mismatch → unload so the install path below
		// re-spawns against the current binary (ADR-pointer: #338).
		match, _ := daemon.BinaryVersionMatches(bin)
		if match {
			_ = daemon.CleanupLegacy(projectRoot)
			return nil
		}
		_ = daemon.UninstallGlobal()
	}

	if err := daemon.InstallGlobal(daemon.GlobalServiceConfig{MomBinary: bin}); err != nil {
		return fmt.Errorf("installing global daemon: %w", err)
	}
	// Best-effort: record the binary identity so the next ensureGlobal
	// call can detect a future upgrade. Failure to write the sentinel
	// is non-fatal — at worst the next call treats the daemon as stale
	// and reinstalls unnecessarily.
	_ = daemon.RecordBinaryVersion(bin)

	_ = daemon.CleanupLegacy(projectRoot)
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
