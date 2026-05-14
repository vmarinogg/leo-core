package herald

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// schemaDir returns the absolute path to .github/mrp/schemas/ relative to
// this test file. The path walks four levels up from the package directory:
//
//	cli/internal/herald  →  cli/internal  →  cli  →  mom  →  .github/mrp/schemas
func schemaDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".github", "mrp", "schemas")
}

// validateAgainstSchema marshals v to JSON, round-trips through json.Unmarshal
// into an any (producing the map/slice/scalar types the validator expects),
// then validates against the named schema file.
func validateAgainstSchema(t *testing.T, schemaFile string, v any) error {
	t.Helper()
	schemaPath := filepath.Join(schemaDir(t), schemaFile)

	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft7
	sch, err := c.Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema %s: %v", schemaFile, err)
	}

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}

	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal for validation: %v", err)
	}

	return sch.Validate(decoded)
}

// mustPass asserts that v validates against schemaFile without error.
func mustPass(t *testing.T, schemaFile string, v any) {
	t.Helper()
	if err := validateAgainstSchema(t, schemaFile, v); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}
}

// mustFail asserts that v fails validation against schemaFile.
func mustFail(t *testing.T, schemaFile string, v any) {
	t.Helper()
	if err := validateAgainstSchema(t, schemaFile, v); err == nil {
		t.Errorf("expected validation failure, but event passed")
	}
}

// ── session.start ────────────────────────────────────────────────────────────

func TestMRPSessionStart_ValidMinimal(t *testing.T) {
	mustPass(t, "session-start.schema.json", MRPSessionStart{
		MRPVersion: "v0",
		Event:      "session.start",
		SessionID:  "sess-001",
		Harness:    "claude-code",
		Timestamp:  "2026-04-18T10:00:00Z",
		StartedAt:  "2026-04-18T10:00:00Z",
	})
}

func TestMRPSessionStart_ValidWithOptionals(t *testing.T) {
	mustPass(t, "session-start.schema.json", MRPSessionStart{
		MRPVersion:  "v0",
		Event:       "session.start",
		SessionID:   "sess-002",
		Harness:     "codex",
		Timestamp:   "2026-04-18T10:00:00Z",
		StartedAt:   "2026-04-18T10:00:00Z",
		ProjectRoot: "/home/user/project",
		UserID:      "user-42",
	})
}

func TestMRPSessionStart_MissingRequired(t *testing.T) {
	tests := []struct {
		name string
		ev   MRPSessionStart
	}{
		{
			name: "missing mrp_version",
			ev: MRPSessionStart{
				Event: "session.start", SessionID: "s", Harness: "r",
				Timestamp: "2026-04-18T10:00:00Z", StartedAt: "2026-04-18T10:00:00Z",
			},
		},
		{
			name: "missing session_id",
			ev: MRPSessionStart{
				MRPVersion: "v0", Event: "session.start", Harness: "r",
				Timestamp: "2026-04-18T10:00:00Z", StartedAt: "2026-04-18T10:00:00Z",
			},
		},
		{
			name: "missing started_at",
			ev: MRPSessionStart{
				MRPVersion: "v0", Event: "session.start", SessionID: "s",
				Harness: "r", Timestamp: "2026-04-18T10:00:00Z",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mustFail(t, "session-start.schema.json", tc.ev)
		})
	}
}

func TestMRPSessionStart_WrongEventConst(t *testing.T) {
	mustFail(t, "session-start.schema.json", MRPSessionStart{
		MRPVersion: "v0",
		Event:      "session.end", // wrong const
		SessionID:  "s",
		Harness:    "r",
		Timestamp:  "2026-04-18T10:00:00Z",
		StartedAt:  "2026-04-18T10:00:00Z",
	})
}

// ── session.end ──────────────────────────────────────────────────────────────

func TestMRPSessionEnd_ValidMinimal(t *testing.T) {
	mustPass(t, "session-end.schema.json", MRPSessionEnd{
		MRPVersion: "v0",
		Event:      "session.end",
		SessionID:  "sess-001",
		Harness:    "claude-code",
		Timestamp:  "2026-04-18T11:00:00Z",
		StartedAt:  "2026-04-18T10:00:00Z",
		EndedAt:    "2026-04-18T11:00:00Z",
	})
}

func TestMRPSessionEnd_ValidWithOptionals(t *testing.T) {
	turns := 12
	reason := "user_ended"
	mustPass(t, "session-end.schema.json", MRPSessionEnd{
		MRPVersion: "v0",
		Event:      "session.end",
		SessionID:  "sess-002",
		Harness:    "claude-code",
		Timestamp:  "2026-04-18T11:00:00Z",
		StartedAt:  "2026-04-18T10:00:00Z",
		EndedAt:    "2026-04-18T11:00:00Z",
		TurnCount:  &turns,
		ExitReason: &reason,
	})
}

func TestMRPSessionEnd_MissingEndedAt(t *testing.T) {
	mustFail(t, "session-end.schema.json", MRPSessionEnd{
		MRPVersion: "v0",
		Event:      "session.end",
		SessionID:  "s",
		Harness:    "r",
		Timestamp:  "2026-04-18T11:00:00Z",
		StartedAt:  "2026-04-18T10:00:00Z",
		// EndedAt missing
	})
}

func TestMRPSessionEnd_InvalidExitReason(t *testing.T) {
	reason := "aborted" // not in enum
	mustFail(t, "session-end.schema.json", MRPSessionEnd{
		MRPVersion: "v0",
		Event:      "session.end",
		SessionID:  "s",
		Harness:    "r",
		Timestamp:  "2026-04-18T11:00:00Z",
		StartedAt:  "2026-04-18T10:00:00Z",
		EndedAt:    "2026-04-18T11:00:00Z",
		ExitReason: &reason,
	})
}

func TestMRPSessionEnd_AllExitReasons(t *testing.T) {
	for _, reason := range []string{"user_ended", "harness_ended", "timeout", "error"} {
		r := reason
		t.Run(reason, func(t *testing.T) {
			mustPass(t, "session-end.schema.json", MRPSessionEnd{
				MRPVersion: "v0",
				Event:      "session.end",
				SessionID:  "s",
				Harness:    "r",
				Timestamp:  "2026-04-18T11:00:00Z",
				StartedAt:  "2026-04-18T10:00:00Z",
				EndedAt:    "2026-04-18T11:00:00Z",
				ExitReason: &r,
			})
		})
	}
}

// ── turn.complete ────────────────────────────────────────────────────────────

func TestMRPTurnComplete_ValidMinimal(t *testing.T) {
	mustPass(t, "turn-complete.schema.json", MRPTurnComplete{
		MRPVersion: "v0",
		Event:      "turn.complete",
		SessionID:  "sess-001",
		Harness:    "claude-code",
		Timestamp:  "2026-04-18T10:05:00Z",
		TurnIndex:  0,
	})
}

func TestMRPTurnComplete_ValidWithTokens(t *testing.T) {
	prompt := 1024
	completion := 512
	mustPass(t, "turn-complete.schema.json", MRPTurnComplete{
		MRPVersion:       "v0",
		Event:            "turn.complete",
		SessionID:        "sess-001",
		Harness:          "claude-code",
		Timestamp:        "2026-04-18T10:05:00Z",
		TurnIndex:        1,
		PromptTokens:     &prompt,
		CompletionTokens: &completion,
	})
}

func TestMRPTurnComplete_MissingTurnIndex(t *testing.T) {
	// turn_index is required — zero value is valid (0 is allowed), so test missing runtime instead.
	mustFail(t, "turn-complete.schema.json", MRPTurnComplete{
		MRPVersion: "v0",
		Event:      "turn.complete",
		// SessionID missing — required field
		Harness:   "claude-code",
		Timestamp: "2026-04-18T10:05:00Z",
		TurnIndex: 1,
	})
}

// ── compact.triggered ────────────────────────────────────────────────────────

func TestMRPCompactTriggered_ValidMinimal(t *testing.T) {
	mustPass(t, "compact-triggered.schema.json", MRPCompactTriggered{
		MRPVersion: "v0",
		Event:      "compact.triggered",
		SessionID:  "sess-001",
		Harness:    "claude-code",
		Timestamp:  "2026-04-18T10:30:00Z",
	})
}

func TestMRPCompactTriggered_ValidTriggerSources(t *testing.T) {
	for _, src := range []string{"user", "harness"} {
		s := src
		t.Run(src, func(t *testing.T) {
			mustPass(t, "compact-triggered.schema.json", MRPCompactTriggered{
				MRPVersion:    "v0",
				Event:         "compact.triggered",
				SessionID:     "s",
				Harness:       "r",
				Timestamp:     "2026-04-18T10:30:00Z",
				TriggerSource: &s,
			})
		})
	}
}

func TestMRPCompactTriggered_InvalidTriggerSource(t *testing.T) {
	src := "schedule" // not in enum
	mustFail(t, "compact-triggered.schema.json", MRPCompactTriggered{
		MRPVersion:    "v0",
		Event:         "compact.triggered",
		SessionID:     "s",
		Harness:       "r",
		Timestamp:     "2026-04-18T10:30:00Z",
		TriggerSource: &src,
	})
}

// ── clear.triggered ──────────────────────────────────────────────────────────

func TestMRPClearTriggered_ValidMinimal(t *testing.T) {
	mustPass(t, "clear-triggered.schema.json", MRPClearTriggered{
		MRPVersion: "v0",
		Event:      "clear.triggered",
		SessionID:  "sess-001",
		Harness:    "claude-code",
		Timestamp:  "2026-04-18T10:45:00Z",
	})
}

func TestMRPClearTriggered_ValidTriggerSources(t *testing.T) {
	for _, src := range []string{"user", "harness"} {
		s := src
		t.Run(src, func(t *testing.T) {
			mustPass(t, "clear-triggered.schema.json", MRPClearTriggered{
				MRPVersion:    "v0",
				Event:         "clear.triggered",
				SessionID:     "s",
				Harness:       "r",
				Timestamp:     "2026-04-18T10:45:00Z",
				TriggerSource: &s,
			})
		})
	}
}

func TestMRPClearTriggered_InvalidTriggerSource(t *testing.T) {
	src := "cron" // not in enum
	mustFail(t, "clear-triggered.schema.json", MRPClearTriggered{
		MRPVersion:    "v0",
		Event:         "clear.triggered",
		SessionID:     "s",
		Harness:       "r",
		Timestamp:     "2026-04-18T10:45:00Z",
		TriggerSource: &src,
	})
}

// ── capture-payload ──────────────────────────────────────────────────────────

func TestMRPCapturePayload_ValidWithDrafts(t *testing.T) {
	mustPass(t, "capture-payload.schema.json", MRPCapturePayload{
		RawExhaust: "session transcript here",
		ClassifiedDrafts: []MRPClassifiedDraft{
			{Type: "decision", Summary: "chose TDD approach", Confidence: "EXTRACTED", Tags: []string{"testing"}},
			{Type: "pattern", Summary: "table-driven tests", Confidence: "INFERRED"},
		},
	})
}

func TestMRPCapturePayload_ValidEmptyDrafts(t *testing.T) {
	mustPass(t, "capture-payload.schema.json", MRPCapturePayload{
		RawExhaust:       "raw session output",
		ClassifiedDrafts: []MRPClassifiedDraft{},
	})
}

func TestMRPCapturePayload_MissingRawExhaust(t *testing.T) {
	mustFail(t, "capture-payload.schema.json", map[string]any{
		"classified_drafts": []any{},
	})
}

func TestMRPCapturePayload_InvalidDraftType(t *testing.T) {
	mustFail(t, "capture-payload.schema.json", MRPCapturePayload{
		RawExhaust: "raw",
		ClassifiedDrafts: []MRPClassifiedDraft{
			{Type: "unknown-type", Summary: "s", Confidence: "EXTRACTED"},
		},
	})
}

func TestMRPCapturePayload_InvalidConfidence(t *testing.T) {
	mustFail(t, "capture-payload.schema.json", MRPCapturePayload{
		RawExhaust: "raw",
		ClassifiedDrafts: []MRPClassifiedDraft{
			{Type: "fact", Summary: "s", Confidence: "HIGH"}, // not in enum
		},
	})
}

func TestMRPCapturePayload_AllDraftTypes(t *testing.T) {
	for _, dt := range []string{"decision", "pattern", "fact", "learning", "session-log"} {
		draftType := dt
		t.Run(draftType, func(t *testing.T) {
			mustPass(t, "capture-payload.schema.json", MRPCapturePayload{
				RawExhaust: "raw",
				ClassifiedDrafts: []MRPClassifiedDraft{
					{Type: draftType, Summary: "a summary", Confidence: "EXTRACTED"},
				},
			})
		})
	}
}

func TestMRPCapturePayload_AllConfidenceLevels(t *testing.T) {
	for _, conf := range []string{"EXTRACTED", "INFERRED", "AMBIGUOUS"} {
		confidence := conf
		t.Run(confidence, func(t *testing.T) {
			mustPass(t, "capture-payload.schema.json", MRPCapturePayload{
				RawExhaust: "raw",
				ClassifiedDrafts: []MRPClassifiedDraft{
					{Type: "fact", Summary: "a summary", Confidence: confidence},
				},
			})
		})
	}
}

// ── adapter-capability ───────────────────────────────────────────────────────

func TestMRPAdapterCapability_ValidClaudeCode(t *testing.T) {
	mustPass(t, "adapter-capability.schema.json", MRPAdapterCapability{
		Adapter:      "claude-code",
		Version:      "1.0",
		Supports:     []string{"session.start", "session.end", "turn.complete", "compact.triggered", "clear.triggered"},
		Experimental: []string{},
	})
}

func TestMRPAdapterCapability_ValidCodex(t *testing.T) {
	mustPass(t, "adapter-capability.schema.json", MRPAdapterCapability{
		Adapter:      "codex",
		Version:      "0.9.0",
		Supports:     []string{"session.start", "session.end"},
		Experimental: []string{"compact.triggered", "clear.triggered"},
	})
}

func TestMRPAdapterCapability_MissingRequired(t *testing.T) {
	tests := []struct {
		name string
		ev   map[string]any
	}{
		{
			name: "missing adapter",
			ev:   map[string]any{"version": "1.0", "supports": []any{}, "experimental": []any{}},
		},
		{
			name: "missing supports",
			ev:   map[string]any{"adapter": "x", "version": "1.0", "experimental": []any{}},
		},
		{
			name: "missing experimental",
			ev:   map[string]any{"adapter": "x", "version": "1.0", "supports": []any{}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mustFail(t, "adapter-capability.schema.json", tc.ev)
		})
	}
}

func TestMRPAdapterCapability_InvalidEventName(t *testing.T) {
	mustFail(t, "adapter-capability.schema.json", MRPAdapterCapability{
		Adapter:      "custom",
		Version:      "1.0",
		Supports:     []string{"git.commit"}, // not in enum
		Experimental: []string{},
	})
}
