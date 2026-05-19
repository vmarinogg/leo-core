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

// ── ADR 0016: project-scoped recall ──────────────────────────────────────────

// insertProjectMemory mints a curated memory with the given project_id.
func insertProjectMemory(t *testing.T, lib *librarian.Librarian, summary, text, projectId string) string {
	t.Helper()
	content, _ := json.Marshal(map[string]any{"text": text})
	id, err := lib.InsertMemoryWithTags(librarian.InsertMemory{
		Type:                   "semantic",
		Summary:                summary,
		Content:                string(content),
		SessionID:              "s-cli-test",
		ProjectId:              projectId,
		ProvenanceActor:        "test",
		ProvenanceSourceType:   "test",
		ProvenanceTriggerEvent: "test",
	}, []string{"cli"})
	if err != nil {
		t.Fatalf("InsertMemoryWithTags: %v", err)
	}
	curated := "curated"
	if err := lib.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &curated}); err != nil {
		t.Fatalf("UpdateOperational: %v", err)
	}
	return id
}

// chdirToBoundDir creates a tempdir with .mom-project.yaml (id=projectId)
// and chdirs into it for the duration of the test.
func chdirToBoundDir(t *testing.T, projectId string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".mom-project.yaml"),
		[]byte("version: \"1\"\nid: "+projectId+"\n"), 0o644); err != nil {
		t.Fatalf("write bind file: %v", err)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}

func runRecallTest(t *testing.T, query string) string {
	t.Helper()
	buf := new(bytes.Buffer)
	recallCmd.SetOut(buf)
	recallCmd.SetErr(buf)
	if err := runRecall(recallCmd, []string{query}); err != nil {
		t.Fatalf("runRecall: %v\noutput:\n%s", err, buf.String())
	}
	return buf.String()
}

// Default scope: cwd's project_id filters; other project's memories absent.
func TestRecallCmd_DefaultScopesToCwdProject(t *testing.T) {
	resetRecallFlags()
	lib := openCentralTestLib(t)
	alphaID := insertProjectMemory(t, lib, "alpha memory", "deploy alpha service", "alpha")
	betaID := insertProjectMemory(t, lib, "beta memory", "deploy beta service", "beta")
	chdirToBoundDir(t, "alpha")

	out := runRecallTest(t, "deploy")
	if !strings.Contains(out, alphaID) {
		t.Errorf("expected alpha memory %s in output, got:\n%s", alphaID, out)
	}
	if strings.Contains(out, betaID) {
		t.Errorf("beta memory %s should be scoped out, got:\n%s", betaID, out)
	}
}

// --all disables scoping; both projects appear.
func TestRecallCmd_AllFlagDisablesScope(t *testing.T) {
	resetRecallFlags()
	lib := openCentralTestLib(t)
	alphaID := insertProjectMemory(t, lib, "alpha memory", "deploy alpha service", "alpha")
	betaID := insertProjectMemory(t, lib, "beta memory", "deploy beta service", "beta")
	chdirToBoundDir(t, "alpha")
	recallAllProjects = true

	out := runRecallTest(t, "deploy")
	if !strings.Contains(out, alphaID) {
		t.Errorf("--all should include alpha memory %s, got:\n%s", alphaID, out)
	}
	if !strings.Contains(out, betaID) {
		t.Errorf("--all should include beta memory %s, got:\n%s", betaID, out)
	}
}

// --project=foo overrides cwd resolution.
func TestRecallCmd_ProjectFlagOverridesCwd(t *testing.T) {
	resetRecallFlags()
	lib := openCentralTestLib(t)
	alphaID := insertProjectMemory(t, lib, "alpha memory", "deploy alpha service", "alpha")
	betaID := insertProjectMemory(t, lib, "beta memory", "deploy beta service", "beta")
	chdirToBoundDir(t, "alpha") // cwd says alpha
	recallProject = "beta"      // but the flag wins

	out := runRecallTest(t, "deploy")
	if strings.Contains(out, alphaID) {
		t.Errorf("--project=beta should exclude alpha %s, got:\n%s", alphaID, out)
	}
	if !strings.Contains(out, betaID) {
		t.Errorf("--project=beta should include beta %s, got:\n%s", betaID, out)
	}
}

// Unbound cwd → falls through to all-projects + prints stderr hint
// mentioning /mom-project.
func TestRecallCmd_UnboundCwdFallsThroughWithHint(t *testing.T) {
	resetRecallFlags()
	lib := openCentralTestLib(t)
	alphaID := insertProjectMemory(t, lib, "alpha memory", "deploy alpha", "alpha")

	dir := t.TempDir() // no bind file
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	out := runRecallTest(t, "deploy")
	if !strings.Contains(out, alphaID) {
		t.Errorf("unbound cwd should fall through and include alpha %s, got:\n%s", alphaID, out)
	}
	if !strings.Contains(out, "/mom-project") {
		t.Errorf("expected stderr hint mentioning /mom-project, got:\n%s", out)
	}
}

// --strict-project excludes NULL project_id rows.
func TestRecallCmd_StrictProjectExcludesNull(t *testing.T) {
	resetRecallFlags()
	lib := openCentralTestLib(t)
	alphaID := insertProjectMemory(t, lib, "alpha memory", "deploy alpha", "alpha")
	legacyID := insertProjectMemory(t, lib, "legacy memory", "deploy legacy", "")
	chdirToBoundDir(t, "alpha")
	recallStrictProject = true

	out := runRecallTest(t, "deploy")
	if !strings.Contains(out, alphaID) {
		t.Errorf("strict should include alpha %s, got:\n%s", alphaID, out)
	}
	if strings.Contains(out, legacyID) {
		t.Errorf("strict should exclude NULL %s, got:\n%s", legacyID, out)
	}
}

// Cycle 7: When cwd has no .mom-project.yaml ancestor, status surfaces
// a one-line nudge pointing at /mom-project (per ADR 0016 Q5).
func TestStatusCmd_NudgesWhenCwdUnbound(t *testing.T) {
	openCentralTestLib(t)

	// Chdir to an unbound dir.
	unbound := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(unbound); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	buf := new(bytes.Buffer)
	statusCmd.SetOut(buf)
	statusCmd.SetErr(buf)
	if err := runStatus(statusCmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "/mom-project") {
		t.Errorf("expected nudge mentioning /mom-project for unbound cwd, got:\n%s", out)
	}
}

// Cycle 8: When cwd is bound, status reports the project id and stays quiet.
func TestStatusCmd_ShowsProjectWhenBound(t *testing.T) {
	openCentralTestLib(t)

	bound := t.TempDir()
	if err := os.WriteFile(filepath.Join(bound, ".mom-project.yaml"),
		[]byte("version: \"1\"\nid: alpha\n"), 0o644); err != nil {
		t.Fatalf("write bind file: %v", err)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(bound); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	buf := new(bytes.Buffer)
	statusCmd.SetOut(buf)
	statusCmd.SetErr(buf)
	if err := runStatus(statusCmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected bound project id in output, got:\n%s", out)
	}
	if strings.Contains(out, "/mom-project") {
		t.Errorf("bound cwd should NOT trigger the nudge, got:\n%s", out)
	}
}

// ── #345 drafts filters (project / harness / session) ──

// insertDraftFull inserts a draft with full provenance + project_id
// control for the drafts-filter tests.
func insertDraftFull(t *testing.T, lib *librarian.Librarian, summary, harness, sessionID, projectID string) string {
	t.Helper()
	content, _ := json.Marshal(map[string]any{"text": "draft body for " + summary})
	id, err := lib.InsertMemoryWithTags(librarian.InsertMemory{
		Type:                   "untyped",
		Summary:                summary,
		Content:                string(content),
		SessionID:              sessionID,
		ProjectId:              projectID,
		ProvenanceActor:        harness,
		ProvenanceSourceType:   "transcript-extraction",
		ProvenanceTriggerEvent: "watcher",
	}, []string{"cli"})
	if err != nil {
		t.Fatalf("InsertMemoryWithTags: %v", err)
	}
	return id
}

func runDraftsTest(t *testing.T) string {
	t.Helper()
	buf := new(bytes.Buffer)
	draftsCmd.SetOut(buf)
	draftsCmd.SetErr(buf)
	if err := runDrafts(draftsCmd, nil); err != nil {
		t.Fatalf("runDrafts: %v\noutput:\n%s", err, buf.String())
	}
	return buf.String()
}

// Cycle 5: --project foo scopes to the named project.
func TestDraftsCmd_ProjectFlagScopes(t *testing.T) {
	resetDraftsFlags()
	lib := openCentralTestLib(t)
	alphaID := insertDraftFull(t, lib, "alpha draft", "claude-code", "s-1", "alpha")
	betaID := insertDraftFull(t, lib, "beta draft", "claude-code", "s-2", "beta")
	draftsProject = "alpha"

	out := runDraftsTest(t)
	if !strings.Contains(out, alphaID) {
		t.Errorf("expected alpha %s in output, got:\n%s", alphaID, out)
	}
	if strings.Contains(out, betaID) {
		t.Errorf("beta %s should be excluded, got:\n%s", betaID, out)
	}
}

// Cycle 6: --all-projects returns everything regardless of cwd.
func TestDraftsCmd_AllProjectsFlag(t *testing.T) {
	resetDraftsFlags()
	lib := openCentralTestLib(t)
	alphaID := insertDraftFull(t, lib, "alpha draft", "claude-code", "s-1", "alpha")
	betaID := insertDraftFull(t, lib, "beta draft", "claude-code", "s-2", "beta")
	chdirToBoundDir(t, "alpha")
	draftsAllProjects = true

	out := runDraftsTest(t)
	if !strings.Contains(out, alphaID) || !strings.Contains(out, betaID) {
		t.Errorf("--all-projects should include both, got:\n%s", out)
	}
}

// Cycle 7: --harness codex filters by provenance_actor.
func TestDraftsCmd_HarnessFlag(t *testing.T) {
	resetDraftsFlags()
	lib := openCentralTestLib(t)
	codexID := insertDraftFull(t, lib, "codex draft", "codex", "s-c", "alpha")
	claudeID := insertDraftFull(t, lib, "claude draft", "claude-code", "s-cl", "alpha")
	draftsAllProjects = true // skip project filter
	draftsHarness = "codex"

	out := runDraftsTest(t)
	if !strings.Contains(out, codexID) {
		t.Errorf("expected codex draft %s", codexID)
	}
	if strings.Contains(out, claudeID) {
		t.Errorf("claude draft %s should be excluded when --harness=codex", claudeID)
	}
}

// Cycle 8: --session filters by exact session id.
func TestDraftsCmd_SessionFlag(t *testing.T) {
	resetDraftsFlags()
	lib := openCentralTestLib(t)
	wantID := insertDraftFull(t, lib, "target session", "claude-code", "s-target", "alpha")
	otherID := insertDraftFull(t, lib, "other session", "claude-code", "s-other", "alpha")
	draftsAllProjects = true
	draftsSession = "s-target"

	out := runDraftsTest(t)
	if !strings.Contains(out, wantID) {
		t.Errorf("expected target draft %s", wantID)
	}
	if strings.Contains(out, otherID) {
		t.Errorf("other-session draft %s should be excluded", otherID)
	}
}

// Cycle 9: default behaviour scopes to cwd-resolved project.
func TestDraftsCmd_DefaultScopesToCwdProject(t *testing.T) {
	resetDraftsFlags()
	lib := openCentralTestLib(t)
	alphaID := insertDraftFull(t, lib, "alpha draft", "claude-code", "s-1", "alpha")
	betaID := insertDraftFull(t, lib, "beta draft", "claude-code", "s-2", "beta")
	chdirToBoundDir(t, "alpha")

	out := runDraftsTest(t)
	if !strings.Contains(out, alphaID) {
		t.Errorf("expected alpha %s in cwd-scoped output", alphaID)
	}
	if strings.Contains(out, betaID) {
		t.Errorf("beta %s should be excluded when cwd is bound to alpha", betaID)
	}
}

// Cycle 10: unbound cwd → falls through to all + stderr hint.
func TestDraftsCmd_UnboundCwdFallsThroughWithHint(t *testing.T) {
	resetDraftsFlags()
	lib := openCentralTestLib(t)
	alphaID := insertDraftFull(t, lib, "alpha draft", "claude-code", "s-1", "alpha")

	unbound := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(unbound); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	out := runDraftsTest(t)
	if !strings.Contains(out, alphaID) {
		t.Errorf("unbound cwd should fall through and include alpha %s, got:\n%s", alphaID, out)
	}
	if !strings.Contains(out, "/mom-project") {
		t.Errorf("expected stderr hint mentioning /mom-project, got:\n%s", out)
	}
}

// Cycle 11: output shows Harness + Project columns.
func TestDraftsCmd_OutputColumns(t *testing.T) {
	resetDraftsFlags()
	lib := openCentralTestLib(t)
	insertDraftFull(t, lib, "alpha draft", "codex", "s-1", "alpha")
	draftsAllProjects = true

	out := runDraftsTest(t)
	if !strings.Contains(out, "Harness") {
		t.Errorf("expected Harness column header, got:\n%s", out)
	}
	if !strings.Contains(out, "Project") {
		t.Errorf("expected Project column header, got:\n%s", out)
	}
	if !strings.Contains(out, "codex") {
		t.Errorf("expected codex value in row, got:\n%s", out)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected alpha value in row, got:\n%s", out)
	}
}
