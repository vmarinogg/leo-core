package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
)

func openV030TestLib(t *testing.T) *librarian.Librarian {
	t.Helper()
	t.Setenv("MOM_VAULT", filepath.Join(t.TempDir(), "mom.db"))
	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		t.Fatalf("centralvault.OpenLibrarian: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })
	return lib
}

func insertV030Memory(t *testing.T, lib *librarian.Librarian, summary, text string) string {
	t.Helper()
	content, _ := json.Marshal(map[string]any{"text": text})
	id, err := lib.InsertMemoryWithTags(librarian.InsertMemory{
		Type:                   "semantic",
		Summary:                summary,
		Content:                string(content),
		SessionID:              "s-cli-test",
		ProvenanceActor:        "test",
		ProvenanceSourceType:   "test",
		ProvenanceTriggerEvent: "test",
	}, []string{"cli"})
	if err != nil {
		t.Fatalf("InsertMemoryWithTags: %v", err)
	}
	return id
}

func TestRecallCmd_NaturalQueryUsesCentralFinder(t *testing.T) {
	lib := openV030TestLib(t)
	id := insertV030Memory(t, lib, "AWS deployment flow", "deploy Lambda through canary")
	state := "curated"
	if err := lib.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &state}); err != nil {
		t.Fatalf("UpdateOperational: %v", err)
	}

	buf := new(bytes.Buffer)
	recallCmd.SetOut(buf)
	recallCmd.SetErr(buf)
	if err := runRecall(recallCmd, []string{"Lambda canary"}); err != nil {
		t.Fatalf("runRecall: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, id) || !strings.Contains(out, "AWS deployment flow") {
		t.Fatalf("recall output missing memory:\n%s", out)
	}
}

func TestRecallCmd_SQLReadOnlyQuery(t *testing.T) {
	lib := openV030TestLib(t)
	id := insertV030Memory(t, lib, "SQL visible memory", "query me")

	buf := new(bytes.Buffer)
	recallCmd.SetOut(buf)
	recallCmd.SetErr(buf)
	if err := runRecall(recallCmd, []string{"SELECT id, summary FROM memories WHERE id = '" + id + "'"}); err != nil {
		t.Fatalf("runRecall SQL: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, id) || !strings.Contains(out, "SQL visible memory") {
		t.Fatalf("SQL output missing memory:\n%s", out)
	}
}

func TestRecallCmd_SQLRejectsMutation(t *testing.T) {
	_ = openV030TestLib(t)
	buf := new(bytes.Buffer)
	recallCmd.SetOut(buf)
	recallCmd.SetErr(buf)
	err := runRecall(recallCmd, []string{"DELETE FROM memories"})
	if err == nil {
		t.Fatal("expected mutation SQL rejection")
	}
	if !strings.Contains(err.Error(), "read-only") && !strings.Contains(err.Error(), "SELECT") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStatusCmd_V030Shape(t *testing.T) {
	lib := openV030TestLib(t)
	insertV030Memory(t, lib, "Status memory", "status text")

	buf := new(bytes.Buffer)
	statusCmd.SetOut(buf)
	statusCmd.SetErr(buf)
	if err := runStatus(statusCmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"MOM v0.30", "vault", "memories", "types", "landmarks", "op events", "constraints", "skills"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestPromoteCmd_FlipsDraftToCurated(t *testing.T) {
	lib := openV030TestLib(t)
	id := insertV030Memory(t, lib, "Promote memory", "promote me")

	buf := new(bytes.Buffer)
	promoteCmd.SetOut(buf)
	promoteCmd.SetErr(buf)
	if err := runPromote(promoteCmd, []string{id}); err != nil {
		t.Fatalf("runPromote: %v", err)
	}
	mem, err := lib.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.PromotionState != "curated" {
		t.Fatalf("promotion_state = %q, want curated", mem.PromotionState)
	}
	if !strings.Contains(buf.String(), "promoted") {
		t.Fatalf("promote output missing success: %s", buf.String())
	}
}

func TestPromoteCmd_AlreadyCuratedErrors(t *testing.T) {
	lib := openV030TestLib(t)
	id := insertV030Memory(t, lib, "Already curated", "done")
	state := "curated"
	if err := lib.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &state}); err != nil {
		t.Fatalf("UpdateOperational: %v", err)
	}
	err := runPromote(promoteCmd, []string{id})
	if err == nil {
		t.Fatal("expected already curated error")
	}
	if !strings.Contains(err.Error(), "already curated") {
		t.Fatalf("unexpected error: %v", err)
	}
}
