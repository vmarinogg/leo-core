package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/momhq/mom/ingress/mcp"
	"github.com/momhq/mom/shared/ux"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start a server (use --mcp for MCP stdio mode)",
}

var serveMCPCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start the MCP stdio server",
	Long: `Start the MOM MCP (Model Context Protocol) server on stdio.

Any MCP-aware harness (Claude Code, Cursor, Cline, …) can connect by adding
this command to its MCP config:

  {
    "mcpServers": {
      "mom": {
        "command": "mom",
        "args": ["serve", "mcp"]
      }
    }
  }

stdout is reserved for JSON-RPC — all human-readable output goes to stderr.
Block until stdin is closed or SIGINT.`,
	RunE: runServeMCP,
}

var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show MCP server activity from the log",
	RunE:  runServerStatus,
}

func init() {
	serverStatusCmd.Flags().Int("lines", 20, "Number of recent log lines to show")
	serverStatusCmd.Flags().BoolP("follow", "f", false, "Follow the log in real-time (like tail -f)")
	serveCmd.AddCommand(serveMCPCmd)
	serveCmd.AddCommand(serverStatusCmd)
}

func runServeMCP(_ *cobra.Command, _ []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// Allow harnesses that do not set cwd (e.g. Pi launches MCP children from
	// a different working dir) to specify the project directory via env var.
	if envDir := os.Getenv("MOM_PROJECT_DIR"); envDir != "" {
		cwd = envDir
	}

	projectDir, momDir, err := resolveMomContext(cwd)
	if err != nil {
		return err
	}

	// Layer 2: one-shot sweep catches unprocessed transcripts.
	// Covers the scenario where the daemon is not installed yet.
	sweepTranscripts(projectDir, momDir)

	mcp.Version = Version
	srv := mcp.New(momDir)

	// Wire central-vault workers (Drafter + Logbook) onto the MCP
	// server's bus so mom_record events are persisted, and so any
	// turn.observed events the MCP process generates (none today,
	// but future hooks will) are recorded. Same workers as the
	// watcher — opened against $HOME/.mom/mom.db with WAL-safe
	// concurrent access. AttachToBus encapsulates the topic set
	// (Drafter on write events; Logbook on turn.observed +
	// op.memory.* events).
	openCentralWorkers().AttachToBus(srv.Bus())

	// Blocks until stdin is closed.
	srv.Serve(os.Stdin, os.Stdout)
	return nil
}

func runServerStatus(cmd *cobra.Command, _ []string) error {
	lines, _ := cmd.Flags().GetInt("lines")
	follow, _ := cmd.Flags().GetBool("follow")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	_, momDir, err := resolveMomContext(cwd)
	if err != nil {
		return err
	}

	logPath := filepath.Join(momDir, "logs", "mcp-server.log")
	p := ux.NewPrinter(cmd.OutOrStdout())

	if follow {
		return runServerStatusFollow(p, logPath)
	}

	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			p.Muted("No MCP server log found. The server has not been run yet.")
			return nil
		}
		return fmt.Errorf("opening log: %w", err)
	}
	defer f.Close()

	type logLine struct {
		ts     time.Time
		status string
		method string
		detail string
	}

	var allLines []logLine
	scanner := bufio.NewScanner(f)
	var errorCount int

	for scanner.Scan() {
		text := scanner.Text()
		parts := strings.Fields(text)
		if len(parts) < 3 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, parts[0])
		if err != nil {
			continue
		}
		status := parts[1]
		method := parts[2]
		detail := ""
		if len(parts) > 3 {
			detail = strings.Join(parts[3:], " ")
		}
		allLines = append(allLines, logLine{ts: ts, status: status, method: method, detail: detail})
		if strings.EqualFold(status, "error") {
			errorCount++
		}
	}

	if len(allLines) == 0 {
		p.Muted("Log file is empty.")
		return nil
	}

	p.Diamond("MCP server status")
	p.Blank()

	// Last activity timestamp.
	lastActivity := allLines[len(allLines)-1].ts
	p.KeyValue("Last activity", lastActivity.Format(time.RFC3339), 16)
	p.KeyValue("Error count", strconv.Itoa(errorCount), 16)
	p.KeyValue("Total entries", strconv.Itoa(len(allLines)), 16)
	p.Blank()

	// Method call counts sorted by count desc.
	type mc struct {
		method string
		count  int
	}
	mcMap := make(map[string]int)
	for _, l := range allLines {
		mcMap[l.method]++
	}
	var mcs []mc
	for m, c := range mcMap {
		mcs = append(mcs, mc{m, c})
	}
	sort.Slice(mcs, func(i, j int) bool {
		if mcs[i].count != mcs[j].count {
			return mcs[i].count > mcs[j].count
		}
		return mcs[i].method < mcs[j].method
	})
	p.Bold("Method counts")
	for _, mc := range mcs {
		p.Chevron(fmt.Sprintf("%-30s %s", mc.method, p.HighlightValue(strconv.Itoa(mc.count))))
	}
	p.Blank()

	// Print last N lines.
	recent := allLines
	if len(recent) > lines {
		recent = recent[len(recent)-lines:]
	}
	p.Bold(fmt.Sprintf("Recent %d entries", len(recent)))
	for _, l := range recent {
		method := p.HighlightValue(l.method)
		if strings.EqualFold(l.status, "error") {
			if l.detail != "" {
				p.Failf("%s  %s  %s", l.ts.Format("15:04:05"), method, l.detail)
			} else {
				p.Failf("%s  %s", l.ts.Format("15:04:05"), method)
			}
		} else {
			if l.detail != "" {
				p.Checkf("%s  %s  %s", l.ts.Format("15:04:05"), method, l.detail)
			} else {
				p.Checkf("%s  %s", l.ts.Format("15:04:05"), method)
			}
		}
	}

	return nil
}

// runServerStatusFollow tails the MCP log file in real-time.
func runServerStatusFollow(p *ux.Printer, logPath string) error {
	p.Diamond("MCP server — live")
	p.Chevron(logPath)
	p.Muted("press Ctrl-C to stop")
	p.Blank()

	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			p.Muted("No MCP server log found. Waiting for activity...")
			// Wait for file to appear.
			for {
				time.Sleep(500 * time.Millisecond)
				f, err = os.Open(logPath)
				if err == nil {
					break
				}
			}
		} else {
			return fmt.Errorf("opening log: %w", err)
		}
	}
	defer f.Close()

	// Seek to end — only show new activity.
	_, _ = f.Seek(0, io.SeekEnd)

	buf := make([]byte, 4096)
	var partial string
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := partial + string(buf[:n])
			lines := strings.Split(chunk, "\n")
			// Last element may be partial — save it.
			partial = lines[len(lines)-1]
			for _, line := range lines[:len(lines)-1] {
				if line == "" {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) < 3 {
					continue
				}
				status := parts[1]
				method := parts[2]
				detail := ""
				if len(parts) > 3 {
					detail = strings.Join(parts[3:], " ")
				}
				ts, _ := time.Parse(time.RFC3339, parts[0])
				timeStr := ts.Format("15:04:05")
				methodStr := p.HighlightValue(method)
				if strings.EqualFold(status, "error") {
					if detail != "" {
						p.Failf("%s  %s  %s", timeStr, methodStr, detail)
					} else {
						p.Failf("%s  %s", timeStr, methodStr)
					}
				} else {
					if detail != "" {
						p.Checkf("%s  %s  %s", timeStr, methodStr, detail)
					} else {
						p.Checkf("%s  %s", timeStr, methodStr)
					}
				}
			}
		}
		if err == io.EOF {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if err != nil {
			return fmt.Errorf("reading log: %w", err)
		}
	}
}
