package harness

import (
	"strings"
	"testing"
)

// LanguageInstructions tests

func TestLanguageInstructions_English(t *testing.T) {
	result := LanguageInstructions("en")
	if !strings.Contains(result, "English") {
		t.Errorf("expected instructions to contain %q, got:\n%s", "English", result)
	}
}

func TestLanguageInstructions_Portuguese(t *testing.T) {
	result := LanguageInstructions("pt")
	if !strings.Contains(result, "Português") {
		t.Errorf("expected instructions to contain %q, got:\n%s", "Português", result)
	}
}

func TestLanguageInstructions_Spanish(t *testing.T) {
	result := LanguageInstructions("es")
	if !strings.Contains(result, "Español") {
		t.Errorf("expected instructions to contain %q, got:\n%s", "Español", result)
	}
}

func TestLanguageInstructions_UnknownFallsBackToEnglish(t *testing.T) {
	result := LanguageInstructions("zz")
	if !strings.Contains(result, "English") {
		t.Errorf("expected unknown language to fall back to English, got:\n%s", result)
	}
}

// CommunicationModeInstructions tests

func TestCommunicationMode_Concise(t *testing.T) {
	result := CommunicationModeInstructions("concise")
	if result == "" {
		t.Fatal("expected non-empty result for concise mode")
	}
	for _, want := range []string{"Communication mode: Concise", "DROP", "KEEP", "BOUNDARIES"} {
		if !strings.Contains(result, want) {
			t.Errorf("concise mode missing %q", want)
		}
	}
}

func TestCommunicationMode_Efficient(t *testing.T) {
	result := CommunicationModeInstructions("efficient")
	if result == "" {
		t.Fatal("expected non-empty result for efficient mode")
	}
	for _, want := range []string{"Communication mode: Efficient", "Fragment sentences", "Abbreviations allowed"} {
		if !strings.Contains(result, want) {
			t.Errorf("efficient mode missing %q", want)
		}
	}
}

func TestCommunicationMode_Default(t *testing.T) {
	result := CommunicationModeInstructions("default")
	if result != "" {
		t.Errorf("expected empty string for default mode, got:\n%s", result)
	}
}

func TestCommunicationMode_UnknownFallsToDefault(t *testing.T) {
	result := CommunicationModeInstructions("foobar")
	if result != "" {
		t.Errorf("expected empty string for unknown mode, got:\n%s", result)
	}
}

func TestCommunicationMode_BackwardCompat(t *testing.T) {
	// Old mode names "normal" and "verbose" fall through to the default case
	// (empty string). Backward compatibility is handled at the config loading
	// layer (normalizeCommunicationMode), not at instruction generation.
	for _, oldMode := range []string{"normal", "verbose", "caveman"} {
		result := CommunicationModeInstructions(oldMode)
		if result != "" {
			t.Errorf("old mode %q should return empty (mapped at config layer), got non-empty", oldMode)
		}
	}
}

// AutonomyInstructions tests

func TestAutonomyInstructions_Autonomous(t *testing.T) {
	result := AutonomyInstructions("autonomous")
	if !strings.Contains(result, "Autonomous") {
		t.Errorf("expected instructions to contain %q, got:\n%s", "Autonomous", result)
	}
	if !strings.Contains(result, "Act independently") {
		t.Errorf("expected instructions to contain %q, got:\n%s", "Act independently", result)
	}
}

func TestAutonomyInstructions_Balanced(t *testing.T) {
	result := AutonomyInstructions("balanced")
	if !strings.Contains(result, "Balanced") {
		t.Errorf("expected instructions to contain %q, got:\n%s", "Balanced", result)
	}
	if !strings.Contains(result, "Propose before") {
		t.Errorf("expected instructions to contain %q, got:\n%s", "Propose before", result)
	}
}

func TestAutonomyInstructions_Supervised(t *testing.T) {
	result := AutonomyInstructions("supervised")
	if !strings.Contains(result, "Supervised") {
		t.Errorf("expected instructions to contain %q, got:\n%s", "Supervised", result)
	}
	if !strings.Contains(result, "Confirm every") {
		t.Errorf("expected instructions to contain %q, got:\n%s", "Confirm every", result)
	}
}

func TestAutonomyInstructions_UnknownFallsBackToBalanced(t *testing.T) {
	result := AutonomyInstructions("unknown")
	if !strings.Contains(result, "Balanced") {
		t.Errorf("expected unknown autonomy to fall back to Balanced, got:\n%s", result)
	}
}
