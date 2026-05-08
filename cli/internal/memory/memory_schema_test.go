package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// minimalDocJSON is a memory JSON with only required fields.
const minimalDocJSON = `{
	"id": "minimal-doc",
	"scope": "project",
	"tags": ["test"],
	"created": "2026-04-13T00:00:00Z",
	"created_by": "owner",
	"content": {"fact": "minimal memory without optional fields"}
}`

// fullDocJSON is a memory JSON with all optional fields populated.
const fullDocJSON = `{
	"id": "full-doc",
	"scope": "project",
	"tags": ["test"],
	"created": "2026-04-13T00:00:00Z",
	"created_by": "owner",
	"valid_to": "2027-04-13T00:00:00Z",
	"promotion_state": "curated",
	"classification": "INTERNAL",
	"compartments": {"project": ["alpha", "beta"], "department": ["engineering"]},
	"provenance": {
		"runtime": "claude-code",
		"trigger_event": "session.end",
		"commit_sha": "deadbeef",
		"raw_exhaust_ref": ".mom/raw/2026-04-13.jsonl"
	},
	"landmark": true,
	"centrality_score": 0.85,
	"content": {"fact": "full memory with all fields"}
}`

// TestMinimalDoc_LoadFillsDefaults verifies that a minimal memory file
// loads without error and gets safe defaults applied.
func TestMinimalDoc_LoadFillsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "minimal-doc.json")
	if err := os.WriteFile(path, []byte(minimalDocJSON), 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	doc, err := LoadDoc(path)
	if err != nil {
		t.Fatalf("LoadDoc failed: %v", err)
	}

	if doc.PromotionState != "draft" {
		t.Errorf("expected default promotion_state draft, got %q", doc.PromotionState)
	}
	if doc.Classification != "INTERNAL" {
		t.Errorf("expected default classification INTERNAL, got %q", doc.Classification)
	}
	if doc.Compartments == nil {
		t.Error("expected compartments to be non-nil (empty map), got nil")
	}
	if len(doc.Compartments) != 0 {
		t.Errorf("expected empty compartments, got %v", doc.Compartments)
	}
	if doc.Provenance == nil {
		t.Error("expected provenance to be non-nil empty struct, got nil")
	}
	if doc.Landmark {
		t.Error("expected landmark to be false by default")
	}
	if doc.CentralityScore != nil {
		t.Errorf("expected centrality_score nil by default, got %v", doc.CentralityScore)
	}
	if doc.ValidTo != nil {
		t.Errorf("expected valid_to nil by default, got %v", doc.ValidTo)
	}
}

// TestMinimalDoc_ValidatesCleanly confirms validation passes for minimal docs
// after defaults are applied.
func TestMinimalDoc_ValidatesCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "minimal-doc.json")
	if err := os.WriteFile(path, []byte(minimalDocJSON), 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	doc, err := LoadDoc(path)
	if err != nil {
		t.Fatalf("LoadDoc failed: %v", err)
	}
	if err := doc.Validate(); err != nil {
		t.Errorf("minimal doc validation failed: %v", err)
	}
}

// TestFullDoc_RoundTrip verifies that a fully-populated doc
// survives save → load → save without field mutation.
func TestFullDoc_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "full-doc.json")
	if err := os.WriteFile(path, []byte(fullDocJSON), 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	// First load.
	doc, err := LoadDoc(path)
	if err != nil {
		t.Fatalf("first LoadDoc failed: %v", err)
	}

	// Persist to a second path.
	path2 := filepath.Join(dir, "round-trip.json")
	if err := SaveDoc(path2, doc); err != nil {
		t.Fatalf("SaveDoc failed: %v", err)
	}

	// Reload from the saved copy.
	doc2, err := LoadDoc(path2)
	if err != nil {
		t.Fatalf("second LoadDoc failed: %v", err)
	}

	if doc2.PromotionState != "curated" {
		t.Errorf("promotion_state mismatch: got %q", doc2.PromotionState)
	}
	if doc2.Classification != "INTERNAL" {
		t.Errorf("classification mismatch: got %q", doc2.Classification)
	}
	if len(doc2.Compartments["project"]) != 2 {
		t.Errorf("compartments[project] mismatch: got %v", doc2.Compartments["project"])
	}
	if doc2.Provenance == nil || doc2.Provenance.Runtime != "claude-code" {
		t.Errorf("provenance.runtime mismatch: got %v", doc2.Provenance)
	}
	if doc2.Provenance.RawExhaustRef != ".mom/raw/2026-04-13.jsonl" {
		t.Errorf("provenance.raw_exhaust_ref mismatch: got %q", doc2.Provenance.RawExhaustRef)
	}
	if !doc2.Landmark {
		t.Error("landmark should be true")
	}
	if doc2.CentralityScore == nil || *doc2.CentralityScore != 0.85 {
		t.Errorf("centrality_score mismatch: got %v", doc2.CentralityScore)
	}
	if doc2.ValidTo == nil {
		t.Error("valid_to should not be nil")
	}
}

// TestValidate_InvalidClassification rejects unknown classification values.
func TestValidate_InvalidClassification(t *testing.T) {
	for _, bad := range []string{"SUPER_SECRET", "SECRET", "TOP_SECRET", "public"} {
		t.Run(bad, func(t *testing.T) {
			doc := docWithDefaults()
			doc.Classification = bad
			if err := doc.Validate(); err == nil {
				t.Errorf("expected error for invalid classification %q, got nil", bad)
			}
		})
	}
}

// TestValidate_ValidClassificationValues accepts all three valid values.
func TestValidate_ValidClassificationValues(t *testing.T) {
	for _, c := range []string{"PUBLIC", "INTERNAL", "CONFIDENTIAL"} {
		t.Run(c, func(t *testing.T) {
			doc := docWithDefaults()
			doc.Classification = c
			if err := doc.Validate(); err != nil {
				t.Errorf("expected valid for classification %q, got: %v", c, err)
			}
		})
	}
}

// TestValidate_InvalidPromotionState rejects unknown promotion states.
func TestValidate_InvalidPromotionState(t *testing.T) {
	doc := docWithDefaults()
	doc.PromotionState = "active" // not in enum
	if err := doc.Validate(); err == nil {
		t.Fatal("expected error for invalid promotion_state, got nil")
	}
}

// TestValidate_ValidPromotionStates accepts all valid states.
func TestValidate_ValidPromotionStates(t *testing.T) {
	for _, s := range []string{"draft", "curated"} {
		t.Run(s, func(t *testing.T) {
			doc := docWithDefaults()
			doc.PromotionState = s
			if err := doc.Validate(); err != nil {
				t.Errorf("expected valid for promotion_state %q, got: %v", s, err)
			}
		})
	}
}

// TestValidate_CentralityScoreOutOfRange rejects scores outside 0–1.
func TestValidate_CentralityScoreOutOfRange(t *testing.T) {
	for _, bad := range []float64{-0.01, 1.01, -100, 2.5} {
		t.Run("", func(t *testing.T) {
			doc := docWithDefaults()
			score := bad
			doc.CentralityScore = &score
			if err := doc.Validate(); err == nil {
				t.Errorf("expected error for centrality_score %v, got nil", bad)
			}
		})
	}
}

// TestValidate_CentralityScoreInRange accepts scores within 0–1.
func TestValidate_CentralityScoreInRange(t *testing.T) {
	for _, good := range []float64{0.0, 0.5, 1.0, 0.999} {
		t.Run("", func(t *testing.T) {
			doc := docWithDefaults()
			score := good
			doc.CentralityScore = &score
			if err := doc.Validate(); err != nil {
				t.Errorf("expected valid for centrality_score %v, got: %v", good, err)
			}
		})
	}
}

// TestValidate_Compartments_CustomerDimensions confirms arbitrary dimension keys are accepted.
func TestValidate_Compartments_CustomerDimensions(t *testing.T) {
	doc := docWithDefaults()
	doc.Compartments = map[string][]string{
		"project":    {"alpha", "beta"},
		"department": {"engineering"},
		"geography":  {"eu-only"},
	}
	if err := doc.Validate(); err != nil {
		t.Errorf("expected valid with customer compartments, got: %v", err)
	}
}

// TestApplyDefaults_Idempotent verifies calling ApplyDefaults twice doesn't change values.
func TestApplyDefaults_Idempotent(t *testing.T) {
	doc := docWithDefaults()
	doc.PromotionState = "curated"
	doc.Classification = "CONFIDENTIAL"

	doc.ApplyDefaults()
	doc.ApplyDefaults()

	if doc.PromotionState != "curated" {
		t.Errorf("ApplyDefaults overwrote existing promotion_state: got %q", doc.PromotionState)
	}
	if doc.Classification != "CONFIDENTIAL" {
		t.Errorf("ApplyDefaults overwrote existing classification: got %q", doc.Classification)
	}
}

// TestProvenanceRawExhaustRef verifies raw_exhaust_ref lives inside provenance.
func TestProvenanceRawExhaustRef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prov-doc.json")
	if err := os.WriteFile(path, []byte(fullDocJSON), 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	doc, err := LoadDoc(path)
	if err != nil {
		t.Fatalf("LoadDoc failed: %v", err)
	}
	if doc.Provenance == nil {
		t.Fatal("provenance should not be nil")
	}
	if doc.Provenance.RawExhaustRef == "" {
		t.Error("raw_exhaust_ref should be set inside provenance")
	}
}

// TestNewDoc_WriteEmitsFields ensures new docs emit all applicable fields.
func TestNewDoc_WriteEmitsFields(t *testing.T) {
	score := 0.72
	doc := &Doc{
		ID:             "capture-test",
		Scope:          "project",
		Tags:           []string{"capture"},
		Created:        time.Now().UTC(),
		CreatedBy:      "claude-code",
		PromotionState: "draft",
		Classification: "INTERNAL",
		Compartments:   map[string][]string{},
		Provenance: &Provenance{
			Runtime:      "claude-code",
			TriggerEvent: "session.end",
		},
		Landmark:        false,
		CentralityScore: &score,
		Content:         map[string]any{"fact": "freshly captured fact"},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "capture-test.json")

	if err := SaveDoc(path, doc); err != nil {
		t.Fatalf("SaveDoc failed: %v", err)
	}

	// Verify JSON contains the expected keys.
	data, _ := os.ReadFile(path)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parsing saved JSON: %v", err)
	}

	for _, key := range []string{"promotion_state", "classification", "provenance", "centrality_score"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected key %q in saved JSON, not found", key)
		}
	}

	prov, ok := raw["provenance"].(map[string]any)
	if !ok {
		t.Fatal("provenance is not an object")
	}
	if prov["runtime"] != "claude-code" {
		t.Errorf("provenance.runtime mismatch: got %v", prov["runtime"])
	}
}

// TestValidate_FreeFormContent ensures content accepts any shape.
func TestValidate_FreeFormContent(t *testing.T) {
	contents := []map[string]any{
		{"pattern": "structural pattern observed"},
		{"learning": "something learned"},
		{"fact": "a fact", "source": "test"},
		{"text": "raw draft content", "source_lines": []int{0, 5}},
	}
	for i, content := range contents {
		t.Run(fmt.Sprintf("content-%d", i), func(t *testing.T) {
			doc := docWithDefaults()
			doc.Content = content
			if err := doc.Validate(); err != nil {
				t.Errorf("expected valid content, got: %v", err)
			}
		})
	}
}

// docWithDefaults returns a minimal valid Doc with defaults pre-applied.
func docWithDefaults() *Doc {
	doc := &Doc{
		ID:        "test-doc",
		Scope:     "project",
		Tags:      []string{"test"},
		Created:   time.Now().UTC(),
		CreatedBy: "owner",
		Content:   map[string]any{"fact": "a fact"},
	}
	doc.ApplyDefaults()
	return doc
}
