// Package cmd architecture guardrails (v1).
//
// These tests enforce post-alpha main-flow invariants. They are pure unit
// tests — no external services, no filesystem state beyond what they create
// themselves — and run as part of `go test ./internal/cmd/...` locally and
// in CI.
//
// To run only the guardrails locally:
//
//	go test ./internal/cmd/ -run 'TestGuardrail_'
//
// Each guard documents its allowlist and rationale inline. Add an entry only
// with a code reviewer's agreement that the new exception is justified.
package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// canonicalCLISurface is the public command set we intend to expose at the
// root level. Adding or removing a command requires updating this list
// (which is the point of the guard — it forces an explicit decision).
var canonicalCLISurface = []string{
	"curate",
	"demo",
	"doctor",
	"drafts",
	"export",
	"import",
	"init",
	"lens",
	"map",
	"project",
	"recall",
	"record",
	"serve",
	"status",
	"uninstall",
	"upgrade",
	"version",
	"watch",
}

// TestGuardrail_CLISurface_CanonicalCommandsRegistered fails if any command
// from canonicalCLISurface has been removed from rootCmd, catching
// accidental deletions in refactors.
func TestGuardrail_CLISurface_CanonicalCommandsRegistered(t *testing.T) {
	var missing []string
	for _, name := range canonicalCLISurface {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil || cmd == nil || cmd.Name() != name {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("canonical CLI commands missing from rootCmd: %v\n"+
			"If this removal was intentional, update canonicalCLISurface in %s.",
			missing, callerFile(t))
	}
}

// forbiddenTerms are case-insensitive substrings that must not appear in any
// user-facing command surface (Short, Long, flag usage). They name legacy
// concepts retired in the post-alpha architecture.
//
// No allowlist exists for v1: any hit is treated as a real regression to fix.
var forbiddenTerms = []string{
	"runtime", // retired in favor of "harness" (see #312)
	"leo",     // retired legacy naming (see #311)
}

// TestGuardrail_Terminology_NoLegacyTermsInUserFacingText recursively walks
// every command registered on rootCmd and asserts that no Short, Long, or
// flag-usage text contains a forbidden term.
func TestGuardrail_Terminology_NoLegacyTermsInUserFacingText(t *testing.T) {
	type hit struct {
		command string
		surface string
		term    string
		snippet string
	}
	var hits []hit

	var visit func(prefix string)
	visit = func(prefix string) {
		cmd, _, err := rootCmd.Find(strings.Fields(prefix))
		if err != nil || cmd == nil {
			return
		}
		surfaces := map[string]string{
			"short": cmd.Short,
			"long":  cmd.Long,
		}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			surfaces["flag:"+f.Name] = f.Usage
		})

		for surface, text := range surfaces {
			lower := strings.ToLower(text)
			for _, term := range forbiddenTerms {
				if strings.Contains(lower, term) {
					hits = append(hits, hit{
						command: prefix,
						surface: surface,
						term:    term,
						snippet: snippetAround(text, term, 60),
					})
				}
			}
		}

		for _, sub := range cmd.Commands() {
			visit(strings.TrimSpace(prefix + " " + sub.Name()))
		}
	}

	for _, sub := range rootCmd.Commands() {
		visit(sub.Name())
	}

	if len(hits) > 0 {
		var lines []string
		for _, h := range hits {
			lines = append(lines, "  "+h.command+" ["+h.surface+"]: contains "+h.term+
				" → "+h.snippet)
		}
		t.Fatalf("forbidden terminology in user-facing text (%d hit%s):\n%s\n"+
			"Replace 'runtime' with 'harness' (#312) and remove all 'leo'/'LEO' references (#311).",
			len(hits), pluralS(len(hits)), strings.Join(lines, "\n"))
	}
}

// snippetAround returns a short window of text around the first occurrence of
// term (case-insensitive), for friendlier failure messages.
func snippetAround(text, term string, window int) string {
	lower := strings.ToLower(text)
	idx := strings.Index(lower, strings.ToLower(term))
	if idx < 0 {
		return text
	}
	start := idx - window/2
	if start < 0 {
		start = 0
	}
	end := idx + len(term) + window/2
	if end > len(text) {
		end = len(text)
	}
	out := text[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// scopeWalkAllowlist names the files in internal/cmd/ that may still call
// the legacy scope-walk APIs. Each entry has explicit rationale.
//
//   - uninstall.go: Path-1 disconnect uses scope.NearestWritable as a
//     fallback so users with leftover project-local .mom/ dirs from
//     pre-v0.30 installs can still uninstall cleanly (#303 design lock).
//   - map.go: mom map / cartographer still writes drafts to
//     .mom/memory/*.json under the pre-central-vault model. Cartographer
//     v2 will retire this; until then the allowlist documents the debt.
var scopeWalkAllowlist = map[string]bool{
	"uninstall.go": true,
	"map.go":       true,
}

// scopeWalkForbidden is the set of legacy scope-walk symbols whose usage is
// banned outside the allowlist.
var scopeWalkForbidden = []string{
	"scope.NearestWritable",
	"scope.FindByLabel",
}

// TestGuardrail_CoreFlow_NoScopeWalkOutsideAllowlist scans non-test .go
// files in internal/cmd/ and fails if a legacy scope-walk symbol is used
// outside the explicit allowlist.
func TestGuardrail_CoreFlow_NoScopeWalkOutsideAllowlist(t *testing.T) {
	type hit struct {
		file   string
		line   int
		symbol string
		text   string
	}
	var hits []hit

	cmdDir := callerDir(t)
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		t.Fatalf("read cmd dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		if scopeWalkAllowlist[e.Name()] {
			continue
		}
		path := filepath.Join(cmdDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, sym := range scopeWalkForbidden {
				if strings.Contains(line, sym) {
					hits = append(hits, hit{
						file:   e.Name(),
						line:   i + 1,
						symbol: sym,
						text:   strings.TrimSpace(line),
					})
				}
			}
		}
	}
	if len(hits) > 0 {
		var lines []string
		for _, h := range hits {
			lines = append(lines, "  "+h.file+":"+strconv.Itoa(h.line)+": "+h.symbol+" → "+h.text)
		}
		t.Fatalf("scope-walk symbols used outside allowlist (%d hit%s):\n%s\n"+
			"The central-vault architecture has retired scope walks for core memory operations. "+
			"If a new compatibility exception is required, add it to scopeWalkAllowlist with rationale.",
			len(hits), pluralS(len(hits)), strings.Join(lines, "\n"))
	}
}

// callerDir returns the directory of this test file.
func callerDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}

// callerFile returns this test file's path, for friendlier failure messages.
func callerFile(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		return "internal/cmd/architecture_guardrails_test.go"
	}
	return filepath.Join(strings.TrimPrefix(wd, "/"), "architecture_guardrails_test.go")
}
