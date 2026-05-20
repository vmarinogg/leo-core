package mcp_test

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/momhq/mom/storage/canonical"

	"github.com/momhq/mom/ingress/mcp"
	"github.com/momhq/mom/storage/librarian"
)

func setCentralVault(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mom.db")
	t.Setenv("MOM_VAULT", path)
	return path
}

func newTestMomDir(t *testing.T) string {
	t.Helper()
	setCentralVault(t)
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	if err := os.MkdirAll(filepath.Join(momDir, "memory"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(momDir, "config.yaml"), []byte("scope: repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
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
	writeJSON(t, filepath.Join(momDir, "identity.json"), identity)
	return momDir
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func insertCentralMemory(t *testing.T, summary, text string, tags []string) string {
	t.Helper()
	lib, closeFn, err := canonical.OpenLibrarian()
	if err != nil {
		t.Fatalf("canonical.OpenLibrarian: %v", err)
	}
	defer func() { _ = closeFn() }()
	content, _ := json.Marshal(map[string]any{"text": text})
	id, err := lib.InsertMemoryWithTags(librarian.InsertMemory{
		Type:                   "semantic",
		Summary:                summary,
		Content:                string(content),
		SessionID:              "s-test",
		ProvenanceActor:        "test",
		ProvenanceSourceType:   "test-fixture",
		ProvenanceTriggerEvent: "test",
	}, tags)
	if err != nil {
		t.Fatalf("InsertMemoryWithTags: %v", err)
	}
	state := "curated"
	if err := lib.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &state}); err != nil {
		t.Fatalf("UpdateOperational curated: %v", err)
	}
	return id
}

func markLandmark(t *testing.T, id string, score float64) {
	t.Helper()
	lib, closeFn, err := canonical.OpenLibrarian()
	if err != nil {
		t.Fatalf("canonical.OpenLibrarian: %v", err)
	}
	defer func() { _ = closeFn() }()
	landmark := true
	if err := lib.UpdateOperational(id, librarian.OperationalUpdate{Landmark: &landmark, CentralityScore: &score}); err != nil {
		t.Fatalf("UpdateOperational landmark: %v", err)
	}
}

func sendRequest(t *testing.T, w io.Writer, method string, id any, params any) {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
}

func readResponse(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
		t.Fatalf("parsing response %q: %v", line, err)
	}
	return resp
}

func runServer(t *testing.T, momDir string) (inW io.WriteCloser, outR *bufio.Reader, done chan struct{}) {
	t.Helper()
	inR, inW := io.Pipe()
	outR2, outW := io.Pipe()

	srv := mcp.New(momDir)
	done = make(chan struct{})
	go func() {
		defer close(done)
		srv.Serve(inR, outW)
		outW.Close()
	}()
	outR = bufio.NewReader(outR2)
	return inW, outR, done
}

func TestInitializeHandshake(t *testing.T) {
	momDir := newTestMomDir(t)
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	resp := readResponse(t, outR)

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] == nil {
		t.Error("protocolVersion missing")
	}
	if result["instructions"] == nil {
		t.Error("instructions missing")
	}
}

func TestToolsListV030Surface(t *testing.T) {
	momDir := newTestMomDir(t)
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	sendRequest(t, inW, "tools/list", 2, nil)
	resp := readResponse(t, outR)

	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("tools/list returned %d tools, want 5", len(tools))
	}
	names := map[string]bool{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		names[name] = true
		if name == "mom_recall" {
			schema := tool["inputSchema"].(map[string]any)
			props := schema["properties"].(map[string]any)
			if _, ok := props["scope"]; ok {
				t.Fatal("mom_recall schema must not include scope")
			}
		}
	}
	for _, n := range []string{"mom_status", "mom_recall", "mom_get", "mom_landmarks", "mom_record"} {
		if !names[n] {
			t.Errorf("tool %q missing", n)
		}
	}
	for _, old := range []string{"get_memory", "list_scopes", "create_memory_draft", "list_landmarks", "mom_record_turn"} {
		if names[old] {
			t.Errorf("legacy tool %q still present", old)
		}
	}
}

func TestResourcesListUsesCentralVaultSurface(t *testing.T) {
	momDir := newTestMomDir(t)
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	sendRequest(t, inW, "resources/list", 2, nil)
	resp := readResponse(t, outR)

	result := resp["result"].(map[string]any)
	resources := result["resources"].([]any)
	uris := map[string]bool{}
	for _, raw := range resources {
		resource := raw.(map[string]any)
		uris[resource["uri"].(string)] = true
	}

	if !uris["mom://vault"] {
		t.Fatalf("resources/list must expose central vault status resource; got %v", uris)
	}
	for _, legacy := range []string{"mom://scopes", "mom://identity", "mom://constraints"} {
		if uris[legacy] {
			t.Fatalf("resources/list exposed obsolete scope-era resource %q", legacy)
		}
	}
}

func TestToolsCallMomRecallUsesCentralFinder(t *testing.T) {
	momDir := newTestMomDir(t)
	insertCentralMemory(t, "Authentication pattern for JWT", "Use JWT with RS256", []string{"auth", "security"})
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	sendRequest(t, inW, "tools/call", 2, map[string]any{"name": "mom_recall", "arguments": map[string]any{"query": "JWT"}})
	resp := readResponse(t, outR)

	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "Authentication pattern") {
		t.Fatalf("mom_recall did not return central memory: %s", text)
	}
}

func TestToolsCallMomRecallReturnsCompactIndex(t *testing.T) {
	momDir := newTestMomDir(t)
	insertCentralMemory(t, "Authentication pattern for JWT", strings.Repeat("Use JWT with RS256. ", 30), []string{"auth", "security"})
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	sendRequest(t, inW, "tools/call", 2, map[string]any{"name": "mom_recall", "arguments": map[string]any{"query": "JWT"}})
	resp := readResponse(t, outR)

	result := resp["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(text, "\n  ") {
		t.Fatalf("mom_recall should return compact JSON, got: %s", text)
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("mom_recall JSON parse failed: %v\n%s", err, text)
	}
	if len(rows) == 0 {
		t.Fatal("mom_recall returned no rows")
	}
	if _, ok := rows[0]["content"]; ok {
		t.Fatalf("mom_recall compact index must not include full content: %#v", rows[0])
	}
	if _, ok := rows[0]["Content"]; ok {
		t.Fatalf("mom_recall compact index must not include full Content: %#v", rows[0])
	}
	if rows[0]["summary"] == "" || rows[0]["snippet"] == "" || rows[0]["id"] == "" {
		t.Fatalf("mom_recall compact row missing id/summary/snippet: %#v", rows[0])
	}
	if len([]rune(rows[0]["snippet"].(string))) > 161 {
		t.Fatalf("snippet too long: %q", rows[0]["snippet"])
	}
}

func TestToolsCallMomRecallRequiresQuery(t *testing.T) {
	momDir := newTestMomDir(t)
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	sendRequest(t, inW, "tools/call", 2, map[string]any{"name": "mom_recall", "arguments": map[string]any{}})
	resp := readResponse(t, outR)

	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("mom_recall without query should return tool error: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "query is required") {
		t.Fatalf("error = %q, want query is required", text)
	}
}

func TestToolsCallMomGet(t *testing.T) {
	momDir := newTestMomDir(t)
	id := insertCentralMemory(t, "Test fact", "some detail", []string{"test"})
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	sendRequest(t, inW, "tools/call", 2, map[string]any{"name": "mom_get", "arguments": map[string]any{"id": id}})
	resp := readResponse(t, outR)

	result := resp["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, id) || !strings.Contains(text, "Test fact") {
		t.Fatalf("mom_get response missing memory: %s", text)
	}
}

func TestToolsCallMomGetRequiresID(t *testing.T) {
	momDir := newTestMomDir(t)
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	sendRequest(t, inW, "tools/call", 2, map[string]any{"name": "mom_get", "arguments": map[string]any{}})
	resp := readResponse(t, outR)

	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("mom_get without id should return tool error: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "id is required") {
		t.Fatalf("error = %q, want id is required", text)
	}
}

func TestToolsCallMomLandmarks(t *testing.T) {
	momDir := newTestMomDir(t)
	id := insertCentralMemory(t, "Key architecture", "central vault decision", []string{"arch"})
	markLandmark(t, id, 0.9)
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	sendRequest(t, inW, "tools/call", 2, map[string]any{"name": "mom_landmarks", "arguments": map[string]any{}})
	resp := readResponse(t, outR)

	result := resp["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, id) || !strings.Contains(text, "Key architecture") {
		t.Fatalf("mom_landmarks response missing landmark: %s", text)
	}
}

func TestUnknownMethodReturnsError(t *testing.T) {
	momDir := newTestMomDir(t)
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	sendRequest(t, inW, "unknown/method", 2, nil)
	resp := readResponse(t, outR)
	if resp["error"] == nil {
		t.Error("expected error for unknown method")
	}
}

func TestNotificationIgnored(t *testing.T) {
	momDir := newTestMomDir(t)
	inW, outR, _ := runServer(t, momDir)
	defer inW.Close()

	sendRequest(t, inW, "initialize", 1, map[string]any{"protocolVersion": "2024-11-05"})
	readResponse(t, outR)
	notif := map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}
	data, _ := json.Marshal(notif)
	data = append(data, '\n')
	_, _ = inW.Write(data)
	sendRequest(t, inW, "tools/list", 2, nil)
	resp := readResponse(t, outR)
	if id, ok := resp["id"].(float64); !ok || id != 2 {
		t.Errorf("expected response id=2, got %v", resp["id"])
	}
}
