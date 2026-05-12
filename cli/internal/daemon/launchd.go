//go:build darwin

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Binary}}</string>
{{- range .Args}}
		<string>{{.}}</string>
{{- end}}
	</array>
	<key>WorkingDirectory</key>
	<string>{{.WorkingDir}}</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>MOM_PROJECT_DIR</key>
		<string>{{.ProjectDir}}</string>
	</dict>
{{- if .KeepAlive}}
	<key>KeepAlive</key>
	<true/>
{{- end}}
{{- if .StartInterval}}
	<key>StartInterval</key>
	<integer>{{.StartInterval}}</integer>
{{- end}}
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogFile}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogFile}}</string>
</dict>
</plist>
`

type plistData struct {
	Label         string
	Binary        string
	Args          []string
	WorkingDir    string
	ProjectDir    string
	KeepAlive     bool
	StartInterval int
	LogFile       string
}

func launchAgentsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func daemonLabel(hash string) string {
	return "com.momhq.watch-" + hash
}

func sweepLabel(hash string) string {
	return "com.momhq.watch-sweep-" + hash
}

// Install creates and loads the Layer 0 daemon and Layer 1 sweep timer via launchd.
// A single daemon watches all enabled harnesses (config-driven).
func Install(cfg ServiceConfig) error {
	hash := ProjectHash(cfg.ProjectDir)
	agentsDir, err := launchAgentsDir()
	if err != nil {
		return fmt.Errorf("launch agents dir: %w", err)
	}

	logsDir := filepath.Join(cfg.MomDir, "logs")
	_ = os.MkdirAll(logsDir, 0755)

	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return fmt.Errorf("parsing plist template: %w", err)
	}

	// Layer 0: persistent daemon — no --harness flag, reads config for all harnesses.
	dLabel := daemonLabel(hash)
	dPath := filepath.Join(agentsDir, dLabel+".plist")
	dData := plistData{
		Label:      dLabel,
		Binary:     cfg.MomBinary,
		Args:       []string{"watch"},
		WorkingDir: cfg.ProjectDir,
		ProjectDir: cfg.ProjectDir,
		KeepAlive:  true,
		LogFile:    filepath.Join(logsDir, "watch-daemon.log"),
	}

	// Unload existing before overwriting (ignore errors — may not exist).
	if _, err := os.Stat(dPath); err == nil {
		_ = exec.Command("launchctl", "unload", dPath).Run()
	}

	if err := writePlist(tmpl, dPath, dData); err != nil {
		return fmt.Errorf("writing daemon plist: %w", err)
	}
	if err := exec.Command("launchctl", "load", "-w", dPath).Run(); err != nil {
		return fmt.Errorf("loading daemon: %w", err)
	}

	// Layer 1: periodic sweep timer — no --harness flag, sweeps all harnesses.
	sLabel := sweepLabel(hash)
	sPath := filepath.Join(agentsDir, sLabel+".plist")
	sData := plistData{
		Label:         sLabel,
		Binary:        cfg.MomBinary,
		Args:          []string{"watch", "--sweep"},
		WorkingDir:    cfg.ProjectDir,
		ProjectDir:    cfg.ProjectDir,
		StartInterval: 120,
		LogFile:       filepath.Join(logsDir, "watch-sweep.log"),
	}

	if _, err := os.Stat(sPath); err == nil {
		_ = exec.Command("launchctl", "unload", sPath).Run()
	}

	if err := writePlist(tmpl, sPath, sData); err != nil {
		return fmt.Errorf("writing sweep plist: %w", err)
	}
	if err := exec.Command("launchctl", "load", "-w", sPath).Run(); err != nil {
		return fmt.Errorf("loading sweep timer: %w", err)
	}

	return nil
}

// Uninstall stops and removes all launchd agents for this project.
// Cleans up both current and legacy label formats.
func Uninstall(cfg ServiceConfig) error {
	hash := ProjectHash(cfg.ProjectDir)
	agentsDir, err := launchAgentsDir()
	if err != nil {
		return fmt.Errorf("launch agents dir: %w", err)
	}

	// Glob all plist files matching this project's hash (current + legacy formats).
	var matches []string
	for _, pattern := range []string{
		// Current: com.momhq.watch-{hash}.plist, com.momhq.watch-sweep-{hash}.plist
		"com.momhq.watch-" + hash + ".plist",
		"com.momhq.watch-sweep-" + hash + ".plist",
		// Legacy per-harness: com.momhq.watch-{harness}-{hash}.plist
		"com.momhq.watch-*-" + hash + ".plist",
	} {
		found, _ := filepath.Glob(filepath.Join(agentsDir, pattern))
		matches = append(matches, found...)
	}

	var errs []string
	for _, path := range matches {
		_ = exec.Command("launchctl", "unload", path).Run()
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("removing %s: %v", filepath.Base(path), err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Status checks whether the daemon and sweep timer are loaded in launchd.
func Status(cfg ServiceConfig) (*Health, error) {
	hash := ProjectHash(cfg.ProjectDir)
	dLabel := daemonLabel(hash)
	sLabel := sweepLabel(hash)
	h := &Health{
		Platform: "launchd",
		Services: []ServiceHealth{{
			DaemonLabel: dLabel,
			TimerLabel:  sLabel,
		}},
	}

	// launchctl list <label> exits 0 if the job is loaded.
	if err := exec.Command("launchctl", "list", dLabel).Run(); err == nil {
		h.Services[0].DaemonRunning = true
	}
	if err := exec.Command("launchctl", "list", sLabel).Run(); err == nil {
		h.Services[0].TimerActive = true
	}

	return h, nil
}

func writePlist(tmpl *template.Template, path string, data plistData) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, data)
}

// ── Global daemon ────────────────────────────────────────────────────────────

const globalDaemonLabel = "com.momhq.watch"
const globalSweepLabel = "com.momhq.watch-sweep"

// GlobalServiceFiles returns the absolute paths of the platform-specific
// service files the global watch daemon installs. Doctor and other
// introspection callers use this to detect installation without
// duplicating platform literals.
func GlobalServiceFiles() ([]string, error) {
	agentsDir, err := launchAgentsDir()
	if err != nil {
		return nil, err
	}
	return []string{
		filepath.Join(agentsDir, globalDaemonLabel+".plist"),
		filepath.Join(agentsDir, globalSweepLabel+".plist"),
	}, nil
}

const globalPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Binary}}</string>
{{- range .Args}}
		<string>{{.}}</string>
{{- end}}
	</array>
	<key>WorkingDirectory</key>
	<string>{{.WorkingDir}}</string>
{{- if .KeepAlive}}
	<key>KeepAlive</key>
	<true/>
{{- end}}
{{- if .StartInterval}}
	<key>StartInterval</key>
	<integer>{{.StartInterval}}</integer>
{{- end}}
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogFile}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogFile}}</string>
</dict>
</plist>
`

// InstallGlobal creates and loads a single global daemon and sweep timer via launchd.
// Before installing, removes ALL legacy per-project agents.
func InstallGlobal(cfg GlobalServiceConfig) error {
	agentsDir, err := launchAgentsDir()
	if err != nil {
		return fmt.Errorf("launch agents dir: %w", err)
	}

	// One-time cleanup: remove ALL legacy per-project agents.
	legacyPatterns := []string{
		"com.momhq.watch-*.plist",
	}
	for _, pattern := range legacyPatterns {
		matches, _ := filepath.Glob(filepath.Join(agentsDir, pattern))
		for _, m := range matches {
			base := filepath.Base(m)
			// Skip the global agents themselves.
			if base == globalDaemonLabel+".plist" || base == globalSweepLabel+".plist" {
				continue
			}
			_ = exec.Command("launchctl", "unload", m).Run()
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

	tmpl, err := template.New("global-plist").Parse(globalPlistTemplate)
	if err != nil {
		return fmt.Errorf("parsing global plist template: %w", err)
	}

	// Global daemon: mom watch --global
	dPath := filepath.Join(agentsDir, globalDaemonLabel+".plist")
	dData := plistData{
		Label:      globalDaemonLabel,
		Binary:     cfg.MomBinary,
		Args:       []string{"watch", "--global"},
		WorkingDir: home,
		KeepAlive:  true,
		LogFile:    filepath.Join(logsDir, "watch-daemon.log"),
	}

	if _, err := os.Stat(dPath); err == nil {
		_ = exec.Command("launchctl", "unload", dPath).Run()
	}
	if err := writeGlobalPlist(tmpl, dPath, dData); err != nil {
		return fmt.Errorf("writing global daemon plist: %w", err)
	}
	if err := exec.Command("launchctl", "load", "-w", dPath).Run(); err != nil {
		return fmt.Errorf("loading global daemon: %w", err)
	}

	// Global sweep: mom watch --sweep --global
	sPath := filepath.Join(agentsDir, globalSweepLabel+".plist")
	sData := plistData{
		Label:         globalSweepLabel,
		Binary:        cfg.MomBinary,
		Args:          []string{"watch", "--sweep", "--global"},
		WorkingDir:    home,
		StartInterval: 120,
		LogFile:       filepath.Join(logsDir, "watch-sweep.log"),
	}

	if _, err := os.Stat(sPath); err == nil {
		_ = exec.Command("launchctl", "unload", sPath).Run()
	}
	if err := writeGlobalPlist(tmpl, sPath, sData); err != nil {
		return fmt.Errorf("writing global sweep plist: %w", err)
	}
	if err := exec.Command("launchctl", "load", "-w", sPath).Run(); err != nil {
		return fmt.Errorf("loading global sweep timer: %w", err)
	}

	return nil
}

// UninstallGlobal stops and removes the global daemon and sweep timer.
func UninstallGlobal() error {
	agentsDir, err := launchAgentsDir()
	if err != nil {
		return fmt.Errorf("launch agents dir: %w", err)
	}

	var errs []string
	for _, label := range []string{globalDaemonLabel, globalSweepLabel} {
		path := filepath.Join(agentsDir, label+".plist")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		_ = exec.Command("launchctl", "unload", path).Run()
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("removing %s: %v", label, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// StatusGlobal checks the health of the global daemon and sweep timer.
func StatusGlobal() (*Health, error) {
	h := &Health{
		Platform: "launchd",
		Services: []ServiceHealth{{
			DaemonLabel: globalDaemonLabel,
			TimerLabel:  globalSweepLabel,
		}},
	}

	if err := exec.Command("launchctl", "list", globalDaemonLabel).Run(); err == nil {
		h.Services[0].DaemonRunning = true
	}
	if err := exec.Command("launchctl", "list", globalSweepLabel).Run(); err == nil {
		h.Services[0].TimerActive = true
	}

	return h, nil
}

// CleanupLegacy removes old per-project launchd agents for a specific project.
func CleanupLegacy(projectDir string) error {
	hash := ProjectHash(projectDir)
	agentsDir, err := launchAgentsDir()
	if err != nil {
		return err
	}

	for _, pattern := range []string{
		"com.momhq.watch-" + hash + ".plist",
		"com.momhq.watch-sweep-" + hash + ".plist",
		"com.momhq.watch-*-" + hash + ".plist",
	} {
		matches, _ := filepath.Glob(filepath.Join(agentsDir, pattern))
		for _, m := range matches {
			_ = exec.Command("launchctl", "unload", m).Run()
			_ = os.Remove(m)
		}
	}
	return nil
}

func writeGlobalPlist(tmpl *template.Template, path string, data plistData) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, data)
}
