package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/momhq/mom/cli/internal/adapters/harness"
	"github.com/momhq/mom/cli/internal/adapters/storage"
	"github.com/momhq/mom/cli/internal/config"
	"github.com/momhq/mom/cli/internal/memory"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
)

func init() {
	doctorCmd.Flags().Bool("verbose", false, "Show memory breakdowns by confidence, promotion state, and classification")
	doctorCmd.Flags().Bool("telemetry-preview", false, "Show telemetry status and a sample event")
	doctorCmd.Flags().Bool("landmarks", false, "List top landmark memories at current scope")
	doctorCmd.Flags().Bool("bundle", false, "Print a redacted diagnostic bundle to stdout")

	// Update doctor command metadata.
	doctorCmd.Long = `Check .mom/ health and local setup issues.

No network calls; this command reads only local files.

Use flags to access additional diagnostic sections:
  --verbose           Memory breakdowns by confidence, promotion state, classification
  --telemetry-preview Telemetry status and a sample event from today's file
  --landmarks         Top landmark memories at current scope
  --bundle            Redacted diagnostic bundle (stdout only, safe to share)`
}

// runDoctor is the main entry point for `mom doctor` and all its flag variants.
func runDoctor(cmd *cobra.Command, args []string) error {
	verbose, _ := cmd.Flags().GetBool("verbose")
	telemetryPreview, _ := cmd.Flags().GetBool("telemetry-preview")
	landmarksMode, _ := cmd.Flags().GetBool("landmarks")
	bundle, _ := cmd.Flags().GetBool("bundle")

	if bundle {
		return runDoctorBundle(cmd)
	}
	if telemetryPreview {
		return runDoctorTelemetryPreview(cmd)
	}
	if landmarksMode {
		return runDoctorLandmarks(cmd)
	}
	return runDoctorBase(cmd, verbose)
}

// ─── base doctor ──────────────────────────────────────────────────────────────

func runDoctorBase(cmd *cobra.Command, verbose bool) error {
	p := ux.NewPrinter(cmd.OutOrStdout())

	momDir, err := findMomDir()
	if err != nil {
		p.Fail(".mom/ directory: not found — run 'mom init' first")
		return err
	}

	// Detect legacy layout (.mom/kb/ present = pre-v0.8.0 install).
	if _, statErr := os.Stat(filepath.Join(momDir, "kb")); statErr == nil {
		p.Warn("Legacy layout detected (.mom/kb/ present)")
		p.Textf("  Run %s to migrate to the v0.8.0 flat layout.", p.HighlightCmd("mom upgrade"))
		return nil
	}

	failed := false

	// Check 1: .mom/ exists and is writable.
	if err := checkDirWritable(momDir); err != nil {
		p.Failf(".mom/ directory: %v", err)
		failed = true
	} else {
		p.Check(".mom/ directory: exists and writable")
	}

	// Check 2: config.yaml is valid.
	cfg, cfgErr := config.Load(momDir)
	if cfgErr != nil {
		p.Failf("config.yaml: %v", cfgErr)
		failed = true
	} else {
		p.Checkf("config.yaml: valid (harnesses: %s)", strings.Join(cfg.EnabledHarnesses(), ", "))
	}

	// Harness status: tier + integration capabilities.
	if cfg != nil && len(cfg.EnabledHarnesses()) > 0 {
		cwd, _ := os.Getwd()
		reg := harness.NewRegistry(cwd)
		for _, name := range cfg.EnabledHarnesses() {
			a, ok := reg.Get(name)
			if !ok {
				continue
			}
			var parts []string
			if _, ok := a.(harness.HookInstaller); ok {
				parts = append(parts, "hooks")
			}
			if _, ok := a.(harness.ExtensionInstaller); ok {
				parts = append(parts, "extension")
			}
			if ts, ok := a.(harness.TranscriptSource); ok {
				parts = append(parts, "transcript:"+ts.DefaultTranscriptDir())
			}
			detail := a.Tier().String()
			if len(parts) > 0 {
				detail += "  " + strings.Join(parts, "  ")
			}
			p.Checkf("%s: %s", name, detail)
		}
	}

	// Check 3: memory and core dirs exist.
	docsDir := filepath.Join(momDir, "memory")
	if _, statErr := os.Stat(docsDir); statErr != nil {
		p.Failf("memory/: %v", statErr)
		failed = true
	} else {
		p.Check("memory/: exists")
	}

	constraintsDir := filepath.Join(momDir, "constraints")
	if _, statErr := os.Stat(constraintsDir); statErr != nil {
		p.Warn("constraints/: not found")
	} else {
		p.Check("constraints/: exists")
	}

	skillsDir := filepath.Join(momDir, "skills")
	if _, statErr := os.Stat(skillsDir); statErr != nil {
		p.Warn("skills/: not found")
	} else {
		p.Check("skills/: exists")
	}

	// Check 4: All docs pass schema validation.
	diskDocIDs := make(map[string]bool)
	totalErrors := 0

	docErrors, docIDs := validateAllDocs(p, docsDir, "doc")
	totalErrors += docErrors
	for id := range docIDs {
		diskDocIDs[id] = true
	}

	constraintErrors, _ := validateAllDocs(p, constraintsDir, "constraint")
	totalErrors += constraintErrors

	skillErrors, _ := validateAllDocs(p, skillsDir, "skill")
	totalErrors += skillErrors

	if totalErrors > 0 {
		failed = true
	}

	// Check 5: Index consistency (JSON index).
	// Only memory docs are indexed — constraints and skills are static config.
	if orphanFail := checkIndexConsistency(p, momDir, diskDocIDs); orphanFail {
		failed = true
	}

	// Check 5b: SQLite index consistency.
	checkSQLiteConsistency(p, momDir, diskDocIDs)

	// Check 6: Communication mode.
	if cfg != nil {
		commMode := cfg.Communication.Mode
		if commMode == "" {
			commMode = "concise"
		}
		p.Checkf("communication mode: %s", commMode)
	}

	// Check 7: Version.
	p.Checkf("mom version: %s (%s)", Version, Commit)

	// Check 8: Telemetry status.
	if cfg != nil {
		if cfg.Telemetry.TelemetryEnabled() {
			p.Check("telemetry: enabled (local-only)")
		} else {
			p.Warn("telemetry: disabled")
		}
	}

	// Check 9: Memory breakdown.
	if verbose {
		printVerboseMemoryBreakdown(p, momDir)
	}

	// Check 10: Last session timestamp + recent errors from telemetry.
	if cfg != nil {
		telDir := filepath.Join(momDir, "logs")
		printLastSession(p, telDir)
		printRecentErrors(p, telDir, 5)
	}

	if failed {
		return fmt.Errorf("one or more doctor checks failed")
	}

	return nil
}

// checkSQLiteConsistency verifies the SQLite search index is present and
// its document count matches the JSON files on disk.
func checkSQLiteConsistency(p *ux.Printer, momDir string, diskDocIDs map[string]bool) {
	// Check if the cache/index.db file exists.
	dbPath := filepath.Join(momDir, "cache", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		p.Warnf("SQLite index: not found — it will be rebuilt automatically on next indexed access")
		return
	}

	idx := storage.NewIndexedAdapter(momDir)
	defer idx.Close()

	// Compare counts via a search-all query.
	results, err := idx.Search(storage.SearchOptions{Limit: 100000})
	if err != nil {
		p.Warnf("SQLite index: query error — %v", err)
		return
	}

	dbCount := len(results)
	diskCount := len(diskDocIDs)

	if dbCount == diskCount {
		p.Checkf("SQLite index: %d docs indexed (consistent)", dbCount)
	} else {
		p.Warnf("SQLite index: %d indexed vs %d on disk — it will be rebuilt automatically on next indexed access", dbCount, diskCount)
	}
}

// ─── --verbose additions ──────────────────────────────────────────────────────

// printVerboseMemoryBreakdown reads local memory docs and prints breakdowns by
// confidence, promotion_state, and classification.
func printVerboseMemoryBreakdown(p *ux.Printer, momDir string) {
	memDir := filepath.Join(momDir, "memory")
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return
	}

	p.Blank()
	p.Bold("Memory breakdown (verbose)")

	promotion := map[string]int{}
	classification := map[string]int{}
	landmarks := 0

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		doc, err := memory.LoadDoc(filepath.Join(memDir, e.Name()))
		if err != nil {
			continue
		}
		promotion[doc.PromotionState]++
		classification[doc.Classification]++
		if doc.Landmark {
			landmarks++
		}
	}

	p.KeyValue("  Path", shortenPath(memDir), 10)
	p.KeyValue("    Promotion", fmt.Sprintf("draft=%d  curated=%d",
		promotion["draft"], promotion["curated"]), 16)
	p.KeyValue("    Classification", fmt.Sprintf("PUBLIC=%d  INTERNAL=%d  CONFIDENTIAL=%d",
		classification["PUBLIC"], classification["INTERNAL"], classification["CONFIDENTIAL"]), 20)
	p.KeyValue("    Landmarks", fmt.Sprintf("%d", landmarks), 16)

	// Capture pipeline latency from telemetry.
	if momDir != "" {
		telDir := filepath.Join(momDir, "logs")
		printCapturePipelineLatency(p, telDir)
		printExtractorModelUsage(p, telDir)
	}
}

// printCapturePipelineLatency computes p50/p95 of CaptureEvent latency from
// the last 7 days of telemetry. Latency is inferred from CaptureEvent.
func printCapturePipelineLatency(p *ux.Printer, telDir string) {
	events := readTelemetryWindow(telDir, 7)
	var latencies []int64

	for _, raw := range events {
		if raw["kind"] != "CaptureEvent" {
			continue
		}
		if v, ok := raw["latency_ms"]; ok {
			if n, isFloat := v.(float64); isFloat {
				latencies = append(latencies, int64(n))
			}
		}
	}

	if len(latencies) == 0 {
		p.Blank()
		p.Muted("  Capture latency: no data")
		return
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)*50/100]
	p95 := latencies[len(latencies)*95/100]
	p.Blank()
	p.KeyValue("  Capture latency (last 7d)", fmt.Sprintf("p50=%dms  p95=%dms", p50, p95), 30)
}

// printExtractorModelUsage prints the top 5 extractor models used in last 7 days.
func printExtractorModelUsage(p *ux.Printer, telDir string) {
	events := readTelemetryWindow(telDir, 7)
	counts := map[string]int{}

	for _, raw := range events {
		if raw["kind"] != "CaptureEvent" {
			continue
		}
		if m, ok := raw["extractor_model"].(string); ok && m != "" {
			counts[m]++
		}
	}

	if len(counts) == 0 {
		p.Muted("  Extractor model usage (last 7d): no data")
		return
	}

	type pair struct {
		model string
		count int
	}
	var pairs []pair
	for m, c := range counts {
		pairs = append(pairs, pair{m, c})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].count > pairs[j].count })

	p.Bold("  Extractor model usage (last 7d)")
	for i, pr := range pairs {
		if i >= 5 {
			break
		}
		p.KeyValue(fmt.Sprintf("    %s", pr.model), fmt.Sprintf("%d captures", pr.count), 44)
	}
}

// ─── --telemetry-preview ──────────────────────────────────────────────────────

func runDoctorTelemetryPreview(cmd *cobra.Command) error {
	p := ux.NewPrinter(cmd.OutOrStdout())
	momDir, momDirErr := findMomDir()

	// Config for telemetry enabled status.
	var telEnabled bool
	if momDirErr == nil {
		cfg, cfgErr := config.Load(momDir)
		if cfgErr == nil {
			telEnabled = cfg.Telemetry.TelemetryEnabled()
		} else {
			telEnabled = true // default
		}
	}

	w := 20
	p.Bold("Telemetry Preview")
	p.Blank()
	p.KeyValue("Mode", "LOCAL-ONLY (no network calls)", w)
	if !telEnabled {
		p.KeyValue("Status", "disabled", w)
		p.Blank()
		p.Textf("To enable: set telemetry.enabled: true in %s", p.HighlightCmd(".mom/config.yaml"))
		return nil
	}
	p.KeyValue("Status", "enabled", w)

	if momDirErr != nil {
		p.Blank()
		p.Warn("no .mom/ directory found")
		return nil
	}

	telDir := filepath.Join(momDir, "logs")
	today := time.Now().UTC().Format("2006-01-02")
	todayFile := filepath.Join(telDir, today+".jsonl")

	// Count today's events by kind.
	todayEvents, todayRaw := readJSONLFile(todayFile)
	totalToday := len(todayEvents)
	kindCounts := map[string]int{}
	for _, ev := range todayEvents {
		if k, ok := ev["kind"].(string); ok {
			kindCounts[k]++
		}
	}

	p.Blank()
	p.KeyValue("Events today", fmt.Sprintf("%d", totalToday), w)
	if totalToday > 0 {
		for _, kind := range []string{"SessionEvent", "CaptureEvent", "MemoryMutation", "ConsumptionEvent", "RuntimeHealth"} {
			if c := kindCounts[kind]; c > 0 {
				p.KeyValue(fmt.Sprintf("  %s", kind), fmt.Sprintf("%d", c), w)
			}
		}
		for k, c := range kindCounts {
			switch k {
			case "SessionEvent", "CaptureEvent", "MemoryMutation", "ConsumptionEvent", "RuntimeHealth":
			default:
				p.KeyValue(fmt.Sprintf("  %s", k), fmt.Sprintf("%d", c), w)
			}
		}
	}

	// Sample event: most recent (last line).
	p.Blank()
	if len(todayRaw) > 0 {
		lastLine := todayRaw[len(todayRaw)-1]
		p.Bold("Sample event (most recent)")
		var pretty map[string]any
		if err := json.Unmarshal([]byte(lastLine), &pretty); err == nil {
			out, _ := json.MarshalIndent(pretty, "", "  ")
			p.Muted(string(out))
		} else {
			p.Muted(lastLine)
		}
	} else {
		p.Muted("no events yet today")
	}

	// File info.
	p.Blank()
	if info, err := os.Stat(todayFile); err == nil {
		size := info.Size()
		var sizeStr string
		if size < 1024 {
			sizeStr = fmt.Sprintf("%d B", size)
		} else {
			sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1024)
		}
		rel := ".mom/logs/" + today + ".jsonl"
		p.Muted(fmt.Sprintf("Full file: %s (%s)", rel, sizeStr))
	} else {
		p.Muted(fmt.Sprintf("Full file: .mom/logs/%s.jsonl (not yet created)", today))
	}

	return nil
}

// ─── --landmarks ──────────────────────────────────────────────────────────────

const landmarkComputationThreshold = 100

func runDoctorLandmarks(cmd *cobra.Command) error {
	p := ux.NewPrinter(cmd.OutOrStdout())
	momDir, err := findMomDir()
	if err != nil {
		p.Warn("no .mom/ directory found — run 'mom init' first")
		return nil
	}
	memDir := filepath.Join(momDir, "memory")
	entries, err := os.ReadDir(memDir)
	if err != nil {
		p.Warn("no landmark memories found (memory/ unreadable)")
		return nil
	}

	// Count total memories first for threshold check.
	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}

	if len(jsonFiles) < landmarkComputationThreshold {
		p.Warn("no landmarks computed yet")
		p.Textf("Run %s to compute.", p.HighlightCmd("mom map --path ."))
		p.Muted(fmt.Sprintf("graph below threshold: %d/%d memories", len(jsonFiles), landmarkComputationThreshold))
		return nil
	}

	// Load all docs, filter landmarks.
	type landmarkEntry struct {
		doc *memory.Doc
	}
	var landmarks []landmarkEntry

	for _, name := range jsonFiles {
		doc, err := memory.LoadDoc(filepath.Join(memDir, name))
		if err != nil {
			continue
		}
		if doc.Landmark {
			landmarks = append(landmarks, landmarkEntry{doc: doc})
		}
	}

	if len(landmarks) == 0 {
		p.Warn("no landmarks found")
		p.Textf("Run %s to compute.", p.HighlightCmd("mom map --path ."))
		return nil
	}

	// Sort by centrality_score desc.
	sort.Slice(landmarks, func(i, j int) bool {
		si, sj := 0.0, 0.0
		if landmarks[i].doc.CentralityScore != nil {
			si = *landmarks[i].doc.CentralityScore
		}
		if landmarks[j].doc.CentralityScore != nil {
			sj = *landmarks[j].doc.CentralityScore
		}
		return si > sj
	})

	p.Bold("Top Landmarks")
	p.Muted(fmt.Sprintf("memory: %s", shortenPath(memDir)))
	p.Blank()

	w := 16
	shown := 0
	for _, lm := range landmarks {
		if shown >= 10 {
			break
		}
		doc := lm.doc
		centrality := 0.0
		if doc.CentralityScore != nil {
			centrality = *doc.CentralityScore
		}
		created := doc.Created.Format("2006-01-02")
		tagStr := strings.Join(doc.Tags, ", ")
		if len(tagStr) > 40 {
			tagStr = tagStr[:37] + "..."
		}
		summary := doc.Summary
		if summary == "" {
			summary = doc.ID
		}

		p.Diamond(truncate(doc.ID, 50))
		p.KeyValue("  Centrality", fmt.Sprintf("%.4f", centrality), w)
		p.KeyValue("  Created", created, w)
		p.KeyValue("  Tags", fmt.Sprintf("[%d] %s", len(doc.Tags), tagStr), w)
		if summary != doc.ID {
			p.Muted(fmt.Sprintf("  %s", truncate(summary, 76)))
		}
		p.Blank()
		shown++
	}

	return nil
}

// ─── --bundle ────────────────────────────────────────────────────────────────

func runDoctorBundle(cmd *cobra.Command) error {
	cmd.Printf("=== MOM DIAGNOSTIC BUNDLE ===\n")
	cmd.Printf("Generated: (deterministic — no timestamp)\n")
	cmd.Printf("Note: All network calls: NONE. Local files only.\n\n")

	// Version info.
	cmd.Printf("--- Version ---\n")
	cmd.Printf("Mom:  %s (%s)\n", Version, Commit)
	cmd.Printf("Go:   %s\n", runtime.Version())
	cmd.Printf("OS:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
	cmd.Printf("\n")

	momDir, momDirErr := findMomDir()
	if momDirErr != nil {
		cmd.Printf("--- Error ---\n")
		cmd.Printf(".mom/ directory not found. Run 'mom init' first.\n")
		return nil
	}

	cfg, cfgErr := config.Load(momDir)

	// Adapter status.
	cmd.Printf("--- Adapter Status ---\n")
	if cfgErr != nil {
		cmd.Printf("(config unavailable: %v)\n", cfgErr)
	} else {
		cwd, _ := os.Getwd()
		printBundleAdapterStatus(cmd, cwd, cfg)
	}
	cmd.Printf("\n")

	// Current local project metadata (not a scope hierarchy).
	cmd.Printf("--- Project ---\n")
	cmd.Printf("mom_dir: %s\n", momDir)
	cmd.Printf("\n")

	cmd.Printf("--- Memory Counts ---\n")
	memDir := filepath.Join(momDir, "memory")
	entries, _ := os.ReadDir(memDir)
	totalMem := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			totalMem++
		}
	}
	cmd.Printf("Total: %d\n", totalMem)
	cmd.Printf("\n")

	// Recent errors from RuntimeHealth — content stripped.
	cmd.Printf("--- Recent Errors ---\n")
	telDir := filepath.Join(momDir, "logs")
	bundleErrors := readRecentErrors(telDir, 10)
	if len(bundleErrors) == 0 {
		cmd.Printf("(none)\n")
	} else {
		for _, e := range bundleErrors {
			errType := "(nil)"
			if e.ErrorType != nil {
				errType = *e.ErrorType
			}
			cmd.Printf("  ts=%s  runtime=%s  error_type=%s\n", e.TS, e.Runtime, errType)
		}
	}
	cmd.Printf("\n")

	// Telemetry summary — counts per kind, no event bodies.
	cmd.Printf("--- Telemetry Summary ---\n")
	if cfgErr == nil && !cfg.Telemetry.TelemetryEnabled() {
		cmd.Printf("Status: disabled\n")
	} else {
		cmd.Printf("Status: enabled (local-only)\n")
		events, _ := readJSONLFile(filepath.Join(telDir, time.Now().UTC().Format("2006-01-02")+".jsonl"))
		kindCounts := map[string]int{}
		for _, ev := range events {
			if k, ok := ev["kind"].(string); ok {
				kindCounts[k]++
			}
		}
		cmd.Printf("Events today: %d\n", len(events))
		var kinds []string
		for k := range kindCounts {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			cmd.Printf("  %s: %d\n", k, kindCounts[k])
		}
	}
	cmd.Printf("\n")

	cmd.Printf("=== END BUNDLE ===\n")
	return nil
}

func printBundleAdapterStatus(cmd *cobra.Command, cwd string, cfg *config.Config) {
	enabled := cfg.EnabledHarnesses()
	if len(enabled) == 0 {
		cmd.Printf("(no adapters enabled)\n")
		return
	}
	registry := harness.NewRegistry(cwd)
	for _, name := range enabled {
		a, ok := registry.Get(name)
		if !ok {
			cmd.Printf("  %s: unknown adapter\n", name)
			continue
		}
		cap := a.Capabilities()
		cmd.Printf("  %s v%s  [%s]\n", cap.Name, cap.Version, a.Tier())

		// Integration mechanisms.
		var mechanisms []string
		if _, ok := a.(harness.HookInstaller); ok {
			mechanisms = append(mechanisms, "hooks")
		}
		if _, ok := a.(harness.ExtensionInstaller); ok {
			mechanisms = append(mechanisms, "extension")
		}
		if len(mechanisms) > 0 {
			cmd.Printf("    integration:  %s\n", strings.Join(mechanisms, ", "))
		}

		// Transcript source.
		if ts, ok := a.(harness.TranscriptSource); ok {
			cmd.Printf("    transcript:   %s\n", ts.DefaultTranscriptDir())
		}

		// MRP event coverage.
		if len(cap.Supports) > 0 {
			cmd.Printf("    supported:    %s\n", strings.Join(cap.Supports, ", "))
		}
		if len(cap.Experimental) > 0 {
			cmd.Printf("    experimental: %s\n", strings.Join(cap.Experimental, ", "))
		}
	}
}

// ─── telemetry helpers ────────────────────────────────────────────────────────

// runtimeHealthEvent is a minimal struct for reading RuntimeHealth events.
type runtimeHealthEvent struct {
	Kind          string  `json:"kind"`
	Runtime       string  `json:"runtime"`
	TS            string  `json:"ts"`
	WrapUpSuccess bool    `json:"wrap_up_success"`
	ErrorType     *string `json:"error_type"`
	LatencyMS     int64   `json:"latency_ms"`
}

// readJSONLFile reads a JSONL file, returning parsed events and raw lines.
// Gracefully handles missing or empty files.
func readJSONLFile(path string) ([]map[string]any, []string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var events []map[string]any
	var rawLines []string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		rawLines = append(rawLines, line)
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err == nil {
			events = append(events, ev)
		}
	}

	return events, rawLines
}

// readTelemetryWindow reads the last N days of JSONL files from telDir.
func readTelemetryWindow(telDir string, days int) []map[string]any {
	now := time.Now().UTC()
	var all []map[string]any

	for i := 0; i < days; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		path := filepath.Join(telDir, date+".jsonl")
		events, _ := readJSONLFile(path)
		all = append(all, events...)
	}

	return all
}

// printLastSession finds and prints the timestamp of the most recent SessionEvent.
func printLastSession(p *ux.Printer, telDir string) {
	events := readTelemetryWindow(telDir, 7)
	var lastTS string

	for _, ev := range events {
		if ev["kind"] != "SessionEvent" {
			continue
		}
		ts := ""
		if s, ok := ev["started_at"].(string); ok {
			ts = s
		}
		if ts > lastTS {
			lastTS = ts
		}
	}

	if lastTS == "" {
		p.Warn("last session: no session events found")
	} else {
		p.Checkf("last session: %s", lastTS)
	}
}

// printRecentErrors reads the last N RuntimeHealth events with errors.
func printRecentErrors(p *ux.Printer, telDir string, limit int) {
	errors := readRecentErrors(telDir, limit)
	if len(errors) == 0 {
		return
	}

	p.Warnf("recent runtime errors (%d):", len(errors))
	for _, e := range errors {
		errType := "(unknown)"
		if e.ErrorType != nil {
			errType = *e.ErrorType
		}
		p.Muted(fmt.Sprintf("  ts=%s  runtime=%s  error_type=%s", e.TS, e.Runtime, errType))
	}
}

// readRecentErrors returns at most limit RuntimeHealth events where ErrorType != nil.
func readRecentErrors(telDir string, limit int) []runtimeHealthEvent {
	events := readTelemetryWindow(telDir, 7)
	var errors []runtimeHealthEvent

	for _, raw := range events {
		if raw["kind"] != "RuntimeHealth" {
			continue
		}
		data, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var ev runtimeHealthEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		if ev.ErrorType == nil && ev.WrapUpSuccess {
			continue
		}
		if ev.ErrorType != nil || !ev.WrapUpSuccess {
			errors = append(errors, ev)
		}
	}

	// Return only the most recent ones.
	if len(errors) > limit {
		errors = errors[len(errors)-limit:]
	}
	return errors
}

// ─── shared helpers ───────────────────────────────────────────────────────────

// shortenPath replaces the home directory prefix with ~.
func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// truncate shortens s to at most n runes.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-3]) + "..."
}
