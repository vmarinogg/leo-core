package cmd

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
)

type legacyLogEvent struct {
	SourceItemID string
	Raw          []byte
	Event        librarian.OpEvent
	Hash         string
}

var legacyJSONLPayloadKeys = []string{
	"kind",
	"memory_id",
	"runtime",
	"repo_id",
	"trigger",
	"turn_count",
	"tool_call_count",
	"wrap_up_success",
	"error_type",
	"latency_ms",
	"by_agent",
	"context",
}

var legacyUsageKeys = []string{
	"input_tokens",
	"output_tokens",
	"cache_read_tokens",
	"cache_write_tokens",
	"total_tokens",
	"cost_usd",
	"stop_reasons",
}

var legacyToolCategoryNames = map[string]bool{
	"mom_memory":     true,
	"mom_cli":        true,
	"codebase_read":  true,
	"codebase_write": true,
	"system":         true,
}

func runCentralImport(cmd *cobra.Command, dryRun bool) error {
	plans, err := discoverLegacyVaultsForImport()
	if err != nil {
		return err
	}
	p := ux.NewPrinter(cmd.OutOrStdout())
	if len(plans) == 0 {
		p.Muted("No legacy .mom folders found for central import.")
		return nil
	}
	memoryCount, logCount := 0, 0
	for _, plan := range plans {
		memoryCount += len(plan.Docs)
		logCount += len(plan.Logs)
	}
	p.Blank()
	if dryRun {
		p.Bold("Dry run — central import plan:")
	} else {
		p.Bold("Central import plan:")
	}
	for _, plan := range plans {
		p.KeyValue(shortenPath(plan.Path), fmt.Sprintf("%d memories, %d log events", len(plan.Docs), len(plan.Logs)), 28)
	}
	p.KeyValue("Total", fmt.Sprintf("%d vaults, %d memories, %d log events", len(plans), memoryCount, logCount), 28)
	p.Blank()
	if dryRun {
		return nil
	}
	if !confirmUpgradeImport(cmd, len(plans), memoryCount, logCount) {
		p.Muted("Central import skipped.")
		return nil
	}
	summary, err := executeCentralImport(plans)
	if err != nil {
		return err
	}
	p.Bold("Central import complete:")
	p.Checkf("memories imported: %d", summary.Memories)
	p.Checkf("log events imported: %d", summary.LogEvents)
	if summary.Skipped > 0 {
		p.Warnf("memory sources skipped (already imported): %d", summary.Skipped)
	}
	if summary.LogSkipped > 0 {
		p.Warnf("log sources skipped (already imported): %d", summary.LogSkipped)
	}
	if summary.Audit != "" {
		p.Checkf("memory ID mapping written: %s", summary.Audit)
	}
	if summary.LogAudit != "" {
		p.Checkf("log import mapping written: %s", summary.LogAudit)
	}
	return nil
}

func executeCentralImport(plans []legacyVaultPlan) (importSummary, error) {
	summary, memErr := executeCentralMemoryImport(plans)
	logSummary, logErr := executeCentralLogImport(plans)
	summary.LogEvents = logSummary.LogEvents
	summary.LogMappings = logSummary.LogMappings
	summary.LogSkipped = logSummary.LogSkipped
	summary.LogAudit = logSummary.LogAudit
	if memErr != nil {
		return summary, memErr
	}
	if logErr != nil {
		return summary, logErr
	}
	return summary, nil
}

func executeCentralLogImport(plans []legacyVaultPlan) (importSummary, error) {
	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		return importSummary{}, fmt.Errorf("open central vault: %w", err)
	}
	defer func() { _ = closeFn() }()
	var summary importSummary
	for _, plan := range plans {
		if len(plan.Logs) == 0 {
			continue
		}
		records := make([]librarian.LegacyLogImportEvent, 0, len(plan.Logs))
		for _, e := range plan.Logs {
			records = append(records, librarian.LegacyLogImportEvent{SourceItemID: e.SourceItemID, Event: e.Event, Hash: e.Hash})
		}
		mappings, skipped, err := lib.ImportLegacyLogEvents(filepath.Join(plan.Path, "logs"), plan.LogFingerprint, records)
		if err != nil {
			return summary, err
		}
		if skipped {
			summary.LogSkipped++
			continue
		}
		summary.LogEvents += len(records)
		summary.LogMappings = append(summary.LogMappings, mappings...)
	}
	if len(summary.LogMappings) > 0 {
		audit, err := writeLogImportAudit(summary.LogMappings)
		if err != nil {
			return importSummary{}, err
		}
		summary.LogAudit = audit
	}
	return summary, nil
}

func readLegacyLogEvents(momDir string) ([]legacyLogEvent, string, error) {
	logsDir := filepath.Join(momDir, "logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return nil, "", err
	}
	var events []legacyLogEvent
	var parts []string
	for _, e := range entries {
		path, info, ok := legacyLogFilePath(logsDir, e)
		if !ok {
			continue
		}
		if strings.HasPrefix(e.Name(), "session-") && filepath.Ext(e.Name()) == ".json" {
			ev, ok, err := parseLegacySessionSummary(path, e.Name(), info.ModTime())
			if err != nil {
				return nil, "", err
			}
			if ok {
				events = append(events, ev)
				parts = append(parts, ev.SourceItemID+":"+ev.Hash)
			}
			continue
		}
		if filepath.Ext(e.Name()) == ".jsonl" {
			parsed, err := parseLegacyJSONLLog(path, e.Name(), info.ModTime(), legacySourceKey(momDir))
			if err != nil {
				return nil, "", err
			}
			for _, ev := range parsed {
				events = append(events, ev)
				parts = append(parts, ev.SourceItemID+":"+ev.Hash)
			}
		}
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return events, fmt.Sprintf("%x", sum[:]), nil
}

func legacyLogFilePath(logsDir string, e fs.DirEntry) (string, fs.FileInfo, bool) {
	ext := filepath.Ext(e.Name())
	if e.IsDir() || e.Type()&fs.ModeSymlink != 0 || (ext != ".json" && ext != ".jsonl") {
		return "", nil, false
	}
	path := filepath.Join(logsDir, e.Name())
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", nil, false
	}
	return path, info, true
}

func parseLegacyJSONLLog(path, name string, fallback time.Time, sourceKey string) ([]legacyLogEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []legacyLogEvent
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := append([]byte(nil), scanner.Bytes()...)
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		createdAt := fallback
		if t, ok := parseFirstLegacyTime(doc, "ts", "ended_at", "started_at"); ok {
			createdAt = t
		}
		sessionID := strField(doc, "session_id")
		if sessionID == "" {
			sessionID = fmt.Sprintf("legacy:%s:%s", sourceKey, legacySessionDate(createdAt))
		}
		payload := projectLegacyJSONLPayload(doc)
		sum := sha256.Sum256(raw)
		events = append(events, legacyLogEvent{
			SourceItemID: fmt.Sprintf("%s:%d", name, lineNo),
			Raw:          raw,
			Hash:         fmt.Sprintf("%x", sum[:]),
			Event: librarian.OpEvent{
				EventType: legacyJSONLEventType(strField(doc, "kind")),
				SessionID: sessionID,
				CreatedAt: createdAt,
				Payload:   payload,
			},
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func legacySourceKey(source string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(source)))
	return fmt.Sprintf("%x", sum[:])
}

func legacySessionDate(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format("2006-01-02")
}

func legacyJSONLEventType(kind string) string {
	switch kind {
	case "ConsumptionEvent":
		return "legacy.consumption"
	case "SessionEvent":
		return "legacy.session"
	case "RuntimeHealth":
		return "legacy.runtime_health"
	default:
		return "legacy.event"
	}
}

func projectLegacyJSONLPayload(doc map[string]any) map[string]any {
	out := map[string]any{"legacy_format": "jsonl_event"}
	copyAllowedFields(out, doc, legacyJSONLPayloadKeys)
	return out
}

func parseLegacySessionSummary(path, name string, fallback time.Time) (legacyLogEvent, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return legacyLogEvent{}, false, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return legacyLogEvent{}, false, fmt.Errorf("%s: %w", path, err)
	}
	sessionID := strField(doc, "session_id")
	if sessionID == "" {
		if _, hasContent := doc["content"]; hasContent {
			return legacyLogEvent{}, false, nil
		}
		return legacyLogEvent{}, false, fmt.Errorf("%s: missing session_id", path)
	}
	createdAt := fallback
	if t, ok := parseFirstLegacyTime(doc, "ended", "started"); ok {
		createdAt = t
	}
	payload := map[string]any{"legacy_format": "session_summary"}
	copyLegacyString(payload, doc, "started")
	copyLegacyString(payload, doc, "ended")
	copyLegacyNumber(payload, doc, "interactions")
	copyLegacyNumber(payload, doc, "files_changed")
	copyLegacyNumber(payload, doc, "memories_created")
	copyLegacyString(payload, doc, "model")
	copyLegacyString(payload, doc, "provider")
	if usage, ok := projectLegacyUsage(doc["usage"]); ok {
		payload["usage"] = usage
	}
	if cats := legacyToolCategoryTotals(doc["tool_calls"]); len(cats) > 0 {
		payload["tool_categories"] = cats
	}
	sum := sha256.Sum256(raw)
	return legacyLogEvent{
		SourceItemID: name,
		Raw:          raw,
		Hash:         fmt.Sprintf("%x", sum[:]),
		Event: librarian.OpEvent{
			EventType: "legacy.session.summary",
			SessionID: sessionID,
			CreatedAt: createdAt,
			Payload:   payload,
		},
	}, true, nil
}

func parseFirstLegacyTime(doc map[string]any, keys ...string) (time.Time, bool) {
	for _, key := range keys {
		s := strField(doc, key)
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func copyLegacyString(out, in map[string]any, key string) {
	if s := strField(in, key); s != "" {
		out[key] = s
	}
}

func copyLegacyNumber(out, in map[string]any, key string) {
	switch v := in[key].(type) {
	case float64:
		out[key] = v
	case int:
		out[key] = v
	}
}

func copyAllowedFields(out, in map[string]any, keys []string) {
	for _, key := range keys {
		if v, ok := in[key]; ok {
			out[key] = v
		}
	}
}

func projectLegacyUsage(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	out := map[string]any{}
	copyAllowedFields(out, m, legacyUsageKeys)
	return out, len(out) > 0
}

func legacyToolCategoryTotals(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for cat, raw := range m {
		if !legacyToolCategoryNames[cat] {
			continue
		}
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if total, ok := group["total"].(float64); ok {
			out[cat] = total
		}
	}
	return out
}

func writeLogImportAudit(mappings []librarian.LegacyLogImportMapping) (string, error) {
	dir, err := centralvault.Dir()
	if err != nil {
		return "", err
	}
	upgradeDir := filepath.Join(dir, "upgrade")
	if err := os.MkdirAll(upgradeDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(upgradeDir, "log-import-"+time.Now().UTC().Format("20060102-150405")+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, m := range mappings {
		if err := enc.Encode(m); err != nil {
			return "", err
		}
	}
	return path, nil
}
