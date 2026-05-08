package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newStatusTestDir creates a temp .mom/ dir with config set up for mom_status tests.
func newStatusTestDir(t *testing.T) string {
	t.Helper()
	setCentralVault(t)
	dir := t.TempDir()
	leoDir := filepath.Join(dir, ".mom")

	if err := os.MkdirAll(filepath.Join(leoDir, "memory"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := `version: "1"
scope: repo
harnesses:
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

	insertCentralMemory(t, "A test memory", "detail x", []string{"test"})
	return leoDir
}

// callMomStatus sends the mom_status tool call and returns the parsed JSON payload.
func callMomStatusText(t *testing.T, leoDir string) string {
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
	return text
}

func TestMomStatusCompactShape(t *testing.T) {
	leoDir := newStatusTestDir(t)
	text := callMomStatusText(t, leoDir)
	if strings.Contains(text, "\n  ") {
		t.Fatalf("mom_status should return compact JSON, got: %s", text)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("parsing mom_status text as JSON: %v\ntext: %s", err, text)
	}

	for _, key := range []string{"identity", "health", "routing", "session", "vault_state", "skills"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("missing top-level key %q in mom_status response", key)
		}
	}
	for _, key := range []string{"operating_rules", "boundaries", "tools", "constraints", "modes", "doc_schema"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("mom_status should not include legacy key %q", key)
		}
	}

	identity, ok := payload["identity"].(map[string]any)
	if !ok {
		t.Fatalf("identity is not a map: %T", payload["identity"])
	}
	if identity["name"] != "MOM" {
		t.Fatalf("identity.name = %q; want MOM", identity["name"])
	}
	if _, ok := identity["tagline"].(string); !ok {
		t.Fatal("identity.tagline missing")
	}
	for _, field := range []string{"role", "voice", "stance"} {
		if _, ok := identity[field]; ok {
			t.Fatalf("identity should not include %q", field)
		}
	}

	routing, ok := payload["routing"].(map[string]any)
	if !ok {
		t.Fatalf("routing is not a map: %T", payload["routing"])
	}
	joinedRouting := strings.ToLower(strings.Join([]string{
		fmtString(routing["preferred"]),
		fmtString(routing["mcp"]),
	}, " "))
	if !strings.Contains(joinedRouting, "cli") || !strings.Contains(joinedRouting, "skills") || !strings.Contains(joinedRouting, "fallback") {
		t.Fatalf("routing should prefer skills/CLI and name MCP fallback: %#v", routing)
	}

	session, ok := payload["session"].(map[string]any)
	if !ok {
		t.Fatalf("session is not a map: %T", payload["session"])
	}
	if cwd, _ := session["cwd"].(string); cwd == "" {
		t.Fatal("session.cwd missing")
	}

	vs, ok := payload["vault_state"].(map[string]any)
	if !ok {
		t.Fatalf("vault_state is not a map: %T", payload["vault_state"])
	}
	for _, key := range []string{"path", "total_memories", "landmarks", "record_mode", "watcher"} {
		if _, ok := vs[key]; !ok {
			t.Fatalf("vault_state.%s missing", key)
		}
	}
	if vs["record_mode"] != "continuous" {
		t.Fatalf("vault_state.record_mode = %v; want continuous", vs["record_mode"])
	}

	skills, ok := payload["skills"].([]any)
	if !ok {
		t.Fatalf("skills is not an array: %T", payload["skills"])
	}
	seen := map[string]bool{}
	for _, raw := range skills {
		s, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("skill item not a map: %T", raw)
		}
		name, _ := s["command"].(string)
		seen[name] = true
	}
	for _, want := range []string{"/mom-status", "/mom-recall", "/mom-wrap-up"} {
		if !seen[want] {
			t.Fatalf("mom_status skills missing %s: %#v", want, skills)
		}
	}

	payloadText, _ := json.Marshal(payload)
	legacyWriteTool := "mom" + "_" + "record"
	if strings.Contains(string(payloadText), legacyWriteTool) {
		t.Fatalf("mom_status should not mention %s", legacyWriteTool)
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
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
