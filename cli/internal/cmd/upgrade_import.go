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
	"github.com/spf13/cobra"
)

type legacyVaultPlan struct {
	Path           string
	Fingerprint    string
	Docs           []legacyMemoryDoc
	LogFingerprint string
	Logs           []legacyLogEvent
}

type legacyMemoryDoc struct {
	Path string
	Raw  []byte
	Doc  map[string]any
	Hash string
}

type importSummary struct {
	Vaults      int
	Memories    int
	Mappings    []librarian.LegacyImportMapping
	Skipped     int
	Audit       string
	LogEvents   int
	LogMappings []librarian.LegacyLogImportMapping
	LogSkipped  int
	LogAudit    string
}

func discoverLegacyVaultsForImport() ([]legacyVaultPlan, error) {
	home := os.Getenv("MOM_UPGRADE_SCAN_ROOT")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve $HOME: %w", err)
		}
	}
	centralDir, _ := centralvault.Dir()
	var plans []legacyVaultPlan
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == home {
			return nil
		}
		if depth(home, path) > 5 {
			return filepath.SkipDir
		}
		name := d.Name()
		if name == ".mom" {
			if samePath(path, centralDir) {
				return filepath.SkipDir
			}
			docs, fingerprint, memErr := readLegacyMemoryDocs(path)
			logs, logFingerprint, logErr := readLegacyLogEvents(path)
			if logErr != nil && !os.IsNotExist(logErr) {
				return logErr
			}
			if (memErr == nil && len(docs) > 0) || (logErr == nil && len(logs) > 0) {
				plans = append(plans, legacyVaultPlan{Path: path, Fingerprint: fingerprint, Docs: docs, LogFingerprint: logFingerprint, Logs: logs})
			}
			return filepath.SkipDir
		}
		if shouldSkipImportDir(name) {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover legacy vaults: %w", err)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Path < plans[j].Path })
	return plans, nil
}

func shouldSkipImportDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "Library", "Applications", "Movies", "Music", "Pictures", "Public", "node_modules", "vendor", "Caches", "Trash", "tmp", "temp":
		return true
	default:
		return false
	}
}

func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(rel, string(os.PathSeparator)))
}

func samePath(a, b string) bool {
	aa, errA := filepath.EvalSymlinks(a)
	bb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return aa == bb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func readLegacyMemoryDocs(momDir string) ([]legacyMemoryDoc, string, error) {
	memDir := filepath.Join(momDir, "memory")
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return nil, "", err
	}
	var docs []legacyMemoryDoc
	var parts []string
	for _, e := range entries {
		path, ok := legacyMemoryJSONPath(memDir, e)
		if !ok {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue
		}
		sum := sha256.Sum256(raw)
		hash := fmt.Sprintf("%x", sum[:])
		docs = append(docs, legacyMemoryDoc{Path: path, Raw: raw, Doc: doc, Hash: hash})
		parts = append(parts, e.Name()+":"+hash)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return docs, fmt.Sprintf("%x", sum[:]), nil
}

func legacyMemoryJSONPath(memDir string, e fs.DirEntry) (string, bool) {
	if e.IsDir() || filepath.Ext(e.Name()) != ".json" || e.Type()&fs.ModeSymlink != 0 {
		return "", false
	}
	path := filepath.Join(memDir, e.Name())
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

func confirmUpgradeImport(cmd *cobra.Command, vaults, memories int, logs ...int) bool {
	if strings.EqualFold(os.Getenv("MOM_UPGRADE_ASSUME_YES"), "1") || strings.EqualFold(os.Getenv("MOM_UPGRADE_ASSUME_YES"), "true") {
		return true
	}
	logCount := 0
	if len(logs) > 0 {
		logCount = logs[0]
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Import %d memories and %d log events from %d legacy .mom folders into the central vault? [y/N] ", memories, logCount, vaults)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func executeCentralMemoryImport(plans []legacyVaultPlan) (importSummary, error) {
	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		return importSummary{}, fmt.Errorf("open central vault: %w", err)
	}
	defer func() { _ = closeFn() }()
	var summary importSummary
	for _, plan := range plans {
		if len(plan.Docs) == 0 {
			continue
		}
		records := make([]librarian.LegacyImportMemory, 0, len(plan.Docs))
		for _, d := range plan.Docs {
			rec, err := legacyDocToImportRecord(d)
			if err != nil {
				return importSummary{}, fmt.Errorf("%s: %w", d.Path, err)
			}
			records = append(records, rec)
		}
		mappings, skipped, err := lib.ImportLegacyMemories(plan.Path, plan.Fingerprint, records)
		if err != nil {
			return importSummary{}, err
		}
		if skipped {
			summary.Skipped++
			continue
		}
		summary.Vaults++
		summary.Memories += len(records)
		summary.Mappings = append(summary.Mappings, mappings...)
	}
	if len(summary.Mappings) > 0 {
		audit, err := writeImportAudit(summary.Mappings)
		if err != nil {
			return importSummary{}, err
		}
		summary.Audit = audit
	}
	return summary, nil
}

func legacyDocToImportRecord(d legacyMemoryDoc) (librarian.LegacyImportMemory, error) {
	doc := d.Doc
	oldID := strField(doc, "id")
	contentAny := doc["content"]
	if contentAny == nil {
		contentAny = map[string]any{"text": string(d.Raw)}
	}
	content, err := json.Marshal(contentAny)
	if err != nil {
		return librarian.LegacyImportMemory{}, err
	}
	createdAt := time.Now().UTC()
	if s := strField(doc, "created"); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			createdAt = t
		}
	}
	tags := normaliseLegacyTags(doc["tags"])
	createdBy := strField(doc, "created_by")
	actor := createdBy
	if actor == "" {
		actor = "legacy-import"
	}
	sessionID := strField(doc, "session_id")
	if sessionID == "" {
		sessionID = "legacy-import"
	}
	return librarian.LegacyImportMemory{
		OldID: oldID,
		Memory: librarian.InsertMemory{
			Type:                   mapLegacyType(strField(doc, "type")),
			Summary:                strField(doc, "summary"),
			Content:                string(content),
			CreatedAt:              createdAt,
			SessionID:              sessionID,
			ProvenanceActor:        actor,
			ProvenanceSourceType:   "legacy-memory-json",
			ProvenanceTriggerEvent: "upgrade",
		},
		Tags:      tags,
		CreatedBy: createdBy,
		Hash:      d.Hash,
	}, nil
}

func strField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func normaliseLegacyTags(v any) []string {
	var out []string
	seen := map[string]bool{}
	if arr, ok := v.([]any); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				n := librarian.NormalizeTagName(s)
				if n != "" && !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		}
	}
	return out
}

func mapLegacyType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "decision", "fact", "pattern", "semantic":
		return "semantic"
	case "procedure", "runbook", "how-to", "procedural":
		return "procedural"
	case "session-log", "event", "episodic":
		return "episodic"
	case "untyped":
		return "untyped"
	default:
		return "untyped"
	}
}

func writeImportAudit(mappings []librarian.LegacyImportMapping) (string, error) {
	dir, err := centralvault.Dir()
	if err != nil {
		return "", err
	}
	upgradeDir := filepath.Join(dir, "upgrade")
	if err := os.MkdirAll(upgradeDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(upgradeDir, "import-"+time.Now().UTC().Format("20060102-150405")+".jsonl")
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
