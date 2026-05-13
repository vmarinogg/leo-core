package librarian_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/vault"
)

// openLib opens a fresh Vault with Librarian's migrations applied and
// returns a Librarian wrapping it. The Vault is closed via t.Cleanup.
func openLib(t *testing.T) *librarian.Librarian {
	t.Helper()
	dir := t.TempDir()
	v, err := vault.Open(filepath.Join(dir, "mom.db"), librarian.Migrations())
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return librarian.New(v)
}

func validInsert() librarian.InsertMemory {
	return librarian.InsertMemory{
		Content:                `{"text":"hello world"}`,
		SessionID:              "s-test-1",
		ProvenanceActor:        "claude-code",
		ProvenanceSourceType:   "transcript-extraction",
		ProvenanceTriggerEvent: "watcher",
	}
}

// ── Insert / Get ──────────────────────────────────────────────────────────────

func TestInsert_RoundTripWithDefaults(t *testing.T) {
	l := openLib(t)
	id, err := l.Insert(validInsert())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id == "" {
		t.Fatal("Insert returned empty id")
	}

	got, err := l.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.Type != "untyped" {
		t.Errorf("Type = %q, want untyped (default)", got.Type)
	}
	if got.PromotionState != "draft" {
		t.Errorf("PromotionState = %q, want draft (default)", got.PromotionState)
	}
	if got.Landmark {
		t.Error("Landmark should default to false")
	}
	if got.SessionID != "s-test-1" {
		t.Errorf("SessionID = %q, want s-test-1", got.SessionID)
	}
	if got.Content != `{"text":"hello world"}` {
		t.Errorf("Content = %q", got.Content)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should default to now, got zero time")
	}
}

func TestInsert_RejectsEmptySessionID(t *testing.T) {
	l := openLib(t)
	in := validInsert()
	in.SessionID = ""
	_, err := l.Insert(in)
	if !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestInsert_RejectsWhitespaceSessionID(t *testing.T) {
	l := openLib(t)
	in := validInsert()
	in.SessionID = "   "
	_, err := l.Insert(in)
	if !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestInsert_RejectsEmptyContent(t *testing.T) {
	l := openLib(t)
	in := validInsert()
	in.Content = ""
	_, err := l.Insert(in)
	if !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestInsert_RejectsInvalidJSONContent_BySchemaCheck(t *testing.T) {
	// API-level guard only checks non-empty; the schema CHECK
	// (json_valid(content)) is the second line of defense. Defense in
	// depth: the same constraint at two layers means a code path that
	// bypasses one still hits the other.
	l := openLib(t)
	in := validInsert()
	in.Content = "this is not json"
	_, err := l.Insert(in)
	if err == nil {
		t.Fatal("expected error for non-JSON content, got nil")
	}
	if !strings.Contains(err.Error(), "CHECK") && !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("expected CHECK/constraint error, got: %v", err)
	}
}

func TestInsert_MintsUUIDPerCall(t *testing.T) {
	l := openLib(t)
	a, err := l.Insert(validInsert())
	if err != nil {
		t.Fatalf("Insert a: %v", err)
	}
	b, err := l.Insert(validInsert())
	if err != nil {
		t.Fatalf("Insert b: %v", err)
	}
	if a == b {
		t.Fatal("Insert returned identical IDs for two calls")
	}
	// Sanity: UUID v4 string length is 36 (8-4-4-4-12 + hyphens).
	if len(a) != 36 {
		t.Errorf("id %q is not the expected uuid length", a)
	}
}

func TestInsert_HonoursExplicitType(t *testing.T) {
	l := openLib(t)
	in := validInsert()
	in.Type = "semantic"
	id, err := l.Insert(in)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, _ := l.Get(id)
	if got.Type != "semantic" {
		t.Errorf("Type = %q, want semantic", got.Type)
	}
}

func TestGet_ReturnsErrNotFoundOnMiss(t *testing.T) {
	l := openLib(t)
	_, err := l.Get("nonexistent-id")
	if !errors.Is(err, librarian.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ── UpdateOperational ─────────────────────────────────────────────────────────

func TestUpdateOperational_MutatesOperationalFieldsOnly(t *testing.T) {
	l := openLib(t)
	id, err := l.Insert(validInsert())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	newType := "procedural"
	newPromo := "curated"
	landmark := true
	score := 0.875
	if err := l.UpdateOperational(id, librarian.OperationalUpdate{
		Type:            &newType,
		PromotionState:  &newPromo,
		Landmark:        &landmark,
		CentralityScore: &score,
	}); err != nil {
		t.Fatalf("UpdateOperational: %v", err)
	}

	got, _ := l.Get(id)
	if got.Type != "procedural" {
		t.Errorf("Type = %q, want procedural", got.Type)
	}
	if got.PromotionState != "curated" {
		t.Errorf("PromotionState = %q, want curated", got.PromotionState)
	}
	if !got.Landmark {
		t.Error("Landmark = false, want true")
	}
	if !got.CentralityScore.Valid || got.CentralityScore.Float64 != 0.875 {
		t.Errorf("CentralityScore = %+v, want 0.875", got.CentralityScore)
	}

	// Substance fields must not have changed.
	if got.Content != `{"text":"hello world"}` {
		t.Errorf("Content was mutated: %q", got.Content)
	}
	if got.SessionID != "s-test-1" {
		t.Errorf("SessionID was mutated: %q", got.SessionID)
	}
	if got.ProvenanceActor != "claude-code" {
		t.Errorf("ProvenanceActor was mutated: %q", got.ProvenanceActor)
	}
}

func TestUpdateOperational_EmptyPatchIsNoopSuccess(t *testing.T) {
	l := openLib(t)
	id, _ := l.Insert(validInsert())
	if err := l.UpdateOperational(id, librarian.OperationalUpdate{}); err != nil {
		t.Fatalf("empty patch should be a no-op success, got: %v", err)
	}
}

func TestUpdateOperational_ReturnsNotFoundForUnknownID(t *testing.T) {
	l := openLib(t)
	t1 := "untyped"
	err := l.UpdateOperational("nope", librarian.OperationalUpdate{Type: &t1})
	if !errors.Is(err, librarian.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateOperational_RejectsEmptyID(t *testing.T) {
	l := openLib(t)
	t1 := "untyped"
	err := l.UpdateOperational("", librarian.OperationalUpdate{Type: &t1})
	if !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

// ── Substance immutability — locked by the API surface ────────────────────────

// TestSubstanceImmutability_OperationalUpdateShape uses reflect to
// enforce that OperationalUpdate has EXACTLY the four operational-
// mutable fields per ADR 0011 — Type, PromotionState, Landmark,
// CentralityScore. Adding a substance field (Content, SessionID,
// CreatedAt, Provenance*) to the struct then fails this test, even
// if no caller uses the new field. This is the actual contract guard
// — reflection over the type, not behaviour over an instance.
func TestSubstanceImmutability_OperationalUpdateShape(t *testing.T) {
	allowed := map[string]bool{
		"Type":            true,
		"PromotionState":  true,
		"Landmark":        true,
		"CentralityScore": true,
	}
	tt := reflect.TypeOf(librarian.OperationalUpdate{})
	if tt.Kind() != reflect.Struct {
		t.Fatalf("OperationalUpdate is %v, want struct", tt.Kind())
	}
	for i := 0; i < tt.NumField(); i++ {
		f := tt.Field(i)
		if !f.IsExported() {
			continue
		}
		if !allowed[f.Name] {
			t.Errorf("OperationalUpdate has unexpected field %q — substance field leaked into the patch type, violating ADR 0011", f.Name)
		}
		delete(allowed, f.Name)
	}
	for missing := range allowed {
		t.Errorf("OperationalUpdate is missing the operational field %q", missing)
	}
}

// TestSubstanceImmutability_RoundTripDoesNotMutateSubstance is the
// behavioural complement to the shape test above: even with every
// operational field exercised through UpdateOperational, the
// substance columns on the row stay byte-identical.
func TestSubstanceImmutability_RoundTripDoesNotMutateSubstance(t *testing.T) {
	l := openLib(t)
	id, _ := l.Insert(validInsert())
	got1, _ := l.Get(id)

	t1, st, lm, sc := "episodic", "curated", true, 1.0
	if err := l.UpdateOperational(id, librarian.OperationalUpdate{
		Type: &t1, PromotionState: &st, Landmark: &lm, CentralityScore: &sc,
	}); err != nil {
		t.Fatalf("UpdateOperational: %v", err)
	}

	got2, _ := l.Get(id)
	if got2.Content != got1.Content {
		t.Error("Content changed despite only operational update")
	}
	if got2.SessionID != got1.SessionID {
		t.Error("SessionID changed despite only operational update")
	}
	if got2.CreatedAt != got1.CreatedAt {
		t.Error("CreatedAt changed despite only operational update")
	}
	if got2.ProvenanceActor != got1.ProvenanceActor ||
		got2.ProvenanceSourceType != got1.ProvenanceSourceType ||
		got2.ProvenanceTriggerEvent != got1.ProvenanceTriggerEvent {
		t.Error("Provenance changed despite only operational update")
	}
}

// TestInsert_StampsProjectId verifies project_id round-trips through the
// memories table (per ADR 0016).
func TestInsert_StampsProjectId(t *testing.T) {
	l := openLib(t)
	m := validInsert()
	m.ProjectId = "pi-agents-cli"

	id, err := l.Insert(m)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := l.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ProjectId != "pi-agents-cli" {
		t.Errorf("ProjectId = %q, want pi-agents-cli", got.ProjectId)
	}
}

// TestSearchMemories_FiltersByProjectId verifies the project_id scope
// filter (ADR 0016).
func TestSearchMemories_FiltersByProjectId(t *testing.T) {
	l := openLib(t)
	a := validInsert(); a.ProjectId = "alpha"; a.SessionID = "s-a"
	b := validInsert(); b.ProjectId = "beta"; b.SessionID = "s-b"
	c := validInsert(); c.ProjectId = ""; c.SessionID = "s-c" // NULL
	idA, _ := l.Insert(a)
	idB, _ := l.Insert(b)
	idC, _ := l.Insert(c)

	got, err := l.SearchMemories(librarian.SearchFilter{ProjectId: "alpha"})
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if !ids[idA] {
		t.Errorf("expected alpha memory %s in results, got %v", idA, ids)
	}
	if ids[idB] {
		t.Errorf("did NOT expect beta memory %s when scoped to alpha", idB)
	}
	// NULL row included by default — ADR 0016 "legacy memories remain findable".
	if !ids[idC] {
		t.Errorf("expected NULL-project memory %s to be included by default", idC)
	}
}

// TestSearchMemories_StrictProjectExcludesNull confirms IncludeUnknownProject=false
// drops NULL rows from a scoped query (ADR 0016 --strict-project semantics).
func TestSearchMemories_StrictProjectExcludesNull(t *testing.T) {
	l := openLib(t)
	a := validInsert(); a.ProjectId = "alpha"; a.SessionID = "s-a"
	c := validInsert(); c.ProjectId = ""; c.SessionID = "s-c"
	idA, _ := l.Insert(a)
	idC, _ := l.Insert(c)

	got, err := l.SearchMemories(librarian.SearchFilter{
		ProjectId:             "alpha",
		StrictProject:        true,
	})
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if !ids[idA] {
		t.Errorf("expected alpha memory %s in strict results", idA)
	}
	if ids[idC] {
		t.Errorf("strict mode must exclude NULL memory %s", idC)
	}
}

// TestMigration_BackwardSafe_PreExistingRowsHaveNullProjectId verifies
// that memories inserted before the v6 migration survive it with
// project_id IS NULL (per ADR 0016 — legacy memories remain findable).
func TestMigration_BackwardSafe_PreExistingRowsHaveNullProjectId(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mom.db")

	// Step 1: open the vault with ONLY the v2 migration applied
	// (pre-ADR-0016 schema; no project_id column).
	all := librarian.Migrations()
	var preV6 []librarian.Migration
	for _, m := range all {
		if m.Version < 6 {
			preV6 = append(preV6, m)
		}
	}
	v, err := vault.Open(dbPath, preV6)
	if err != nil {
		t.Fatalf("vault.Open (pre-v6): %v", err)
	}
	// Insert via raw SQL using the pre-v6 column list so we are not
	// coupled to the current Librarian INSERT (which assumes v6+).
	id := "11111111-2222-3333-4444-555555555555"
	if err := v.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO memories (id, type, content, created_at, session_id)
			 VALUES (?, 'untyped', '{"text":"legacy memory"}', '2026-04-01T00:00:00.000000000Z', 's-legacy')`,
			id,
		)
		return err
	}); err != nil {
		t.Fatalf("raw insert pre-v6: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Step 2: reopen with the full migration set (includes v6).
	v2, err := vault.Open(dbPath, librarian.Migrations())
	if err != nil {
		t.Fatalf("vault.Open (post-v6): %v", err)
	}
	t.Cleanup(func() { _ = v2.Close() })
	l2 := librarian.New(v2)

	got, err := l2.Get(id)
	if err != nil {
		t.Fatalf("Get post-migration: %v", err)
	}
	if got.ProjectId != "" {
		t.Errorf("legacy memory must have empty ProjectId post-migration, got %q", got.ProjectId)
	}
	// And it should still be findable through scoped recall (NULL included by default).
	results, err := l2.SearchMemories(librarian.SearchFilter{ProjectId: "any-project"})
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("legacy memory %s should remain findable in scoped recall by default", id)
	}
}
