package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/spf13/cobra"
)

func writeLegacyLog(t *testing.T, momDir, name, body string) {
	t.Helper()
	logsDir := filepath.Join(momDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, name), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRunCentralImport_DryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	vaultPath := filepath.Join(home, "central.db")
	t.Setenv("HOME", home)
	t.Setenv("MOM_UPGRADE_SCAN_ROOT", home)
	t.Setenv("MOM_VAULT", vaultPath)
	legacy := filepath.Join(home, "repo", ".mom")
	writeLegacyMemory(t, legacy, "m", `{"id":"m","content":{"text":"memory"}}`)
	writeLegacyLog(t, legacy, "session-s-1.json", `{"session_id":"s-1","started":"2026-04-10T14:39:52Z","ended":"2026-04-10T14:40:00Z","interactions":1}`)
	cmd := &cobra.Command{}
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := runCentralImport(cmd, true); err != nil {
		t.Fatalf("runCentralImport dry-run: %v", err)
	}
	if _, err := os.Stat(vaultPath); !os.IsNotExist(err) {
		t.Fatalf("vault exists after dry-run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "1 memories, 1 log events") {
		t.Fatalf("dry-run output = %q", out)
	}
}

func TestUpgradeCmd_OnlyDryRunFlag(t *testing.T) {
	if upgradeCmd.Flags().Lookup("dry-run") == nil {
		t.Fatal("upgrade --dry-run flag missing")
	}
	for _, name := range []string{"all", "skip-memories", "skip-logs"} {
		if upgradeCmd.Flags().Lookup(name) != nil {
			t.Fatalf("upgrade flag %q should not exist", name)
		}
	}
}

func TestDiscoverLegacyVaultsForImport_SkipsSymlinkedLogs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_UPGRADE_SCAN_ROOT", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, "central.db"))
	legacy := filepath.Join(home, "repo", ".mom")
	writeLegacyLog(t, legacy, "session-real.json", `{"session_id":"real","started":"2026-04-10T14:39:52Z","ended":"2026-04-10T14:40:00Z","interactions":1}`)
	outside := filepath.Join(home, "outside.json")
	if err := os.WriteFile(outside, []byte(`{"session_id":"secret","started":"2026-04-10T14:39:52Z","ended":"2026-04-10T14:40:00Z","interactions":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(legacy, "logs", "session-secret.json")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	plans, err := discoverLegacyVaultsForImport()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(plans) != 1 || len(plans[0].Logs) != 1 || plans[0].Logs[0].Event.SessionID != "real" {
		t.Fatalf("plans = %+v", plans)
	}
}

func TestDiscoverLegacyVaultsForImport_FailsMalformedImportableLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_UPGRADE_SCAN_ROOT", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, "central.db"))
	legacy := filepath.Join(home, "repo", ".mom")
	writeLegacyLog(t, legacy, "2026-04-29.jsonl", `{"kind":"ConsumptionEvent","ts":"2026-04-29T22:32:22Z"}
not-json
`)

	_, err := discoverLegacyVaultsForImport()
	if err == nil {
		t.Fatal("discoverLegacyVaultsForImport succeeded, want malformed log error")
	}
}

func TestExecuteCentralImport_ImportsLegacyJSONLWithAllowlistedPayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_UPGRADE_SCAN_ROOT", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, "central.db"))
	legacy := filepath.Join(home, "repo", ".mom")
	writeLegacyLog(t, legacy, "2026-04-29.jsonl", `{"kind":"ConsumptionEvent","memory_id":"mem-1","session_id":null,"ts":"2026-04-29T22:32:22Z","by_agent":"mcp","context":"mom_recall","secret":"AKIA-SHOULD-NOT-PERSIST"}
{"kind":"RuntimeHealth","runtime":"claude","ts":"2026-04-29T22:35:00Z","wrap_up_success":true,"error_type":null,"latency_ms":42,"command":"cat secrets.env"}
`)

	plans, err := discoverLegacyVaultsForImport()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	summary, err := executeCentralImport(plans)
	if err != nil {
		t.Fatalf("executeCentralImport: %v", err)
	}
	if summary.LogEvents != 2 {
		t.Fatalf("LogEvents = %d, want 2", summary.LogEvents)
	}

	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		t.Fatalf("OpenLibrarian: %v", err)
	}
	defer func() { _ = closeFn() }()
	rows, err := lib.QueryOpEvents(librarian.OpEventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("QueryOpEvents: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		b, _ := json.Marshal(row.Payload)
		if strings.Contains(string(b), "AKIA") || strings.Contains(string(b), "secrets.env") {
			t.Fatalf("payload leaked non-allowlisted data: %s", b)
		}
		if !strings.HasPrefix(row.SessionID, "legacy:") {
			t.Fatalf("SessionID = %q, want legacy pseudo-session", row.SessionID)
		}
	}
}

func TestExecuteCentralImport_ImportsLegacySessionSummaryLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_UPGRADE_SCAN_ROOT", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, "central.db"))
	legacy := filepath.Join(home, "repo", ".mom")
	writeLegacyLog(t, legacy, "session-s-1.json", `{
		"session_id":"s-1",
		"started":"2026-04-10T14:39:52.745Z",
		"ended":"2026-04-10T15:10:41.294Z",
		"interactions":34,
		"files_changed":2,
		"memories_created":1,
		"tool_calls":{
			"codebase_read":{"total":10,"detail":{"Read":10}},
			"system":{"total":3,"detail":{"Bash":3}},
			"raw_tool_name":{"total":99,"detail":{"SecretTool":99}}
		},
		"model":"claude-sonnet",
		"provider":"anthropic",
		"usage":{"total_tokens":1290,"cost_usd":0.0185,"raw":"SECRET-USAGE"}
	}`)

	plans, err := discoverLegacyVaultsForImport()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	summary, err := executeCentralImport(plans)
	if err != nil {
		t.Fatalf("executeCentralImport: %v", err)
	}
	if summary.LogEvents != 1 {
		t.Fatalf("LogEvents = %d, want 1", summary.LogEvents)
	}
	if summary.LogAudit == "" || !strings.Contains(summary.LogAudit, "log-import") {
		t.Fatalf("LogAudit = %q", summary.LogAudit)
	}

	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		t.Fatalf("OpenLibrarian: %v", err)
	}
	defer func() { _ = closeFn() }()
	rows, err := lib.QueryOpEvents(librarian.OpEventFilter{EventType: "legacy.session.summary", SessionID: "s-1", Limit: 10})
	if err != nil {
		t.Fatalf("QueryOpEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	wantTime, _ := time.Parse(time.RFC3339Nano, "2026-04-10T15:10:41.294Z")
	if !rows[0].CreatedAt.Equal(wantTime) {
		t.Fatalf("CreatedAt = %s, want %s", rows[0].CreatedAt, wantTime)
	}
	if rows[0].Payload["legacy_format"] != "session_summary" || rows[0].Payload["interactions"] != float64(34) {
		t.Fatalf("payload = %#v", rows[0].Payload)
	}
	cats, ok := rows[0].Payload["tool_categories"].(map[string]any)
	if !ok || cats["codebase_read"] != float64(10) || cats["system"] != float64(3) {
		b, _ := json.Marshal(rows[0].Payload)
		t.Fatalf("tool_categories not category totals only: %s", b)
	}
	payloadJSON, _ := json.Marshal(rows[0].Payload)
	if strings.Contains(string(payloadJSON), "raw_tool_name") || strings.Contains(string(payloadJSON), "SecretTool") || strings.Contains(string(payloadJSON), "SECRET-USAGE") {
		t.Fatalf("session summary payload leaked non-category details: %s", payloadJSON)
	}

	again, err := executeCentralImport(plans)
	if err != nil {
		t.Fatalf("second executeCentralImport: %v", err)
	}
	if again.LogSkipped != 1 || again.LogEvents != 0 {
		t.Fatalf("second summary = %+v", again)
	}
	writeLegacyLog(t, legacy, "session-s-2.json", `{"session_id":"s-2","started":"2026-04-10T16:00:00Z","ended":"2026-04-10T16:01:00Z","interactions":1}`)
	changed, err := discoverLegacyVaultsForImport()
	if err != nil {
		t.Fatalf("rediscover: %v", err)
	}
	if _, err := executeCentralImport(changed); err == nil || !strings.Contains(err.Error(), "different fingerprint") {
		t.Fatalf("changed fingerprint err = %v, want different fingerprint", err)
	}
}
