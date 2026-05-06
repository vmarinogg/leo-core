package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/centralvault"
)

// newStatusTestDir creates a temp .mom/ dir with constraints, skills, and config
// set up for mom_status tests.
func newStatusTestDir(t *testing.T) string {
	t.Helper()
	vaultPath := setCentralVault(t)
	centralDir := filepath.Dir(vaultPath)
	dir := t.TempDir()
	leoDir := filepath.Join(dir, ".mom")

	// Required dirs.
	for _, sub := range []string{"memory"} {
		if err := os.MkdirAll(filepath.Join(leoDir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, sub := range []string{"constraints", "skills"} {
		if err := os.MkdirAll(filepath.Join(centralDir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}

	// config.yaml with language and communication mode.
	cfg := `version: "1"
scope: repo
runtimes:
  claude:
    enabled: true
user:
  language: en
communication:
  mode: concise
`
	if err := os.WriteFile(filepath.Join(leoDir, "config.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	// identity.json (required by newTestLeoDir pattern; not used by mom_status but
	// keeps the server happy during optional identity resource reads).
	identity := map[string]any{
		"id":         "identity",
		"type":       "identity",
		"lifecycle":  "permanent",
		"scope":      "project",
		"tags":       []string{"identity"},
		"created":    time.Now().Format(time.RFC3339),
		"created_by": "test",
		"updated":    time.Now().Format(time.RFC3339),
		"updated_by": "test",
		"content":    map[string]any{"name": "Test Project"},
	}
	writeJSON(t, filepath.Join(leoDir, "identity.json"), identity)

	// Two constraint docs.
	writeJSON(t, filepath.Join(centralDir, "constraints", "anti-hallucination.json"), map[string]any{
		"id":         "anti-hallucination",
		"type":       "constraint",
		"lifecycle":  "permanent",
		"scope":      "core",
		"tags":       []string{"constraint"},
		"summary":    "Never hallucinate",
		"created":    time.Now().Format(time.RFC3339),
		"created_by": "test",
		"updated":    time.Now().Format(time.RFC3339),
		"updated_by": "test",
		"content":    map[string]any{"rule": "no hallucination"},
	})
	writeJSON(t, filepath.Join(centralDir, "constraints", "escalation-triggers.json"), map[string]any{
		"id":         "escalation-triggers",
		"type":       "constraint",
		"lifecycle":  "permanent",
		"scope":      "core",
		"tags":       []string{"constraint"},
		"summary":    "Stop and ask before destructive actions",
		"created":    time.Now().Format(time.RFC3339),
		"created_by": "test",
		"updated":    time.Now().Format(time.RFC3339),
		"updated_by": "test",
		"content":    map[string]any{"rule": "escalate"},
	})

	// One skill doc.
	writeJSON(t, filepath.Join(centralDir, "skills", "session-wrap-up.json"), map[string]any{
		"id":         "session-wrap-up",
		"type":       "skill",
		"lifecycle":  "permanent",
		"scope":      "core",
		"tags":       []string{"skill"},
		"summary":    "End-of-session knowledge propagation",
		"created":    time.Now().Format(time.RFC3339),
		"created_by": "test",
		"updated":    time.Now().Format(time.RFC3339),
		"updated_by": "test",
		"content":    map[string]any{"steps": "inventory, classify, write, report"},
	})

	// One memory row to verify total_memories count.
	insertCentralMemory(t, "A test memory", "detail x", []string{"test"})

	return leoDir
}

// callMomStatus is a helper that sends the mom_status tool call and returns
// the parsed text payload as a map.
func callMomStatus(t *testing.T, leoDir string) map[string]any {
	t.Helper()
	inW, outR, _ := runServer(t, leoDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)

	sendRequest(t, inW, "tools/call", 2, map[string]any{
		"name":      "mom_status",
		"arguments": map[string]any{},
	})
	resp := readResponse(t, outR)

	if resp["error"] != nil {
		t.Fatalf("mom_status returned error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("content missing or empty")
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatal("first content item not a map")
	}
	text, _ := first["text"].(string)
	if text == "" {
		t.Fatal("text content is empty")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parsing mom_status text as JSON: %v\ntext: %s", err, text)
	}
	return payload
}

func TestMomStatusTopLevelKeys(t *testing.T) {
	leoDir := newStatusTestDir(t)
	payload := callMomStatus(t, leoDir)

	requiredKeys := []string{
		"identity",
		"operating_rules",
		"boundaries",
		"tools",
		"constraints",
		"skills",
		"modes",
		"vault_state",
		"doc_schema",
	}
	for _, k := range requiredKeys {
		if _, ok := payload[k]; !ok {
			t.Errorf("missing top-level key %q in mom_status response", k)
		}
	}
}

func TestMomStatusIdentity(t *testing.T) {
	leoDir := newStatusTestDir(t)
	payload := callMomStatus(t, leoDir)

	identity, ok := payload["identity"].(map[string]any)
	if !ok {
		t.Fatalf("identity is not a map: %T", payload["identity"])
	}
	for _, field := range []string{"name", "expansion", "tagline", "role", "voice", "stance"} {
		if v, ok := identity[field].(string); !ok || v == "" {
			t.Errorf("identity.%s missing or empty", field)
		}
	}
	if identity["name"] != "MOM" {
		t.Errorf("identity.name = %q; want MOM", identity["name"])
	}
}

func TestMomStatusConstraintsLoaded(t *testing.T) {
	leoDir := newStatusTestDir(t)
	payload := callMomStatus(t, leoDir)

	constraints, ok := payload["constraints"].([]any)
	if !ok {
		t.Fatalf("constraints is not an array: %T", payload["constraints"])
	}
	if len(constraints) < 2 {
		t.Errorf("expected at least 2 constraints, got %d", len(constraints))
	}

	ids := make(map[string]bool)
	for _, raw := range constraints {
		c, ok := raw.(map[string]any)
		if !ok {
			t.Fatal("constraint item not a map")
		}
		id, _ := c["id"].(string)
		if id == "" {
			t.Error("constraint item missing id")
		}
		ids[id] = true
		if _, ok := c["summary"]; !ok {
			t.Errorf("constraint %q missing summary field", id)
		}
		if _, ok := c["path"]; !ok {
			t.Errorf("constraint %q missing path field", id)
		}
	}

	for _, expectedID := range []string{"anti-hallucination", "escalation-triggers"} {
		if !ids[expectedID] {
			t.Errorf("constraint %q not found in response", expectedID)
		}
	}
}

func TestMomStatusSkillsLoaded(t *testing.T) {
	leoDir := newStatusTestDir(t)
	payload := callMomStatus(t, leoDir)

	skills, ok := payload["skills"].([]any)
	if !ok {
		t.Fatalf("skills is not an array: %T", payload["skills"])
	}
	if len(skills) < 1 {
		t.Errorf("expected at least 1 skill, got %d", len(skills))
	}

	found := false
	for _, raw := range skills {
		s, ok := raw.(map[string]any)
		if !ok {
			t.Fatal("skill item not a map")
		}
		id, _ := s["id"].(string)
		if id == "" {
			t.Error("skill item missing id")
		}
		if _, ok := s["summary"]; !ok {
			t.Errorf("skill %q missing summary field", id)
		}
		if _, ok := s["path"]; !ok {
			t.Errorf("skill %q missing path field", id)
		}
		if id == "session-wrap-up" {
			found = true
		}
	}
	if !found {
		t.Error("skill session-wrap-up not found in response")
	}
}

func TestMomStatusVaultState(t *testing.T) {
	leoDir := newStatusTestDir(t)
	payload := callMomStatus(t, leoDir)

	vs, ok := payload["vault_state"].(map[string]any)
	if !ok {
		t.Fatalf("vault_state is not a map: %T", payload["vault_state"])
	}

	// scopes must be an array.
	scopes, ok := vs["scopes"].([]any)
	if !ok {
		t.Fatalf("vault_state.scopes is not an array: %T", vs["scopes"])
	}
	if len(scopes) == 0 {
		t.Error("vault_state.scopes is empty")
	}

	// total_memories must be >= 1 (we wrote one memory doc in setup).
	totalRaw, ok := vs["total_memories"]
	if !ok {
		t.Fatal("vault_state.total_memories missing")
	}
	total, ok := totalRaw.(float64)
	if !ok {
		t.Fatalf("vault_state.total_memories not a number: %T", totalRaw)
	}
	if total < 1 {
		t.Errorf("vault_state.total_memories = %v; want >= 1", total)
	}

	// record_mode must be "continuous".
	if vs["record_mode"] != "continuous" {
		t.Errorf("vault_state.record_mode = %v; want continuous", vs["record_mode"])
	}
}

func TestMomStatusModes(t *testing.T) {
	leoDir := newStatusTestDir(t)
	payload := callMomStatus(t, leoDir)

	modes, ok := payload["modes"].(map[string]any)
	if !ok {
		t.Fatalf("modes is not a map: %T", payload["modes"])
	}
	lang, _ := modes["language"].(string)
	if lang == "" {
		t.Error("modes.language missing or empty")
	}
	if !strings.Contains(lang, "## Language:") {
		t.Errorf("modes.language should contain full rules, got: %s", lang[:min(len(lang), 80)])
	}
	comm, _ := modes["communication"].(string)
	if comm == "" {
		t.Error("modes.communication missing or empty")
	}
	if !strings.Contains(comm, "## Communication mode:") {
		t.Errorf("modes.communication should contain full rules, got: %s", comm[:min(len(comm), 80)])
	}
	auto, _ := modes["autonomy"].(string)
	if auto == "" {
		t.Error("modes.autonomy missing or empty")
	}
	if !strings.Contains(auto, "## Autonomy level:") {
		t.Errorf("modes.autonomy should contain full rules, got: %s", auto[:min(len(auto), 80)])
	}
}

func TestMomStatusInToolsList(t *testing.T) {
	leoDir := newStatusTestDir(t)
	inW, outR, _ := runServer(t, leoDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)

	sendRequest(t, inW, "tools/list", 2, nil)
	resp := readResponse(t, outR)

	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)

	found := false
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == "mom_status" {
			found = true
		}
	}
	if !found {
		t.Error("mom_status not found in tools/list")
	}
}

func TestMomStatusOperatingRules(t *testing.T) {
	leoDir := newStatusTestDir(t)
	payload := callMomStatus(t, leoDir)

	rules, ok := payload["operating_rules"].(map[string]any)
	if !ok {
		t.Fatalf("operating_rules is not a map: %T", payload["operating_rules"])
	}
	for _, field := range []string{"on_start", "recall", "recording", "wrap_up"} {
		if v, _ := rules[field].(string); v == "" {
			t.Errorf("operating_rules.%s missing or empty", field)
		}
	}
}

func TestMomStatusBoundaries(t *testing.T) {
	leoDir := newStatusTestDir(t)
	payload := callMomStatus(t, leoDir)

	boundaries, ok := payload["boundaries"].([]any)
	if !ok {
		t.Fatalf("boundaries is not an array: %T", payload["boundaries"])
	}
	if len(boundaries) == 0 {
		t.Error("boundaries array is empty")
	}
}

func TestMomStatusDocSchema(t *testing.T) {
	leoDir := newStatusTestDir(t)
	payload := callMomStatus(t, leoDir)

	schema, ok := payload["doc_schema"].(string)
	if !ok || schema == "" {
		t.Errorf("doc_schema missing or not a string: %T %v", payload["doc_schema"], payload["doc_schema"])
	}
}

func TestMomStatusNoConstraintsDir(t *testing.T) {
	// mom_status must not fail when central constraints/ is absent.
	leoDir := newStatusTestDir(t)
	centralDir, err := centralvault.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(centralDir, "constraints")); err != nil {
		t.Fatal(err)
	}

	payload := callMomStatus(t, leoDir)

	constraints, ok := payload["constraints"].([]any)
	if !ok {
		t.Fatalf("constraints is not an array: %T", payload["constraints"])
	}
	// No constraints dir → empty array is fine.
	_ = constraints
}

func TestMomStatusNoSkillsDir(t *testing.T) {
	// mom_status must not fail when central skills/ is absent.
	leoDir := newStatusTestDir(t)
	centralDir, err := centralvault.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(centralDir, "skills")); err != nil {
		t.Fatal(err)
	}

	payload := callMomStatus(t, leoDir)

	skills, ok := payload["skills"].([]any)
	if !ok {
		t.Fatalf("skills is not an array: %T", payload["skills"])
	}
	_ = skills
}

func TestMomStatusTextContainsRequiredStrings(t *testing.T) {
	leoDir := newStatusTestDir(t)
	inW, outR, _ := runServer(t, leoDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)

	sendRequest(t, inW, "tools/call", 2, map[string]any{
		"name":      "mom_status",
		"arguments": map[string]any{},
	})
	resp := readResponse(t, outR)

	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)

	for _, want := range []string{"MOM", "Memory Oriented Machine", "continuous"} {
		if !strings.Contains(text, want) {
			t.Errorf("mom_status text does not contain %q", want)
		}
	}
}
