//go:build linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func systemdUserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func daemonServiceName(hash string) string {
	return "mom-watch-" + hash + ".service"
}

func sweepServiceName(hash string) string {
	return "mom-watch-sweep-" + hash + ".service"
}

func sweepTimerName(hash string) string {
	return "mom-watch-sweep-" + hash + ".timer"
}

// Install creates and enables the Layer 0 daemon service and Layer 1 sweep timer via systemd.
// A single daemon watches all enabled harnesses (config-driven).
func Install(cfg ServiceConfig) error {
	hash := ProjectHash(cfg.ProjectDir)
	unitDir, err := systemdUserDir()
	if err != nil {
		return fmt.Errorf("systemd user dir: %w", err)
	}

	logsDir := filepath.Join(cfg.MomDir, "logs")
	_ = os.MkdirAll(logsDir, 0755)

	// Layer 0: persistent daemon service — no --harness, reads config.
	daemonUnit := fmt.Sprintf(`[Unit]
Description=MOM watch daemon (%s)

[Service]
Type=simple
ExecStart=%s watch
WorkingDirectory=%s
Environment=MOM_PROJECT_DIR=%s
Restart=always
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, cfg.ProjectDir, cfg.MomBinary, cfg.ProjectDir, cfg.ProjectDir,
		filepath.Join(logsDir, "watch-daemon.log"),
		filepath.Join(logsDir, "watch-daemon.log"))

	if err := os.WriteFile(filepath.Join(unitDir, daemonServiceName(hash)), []byte(daemonUnit), 0644); err != nil {
		return fmt.Errorf("writing daemon service: %w", err)
	}

	// Layer 1: one-shot sweep service — no --harness, sweeps all.
	sweepUnit := fmt.Sprintf(`[Unit]
Description=MOM watch sweep (%s)

[Service]
Type=oneshot
ExecStart=%s watch --sweep
WorkingDirectory=%s
Environment=MOM_PROJECT_DIR=%s
StandardOutput=append:%s
StandardError=append:%s
`, cfg.ProjectDir, cfg.MomBinary, cfg.ProjectDir, cfg.ProjectDir,
		filepath.Join(logsDir, "watch-sweep.log"),
		filepath.Join(logsDir, "watch-sweep.log"))

	if err := os.WriteFile(filepath.Join(unitDir, sweepServiceName(hash)), []byte(sweepUnit), 0644); err != nil {
		return fmt.Errorf("writing sweep service: %w", err)
	}

	// Layer 1: timer that triggers sweep every 2 minutes.
	timerUnit := fmt.Sprintf(`[Unit]
Description=MOM watch sweep timer (%s)

[Timer]
OnBootSec=30
OnUnitActiveSec=120
Unit=%s

[Install]
WantedBy=timers.target
`, cfg.ProjectDir, sweepServiceName(hash))

	if err := os.WriteFile(filepath.Join(unitDir, sweepTimerName(hash)), []byte(timerUnit), 0644); err != nil {
		return fmt.Errorf("writing sweep timer: %w", err)
	}

	// Reload and enable.
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "enable", "--now", daemonServiceName(hash)).Run(); err != nil {
		return fmt.Errorf("enabling daemon: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "enable", "--now", sweepTimerName(hash)).Run(); err != nil {
		return fmt.Errorf("enabling sweep timer: %w", err)
	}

	return nil
}

// Uninstall stops and removes all systemd units for this project.
// Cleans up both current and legacy label formats.
func Uninstall(cfg ServiceConfig) error {
	hash := ProjectHash(cfg.ProjectDir)
	unitDir, err := systemdUserDir()
	if err != nil {
		return fmt.Errorf("systemd user dir: %w", err)
	}

	// Glob all unit files matching this project's hash (current + legacy formats).
	var units []string
	for _, pattern := range []string{
		// Current: single daemon/sweep per project.
		"mom-watch-" + hash + ".service",
		"mom-watch-sweep-" + hash + ".service",
		"mom-watch-sweep-" + hash + ".timer",
		// Legacy per-harness names.
		"mom-watch-*-" + hash + ".service",
		"mom-watch-sweep-*-" + hash + ".service",
		"mom-watch-sweep-*-" + hash + ".timer",
	} {
		matches, _ := filepath.Glob(filepath.Join(unitDir, pattern))
		for _, m := range matches {
			units = append(units, filepath.Base(m))
		}
	}

	var errs []string
	for _, unit := range units {
		_ = exec.Command("systemctl", "--user", "disable", "--now", unit).Run()
		path := filepath.Join(unitDir, unit)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("removing %s: %v", unit, err))
		}
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Status checks whether the daemon and sweep timer are active via systemd.
func Status(cfg ServiceConfig) (*Health, error) {
	hash := ProjectHash(cfg.ProjectDir)
	dName := daemonServiceName(hash)
	tName := sweepTimerName(hash)
	h := &Health{
		Platform: "systemd",
		Services: []ServiceHealth{{
			DaemonLabel: dName,
			TimerLabel:  tName,
		}},
	}

	if err := exec.Command("systemctl", "--user", "is-active", "--quiet", dName).Run(); err == nil {
		h.Services[0].DaemonRunning = true
	}
	if err := exec.Command("systemctl", "--user", "is-active", "--quiet", tName).Run(); err == nil {
		h.Services[0].TimerActive = true
	}

	return h, nil
}

// ── Global daemon ────────────────────────────────────────────────────────────

const (
	globalDaemonUnit = "mom-watch.service"
	globalSweepUnit  = "mom-watch-sweep.service"
	globalSweepTimer = "mom-watch-sweep.timer"
)

// GlobalServiceFiles returns the absolute paths of the platform-specific
// service files the global watch daemon installs. Doctor and other
// introspection callers use this to detect installation without
// duplicating platform literals.
func GlobalServiceFiles() ([]string, error) {
	unitDir, err := systemdUserDir()
	if err != nil {
		return nil, err
	}
	return []string{
		filepath.Join(unitDir, globalDaemonUnit),
		filepath.Join(unitDir, globalSweepUnit),
	}, nil
}

// InstallGlobal creates and enables a single global daemon and sweep timer via systemd.
// Before installing, removes ALL legacy per-project units.
func InstallGlobal(cfg GlobalServiceConfig) error {
	unitDir, err := systemdUserDir()
	if err != nil {
		return fmt.Errorf("systemd user dir: %w", err)
	}

	// One-time cleanup: remove all legacy per-project units.
	for _, pattern := range []string{
		"mom-watch-*.service",
		"mom-watch-sweep-*.service",
		"mom-watch-sweep-*.timer",
	} {
		matches, _ := filepath.Glob(filepath.Join(unitDir, pattern))
		for _, m := range matches {
			base := filepath.Base(m)
			if base == globalDaemonUnit || base == globalSweepUnit || base == globalSweepTimer {
				continue
			}
			_ = exec.Command("systemctl", "--user", "disable", "--now", base).Run()
			_ = os.Remove(m)
		}
	}

	logsDir, err := GlobalLogsDir()
	if err != nil {
		return fmt.Errorf("global logs dir: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home: %w", err)
	}

	// Global daemon service.
	daemonContent := fmt.Sprintf(`[Unit]
Description=MOM global watch daemon

[Service]
Type=simple
ExecStart=%s watch --global
WorkingDirectory=%s
Restart=always
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, cfg.MomBinary, home,
		filepath.Join(logsDir, "watch-daemon.log"),
		filepath.Join(logsDir, "watch-daemon.log"))

	if err := os.WriteFile(filepath.Join(unitDir, globalDaemonUnit), []byte(daemonContent), 0644); err != nil {
		return fmt.Errorf("writing global daemon service: %w", err)
	}

	// Global sweep service.
	sweepContent := fmt.Sprintf(`[Unit]
Description=MOM global watch sweep

[Service]
Type=oneshot
ExecStart=%s watch --sweep --global
WorkingDirectory=%s
StandardOutput=append:%s
StandardError=append:%s
`, cfg.MomBinary, home,
		filepath.Join(logsDir, "watch-sweep.log"),
		filepath.Join(logsDir, "watch-sweep.log"))

	if err := os.WriteFile(filepath.Join(unitDir, globalSweepUnit), []byte(sweepContent), 0644); err != nil {
		return fmt.Errorf("writing global sweep service: %w", err)
	}

	// Global sweep timer.
	timerContent := fmt.Sprintf(`[Unit]
Description=MOM global watch sweep timer

[Timer]
OnBootSec=30
OnUnitActiveSec=120
Unit=%s

[Install]
WantedBy=timers.target
`, globalSweepUnit)

	if err := os.WriteFile(filepath.Join(unitDir, globalSweepTimer), []byte(timerContent), 0644); err != nil {
		return fmt.Errorf("writing global sweep timer: %w", err)
	}

	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "enable", "--now", globalDaemonUnit).Run(); err != nil {
		return fmt.Errorf("enabling global daemon: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "enable", "--now", globalSweepTimer).Run(); err != nil {
		return fmt.Errorf("enabling global sweep timer: %w", err)
	}

	return nil
}

// UninstallGlobal stops and removes the global daemon and sweep units.
func UninstallGlobal() error {
	unitDir, err := systemdUserDir()
	if err != nil {
		return fmt.Errorf("systemd user dir: %w", err)
	}

	var errs []string
	for _, unit := range []string{globalSweepTimer, globalSweepUnit, globalDaemonUnit} {
		_ = exec.Command("systemctl", "--user", "disable", "--now", unit).Run()
		path := filepath.Join(unitDir, unit)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("removing %s: %v", unit, err))
		}
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// StatusGlobal checks the health of the global daemon and sweep timer.
func StatusGlobal() (*Health, error) {
	h := &Health{
		Platform: "systemd",
		Services: []ServiceHealth{{
			DaemonLabel: globalDaemonUnit,
			TimerLabel:  globalSweepTimer,
		}},
	}

	if err := exec.Command("systemctl", "--user", "is-active", "--quiet", globalDaemonUnit).Run(); err == nil {
		h.Services[0].DaemonRunning = true
	}
	if err := exec.Command("systemctl", "--user", "is-active", "--quiet", globalSweepTimer).Run(); err == nil {
		h.Services[0].TimerActive = true
	}

	return h, nil
}

// CleanupLegacy removes old per-project systemd units for a specific project.
func CleanupLegacy(projectDir string) error {
	hash := ProjectHash(projectDir)
	unitDir, err := systemdUserDir()
	if err != nil {
		return err
	}

	for _, pattern := range []string{
		"mom-watch-" + hash + ".service",
		"mom-watch-sweep-" + hash + ".service",
		"mom-watch-sweep-" + hash + ".timer",
		"mom-watch-*-" + hash + ".service",
		"mom-watch-sweep-*-" + hash + ".service",
		"mom-watch-sweep-*-" + hash + ".timer",
	} {
		matches, _ := filepath.Glob(filepath.Join(unitDir, pattern))
		for _, m := range matches {
			base := filepath.Base(m)
			_ = exec.Command("systemctl", "--user", "disable", "--now", base).Run()
			_ = os.Remove(m)
		}
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}
