package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/daemon"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
)

// CheckStatus is one of "pass", "fail", or "warn".
type CheckStatus string

const (
	StatusPass CheckStatus = "pass"
	StatusFail CheckStatus = "fail"
	StatusWarn CheckStatus = "warn"
)

// Check is one entry in the doctor health report.
type Check struct {
	Name       string
	Status     CheckStatus
	Detail     string
	NextAction string
}

// HealthReport is the structured output of BuildHealthReport — the
// canonical health view shared by the CLI and (eventually) Lens.
type HealthReport struct {
	Checks []Check
}

// HasFailures returns true if any check failed.
func (r HealthReport) HasFailures() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// BuildHealthReport runs every doctor check against the global install
// and returns a structured report. No network calls; only local files.
func BuildHealthReport() HealthReport {
	return HealthReport{
		Checks: []Check{
			checkCentralVault(),
			checkWatchDaemon(),
			checkHarnessMCP(),
			checkHarnessContext(),
			checkMomVersion(),
		},
	}
}

func checkCentralVault() Check {
	path, err := centralvault.Path()
	if err != nil {
		return Check{Name: "central vault", Status: StatusFail, Detail: err.Error(),
			NextAction: "run 'mom init'"}
	}
	if _, err := os.Stat(path); err != nil {
		return Check{Name: "central vault", Status: StatusFail, Detail: "DB file missing",
			NextAction: "run 'mom init' to create the central vault"}
	}
	v, err := centralvault.Open()
	if err != nil {
		return Check{Name: "central vault", Status: StatusFail,
			Detail:     "DB file present but unopenable (corrupt or unreadable)",
			NextAction: "restore from backup or 'mom export' from a healthy install"}
	}
	_ = v.Close()
	return Check{Name: "central vault", Status: StatusPass, Detail: path}
}

func checkWatchDaemon() Check {
	path, err := daemon.GlobalDaemonFile()
	if err != nil {
		return Check{Name: "watch daemon", Status: StatusFail, Detail: err.Error()}
	}
	if path == "" {
		return Check{Name: "watch daemon", Status: StatusFail,
			Detail:     "not supported on this platform",
			NextAction: "background recording requires macOS launchd or Linux systemd"}
	}
	if _, err := os.Stat(path); err != nil {
		return Check{Name: "watch daemon", Status: StatusFail,
			Detail:     "service file missing at " + path,
			NextAction: "run 'mom init' to install the global watch daemon"}
	}
	return Check{Name: "watch daemon", Status: StatusPass, Detail: path}
}

func checkHarnessMCP() Check {
	home, err := os.UserHomeDir()
	if err != nil {
		return Check{Name: "harness mcp", Status: StatusFail, Detail: err.Error()}
	}
	path := filepath.Join(home, ".claude.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: "harness mcp", Status: StatusFail,
			Detail: ".claude.json missing", NextAction: "run 'mom init --harnesses claude'"}
	}
	var root struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return Check{Name: "harness mcp", Status: StatusFail,
			Detail: ".claude.json unparseable", NextAction: "run 'mom init --harnesses claude'"}
	}
	if _, ok := root.MCPServers["mom"]; !ok {
		return Check{Name: "harness mcp", Status: StatusFail,
			Detail: "mom MCP server entry missing", NextAction: "run 'mom init --harnesses claude'"}
	}
	return Check{Name: "harness mcp", Status: StatusPass, Detail: "claude wired"}
}

func checkHarnessContext() Check {
	home, err := os.UserHomeDir()
	if err != nil {
		return Check{Name: "harness context", Status: StatusFail, Detail: err.Error()}
	}
	path := filepath.Join(home, ".claude", "CLAUDE.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: "harness context", Status: StatusFail,
			Detail: "CLAUDE.md missing", NextAction: "run 'mom init --harnesses claude'"}
	}
	if !strings.Contains(string(data), "BEGIN MOM GENERATED BLOCK") {
		return Check{Name: "harness context", Status: StatusFail,
			Detail: "MOM block missing from CLAUDE.md", NextAction: "run 'mom init --harnesses claude'"}
	}
	return Check{Name: "harness context", Status: StatusPass, Detail: "claude block present"}
}

func checkMomVersion() Check {
	return Check{Name: "mom version", Status: StatusPass,
		Detail: fmt.Sprintf("%s (%s)", Version, Commit)}
}

// ─── command wiring ──────────────────────────────────────────────────────────

func init() {
	doctorCmd.Flags().Bool("bundle", false, "Print a redacted diagnostic bundle to stdout")
	doctorCmd.Long = `Check the global MOM install for health issues.

Runs only against local files; no network calls. Use --bundle to print a
redacted diagnostic blob suitable for pasting into a bug report.`
}

// runDoctor is the cobra entry point for ` + "`" + `mom doctor` + "`" + `.
func runDoctor(cmd *cobra.Command, args []string) error {
	bundle, _ := cmd.Flags().GetBool("bundle")
	report := BuildHealthReport()
	if bundle {
		renderBundle(cmd, report)
	} else {
		renderHuman(cmd, report)
	}
	if report.HasFailures() {
		return fmt.Errorf("one or more doctor checks failed")
	}
	return nil
}

func renderHuman(cmd *cobra.Command, r HealthReport) {
	p := ux.NewPrinter(cmd.OutOrStdout())
	for _, c := range r.Checks {
		line := c.Name
		if c.Detail != "" {
			line += ": " + c.Detail
		}
		switch c.Status {
		case StatusPass:
			p.Check(line)
		case StatusWarn:
			p.Warn(line)
		case StatusFail:
			p.Fail(line)
			if c.NextAction != "" {
				p.Muted("  → " + c.NextAction)
			}
		}
	}
}

// truncate shortens s to at most n runes (shared helper, used elsewhere in
// the cmd package).
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
}

func renderBundle(cmd *cobra.Command, r HealthReport) {
	fmt.Fprintln(cmd.OutOrStdout(), "=== MOM DOCTOR BUNDLE ===")
	for _, c := range r.Checks {
		fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s\n", c.Status, c.Name, c.Detail)
		if c.NextAction != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  next: %s\n", c.NextAction)
		}
	}
	fmt.Fprintln(cmd.OutOrStdout(), "=== END BUNDLE ===")
}
