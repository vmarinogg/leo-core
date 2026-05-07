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
	"github.com/momhq/mom/cli/internal/scope"
	"github.com/momhq/mom/cli/internal/ux"
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
	if sc, ok := scope.NearestWritable(cwd); ok {
		return filepath.Dir(sc.Path), sc.Path, nil
	}
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
func ensureGlobalDaemon(projectRoot, momDir string, runtimes []string) error {
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
	if err := daemon.RegisterProject(projectRoot, momDir, runtimes); err != nil {
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

// runWatchInstall handles `mom watch --install`.
func runWatchInstall(projectRoot, momDir string, p *ux.Printer) error {
	cfg, err := config.Load(momDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	sp := ux.NewSpinner(os.Stderr)
	sp.Start("Installing global watch daemon")
	installErr := ensureGlobalDaemon(projectRoot, momDir, cfg.EnabledHarnesses())
	if installErr != nil {
		sp.StopFail()
		return fmt.Errorf("installing daemon: %w", installErr)
	}
	sp.Stop()

	runtimes := watcherRuntimes(cfg)
	p.Check("global watch daemon installed")
	p.Chevron(fmt.Sprintf("runtimes: %s", strings.Join(runtimes, ", ")))

	h, err := daemon.StatusGlobal()
	if err == nil && len(h.Services) > 0 {
		p.Chevron(fmt.Sprintf("daemon: %s", h.Services[0].DaemonLabel))
		p.Chevron(fmt.Sprintf("timer:  %s", h.Services[0].TimerLabel))
	}
	return nil
}

// runWatchUninstall handles `mom watch --uninstall`.
func runWatchUninstall(projectRoot, momDir string, p *ux.Printer) error {
	sp := ux.NewSpinner(os.Stderr)
	sp.Start("Removing watch daemon")
	uninstallErr := unregisterProject(projectRoot, momDir)
	if uninstallErr != nil {
		sp.StopFail()
		return fmt.Errorf("uninstalling daemon: %w", uninstallErr)
	}
	sp.Stop()

	p.Check("project unregistered from global watch daemon")
	return nil
}

// sweepTranscripts runs a one-shot catch-up sweep for all watcher-capable
// runtimes. Best-effort: errors are logged to stderr, never returned.
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

// watcherRuntimes returns the names of watcher-capable runtimes from config.
func watcherRuntimes(cfg *config.Config) []string {
	var rts []string
	for _, rt := range cfg.EnabledHarnesses() {
		if rt == "claude" || rt == "windsurf" {
			rts = append(rts, rt)
		}
	}
	if len(rts) == 0 {
		return []string{"claude"}
	}
	return rts
}
