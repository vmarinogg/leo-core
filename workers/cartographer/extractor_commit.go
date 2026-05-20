package cartographer

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// defaultCommitDepth is how many commits to scan when depth is unset.
const defaultCommitDepth = 200

// conventionalPrefixes is the set of recognised conventional commit prefixes.
var conventionalPrefixes = map[string]bool{
	"feat":     true,
	"refactor": true,
	"fix":      true,
	"chore":    true,
	"docs":     true,
	"perf":     true,
	"build":    true,
	"ci":       true,
	"test":     true,
}

var reConventional = regexp.MustCompile(`^([a-z]+)(\(([^)]+)\))?(!)?:\s*(.+)$`)

// CommitLogExtractor extracts memories from git commit messages.
type CommitLogExtractor struct {
	depth int
}

// NewCommitLogExtractor returns a CommitLogExtractor with the given depth.
// Depth 0 uses defaultCommitDepth.
func NewCommitLogExtractor(depth int) *CommitLogExtractor {
	if depth <= 0 {
		depth = defaultCommitDepth
	}
	return &CommitLogExtractor{depth: depth}
}

func (e *CommitLogExtractor) Name() string { return "commits" }

// Matches always returns false — this extractor is triggered by Scan directly
// rather than by file extension. The Cartographer identifies "commits" by Name.
func (e *CommitLogExtractor) Matches(_ string) bool { return false }

func (e *CommitLogExtractor) Extract(ctx context.Context, src Source) ([]Draft, error) {
	// Run git log in the source directory.
	format := "%H\x1f%s\x1f%b\x1e"
	args := []string{
		"log",
		fmt.Sprintf("--max-count=%d", e.depth),
		fmt.Sprintf("--format=%s", format),
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = src.Path

	out, err := cmd.Output()
	if err != nil {
		// Not a git repo or git not installed — skip silently.
		return nil, nil
	}

	return parseCommitLog(string(out), src.Path)
}

// parseCommitLog parses the git log output and returns drafts for
// conventional commits.
func parseCommitLog(output, repoPath string) ([]Draft, error) {
	records := strings.Split(output, "\x1e")
	var drafts []Draft

	for _, rec := range records {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}

		parts := strings.SplitN(rec, "\x1f", 3)
		if len(parts) < 2 {
			continue
		}

		sha := strings.TrimSpace(parts[0])
		subject := strings.TrimSpace(parts[1])
		body := ""
		if len(parts) == 3 {
			body = strings.TrimSpace(parts[2])
		}

		m := reConventional.FindStringSubmatch(subject)
		if m == nil {
			continue
		}

		prefix := m[1]
		scope := m[3]
		summary := m[5]
		if !conventionalPrefixes[prefix] {
			continue
		}

		tags := []string{"commit", "bootstrap", prefix}
		if scope != "" {
			tags = append(tags, slugify(scope))
		}

		fullSummary := summary
		if body != "" {
			fullSummary = summary + ". " + truncate(body, 200)
		}

		drafts = append(drafts, Draft{
			Summary: truncate(fullSummary, 200),
			Tags:    tags,
			Content: map[string]any{
				"commit_sha": sha,
				"subject":    subject,
				"body":       body,
				"prefix":     prefix,
				"scope":      scope,
			},
			Provenance: ProvenanceMeta{
				SourceFile:   repoPath,
				TriggerEvent: TriggerEvent,
				CommitSHA:    sha,
			},
		})
	}
	return drafts, nil
}

// slugify converts a string to kebab-case suitable for tags.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == '-' || r == '_' || r == ' ' || r == '/' {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
