package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
)

func openCentralTestLib(t *testing.T) *librarian.Librarian {
	t.Helper()
	t.Setenv("MOM_VAULT", filepath.Join(t.TempDir(), "mom.db"))
	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		t.Fatalf("centralvault.OpenLibrarian: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })
	return lib
}

func insertCentralTestMemory(t *testing.T, lib *librarian.Librarian, summary, text string) string {
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
	lib := openCentralTestLib(t)
	id := insertCentralTestMemory(t, lib, "AWS deployment flow", "deploy Lambda through canary")
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
	lib := openCentralTestLib(t)
	id := insertCentralTestMemory(t, lib, "SQL visible memory", "query me")

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
	_ = openCentralTestLib(t)
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

func TestStatusCmd_CentralShape(t *testing.T) {
	lib := openCentralTestLib(t)
	insertCentralTestMemory(t, lib, "Status memory", "status text")

	buf := new(bytes.Buffer)
	statusCmd.SetOut(buf)
	statusCmd.SetErr(buf)
	if err := runStatus(statusCmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"MOM", "cwd", "vault", "memories", "types", "landmarks", "op events", "recording", "watcher"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"constraints", "skills"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("status output should not include %q:\n%s", forbidden, out)
		}
	}
}

func insertCentralDraftAt(t *testing.T, lib *librarian.Librarian, createdAt time.Time, summary, text string) string {
	t.Helper()
	content, _ := json.Marshal(map[string]any{"text": text})
	id, err := lib.InsertMemoryWithTags(librarian.InsertMemory{
		Type:                   "untyped",
		Summary:                summary,
		Content:                string(content),
		CreatedAt:              createdAt,
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

func TestDraftsCmd_ListsRecentDraftsWithSummaryOrExcerpt(t *testing.T) {
	lib := openCentralTestLib(t)
	now := time.Now().UTC()
	recent := insertCentralDraftAt(t, lib, now.Add(-2*time.Hour), "", "this draft should appear with a content excerpt")
	withSummary := insertCentralDraftAt(t, lib, now.Add(-3*time.Hour), "Existing draft summary", "summary wins over content")
	old := insertCentralDraftAt(t, lib, now.Add(-25*time.Hour), "old draft", "this draft should be hidden")

	buf := new(bytes.Buffer)
	draftsCmd.SetOut(buf)
	draftsCmd.SetErr(buf)
	draftsSince = 24 * time.Hour
	t.Cleanup(func() { draftsSince = 24 * time.Hour })
	if err := runDrafts(draftsCmd, nil); err != nil {
		t.Fatalf("runDrafts: %v", err)
	}
	out := buf.String()
	for _, want := range []string{recent, withSummary, "this draft should appear", "Existing draft summary"} {
		if !strings.Contains(out, want) {
			t.Fatalf("drafts output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, old) || strings.Contains(out, "this draft should be hidden") {
		t.Fatalf("drafts output included old draft:\n%s", out)
	}
}

func TestDraftsCmd_UsesSinceWindow(t *testing.T) {
	lib := openCentralTestLib(t)
	now := time.Now().UTC()
	recent := insertCentralDraftAt(t, lib, now.Add(-30*time.Minute), "recent", "recent draft")
	older := insertCentralDraftAt(t, lib, now.Add(-2*time.Hour), "older", "older draft")

	buf := new(bytes.Buffer)
	draftsCmd.SetOut(buf)
	draftsCmd.SetErr(buf)
	draftsSince = time.Hour
	t.Cleanup(func() { draftsSince = 24 * time.Hour })
	if err := runDrafts(draftsCmd, nil); err != nil {
		t.Fatalf("runDrafts: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, recent) {
		t.Fatalf("drafts output missing recent draft:\n%s", out)
	}
	if strings.Contains(out, older) {
		t.Fatalf("drafts output included older draft:\n%s", out)
	}
}

func TestCurateCmd_CuratesDraftWithTypeAndSummary(t *testing.T) {
	lib := openCentralTestLib(t)
	createdAt := time.Now().UTC().Add(-time.Minute)
	id := insertCentralDraftAt(t, lib, createdAt, "", "curate me")
	before, err := lib.Get(id)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}

	buf := new(bytes.Buffer)
	curateCmd.SetOut(buf)
	curateCmd.SetErr(buf)
	curateType = "procedural"
	curateSummary = "User chose canonical curation flow"
	t.Cleanup(func() {
		curateType = ""
		curateSummary = ""
	})
	if err := runCurate(curateCmd, []string{id}); err != nil {
		t.Fatalf("runCurate: %v", err)
	}
	mem, err := lib.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.PromotionState != "curated" {
		t.Fatalf("promotion_state = %q, want curated", mem.PromotionState)
	}
	if mem.Type != "procedural" {
		t.Fatalf("type = %q, want procedural", mem.Type)
	}
	if mem.Summary != "User chose canonical curation flow" {
		t.Fatalf("summary = %q", mem.Summary)
	}
	if mem.Content != before.Content || mem.SessionID != before.SessionID || mem.ProvenanceActor != before.ProvenanceActor || mem.CreatedAt != before.CreatedAt {
		t.Fatalf("curation changed immutable substance: before=%+v after=%+v", before, mem)
	}
	if !strings.Contains(buf.String(), "curated") {
		t.Fatalf("curate output missing success: %s", buf.String())
	}
}

func TestCurateCmd_AcceptsCuratedMemoryTypes(t *testing.T) {
	for _, typ := range []string{"semantic", "procedural", "episodic"} {
		t.Run(typ, func(t *testing.T) {
			lib := openCentralTestLib(t)
			id := insertCentralDraftAt(t, lib, time.Now().UTC(), "", "curate type")

			curateType = typ
			curateSummary = "Typed curated memory"
			t.Cleanup(func() {
				curateType = ""
				curateSummary = ""
			})
			if err := runCurate(curateCmd, []string{id}); err != nil {
				t.Fatalf("runCurate: %v", err)
			}
			mem, err := lib.Get(id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if mem.Type != typ || mem.PromotionState != "curated" {
				t.Fatalf("type=%q state=%q", mem.Type, mem.PromotionState)
			}
		})
	}
}

func TestCurateCmd_RejectsMissingType(t *testing.T) {
	lib := openCentralTestLib(t)
	id := insertCentralDraftAt(t, lib, time.Now().UTC(), "", "keep draft unchanged")

	curateType = ""
	curateSummary = "Missing type should fail"
	t.Cleanup(func() {
		curateType = ""
		curateSummary = ""
	})
	err := runCurate(curateCmd, []string{id})
	if err == nil {
		t.Fatal("expected missing type error")
	}
	mem, err := lib.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.PromotionState != "draft" || mem.Type != "untyped" || mem.Summary != "" {
		t.Fatalf("memory changed after failed curation: state=%q type=%q summary=%q", mem.PromotionState, mem.Type, mem.Summary)
	}
}

func TestCurateCmd_RejectsPartialCuration(t *testing.T) {
	lib := openCentralTestLib(t)
	id := insertCentralDraftAt(t, lib, time.Now().UTC(), "", "keep draft unchanged")

	curateType = "semantic"
	curateSummary = ""
	t.Cleanup(func() {
		curateType = ""
		curateSummary = ""
	})
	err := runCurate(curateCmd, []string{id})
	if err == nil {
		t.Fatal("expected missing summary error")
	}
	mem, err := lib.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.PromotionState != "draft" || mem.Type != "untyped" || mem.Summary != "" {
		t.Fatalf("memory changed after failed curation: state=%q type=%q summary=%q", mem.PromotionState, mem.Type, mem.Summary)
	}
}

func TestCurateCmd_RejectsUntypedCuration(t *testing.T) {
	lib := openCentralTestLib(t)
	id := insertCentralDraftAt(t, lib, time.Now().UTC(), "", "keep untyped out of curation")

	curateType = "untyped"
	curateSummary = "Untyped curation should fail"
	t.Cleanup(func() {
		curateType = ""
		curateSummary = ""
	})
	err := runCurate(curateCmd, []string{id})
	if err == nil {
		t.Fatal("expected untyped curation rejection")
	}
	mem, err := lib.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.PromotionState != "draft" || mem.Summary != "" {
		t.Fatalf("memory changed after failed curation: state=%q summary=%q", mem.PromotionState, mem.Summary)
	}
}

func TestCurateCmd_AlreadyCuratedErrors(t *testing.T) {
	lib := openCentralTestLib(t)
	id := insertCentralTestMemory(t, lib, "Already curated", "done")
	state := "curated"
	if err := lib.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &state}); err != nil {
		t.Fatalf("UpdateOperational: %v", err)
	}
	curateType = "semantic"
	curateSummary = "Already curated summary"
	t.Cleanup(func() {
		curateType = ""
		curateSummary = ""
	})
	err := runCurate(curateCmd, []string{id})
	if err == nil {
		t.Fatal("expected already curated error")
	}
	if !strings.Contains(err.Error(), "already curated") {
		t.Fatalf("unexpected error: %v", err)
	}
}
