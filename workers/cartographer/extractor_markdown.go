package cartographer

import (
	"context"

	"strings"
)

// markdownExtensions is the set of file extensions handled by MarkdownExtractor.
var markdownExtensions = map[string]bool{
	".md": true, ".mdx": true, ".txt": true, ".rst": true,
}

// MarkdownExtractor extracts decisions, patterns, and facts from markdown-like files.
type MarkdownExtractor struct{}

// NewMarkdownExtractor returns an initialised MarkdownExtractor.
func NewMarkdownExtractor() *MarkdownExtractor { return &MarkdownExtractor{} }

func (e *MarkdownExtractor) Name() string { return "markdown" }

func (e *MarkdownExtractor) Matches(path string) bool {
	ext := strings.ToLower(fileExt(path))
	return markdownExtensions[ext]
}

func (e *MarkdownExtractor) Extract(_ context.Context, src Source) ([]Draft, error) {
	lines := linesOf(src.Content)
	srcHash := hashBytes(src.Content)

	var drafts []Draft

	// Split the document into sections by headings.
	// Each section becomes one memory draft.
	type section struct {
		heading string
		tags    []string
		start   int
		body    strings.Builder
	}

	var cur *section

	flush := func(endLine int) {
		if cur == nil {
			return
		}
		body := strings.TrimSpace(cur.body.String())
		if body == "" {
			cur = nil
			return
		}

		summary := cur.heading
		if summary == "" {
			// No heading — use the file name as context.
			summary = fileBaseName(src.Path)
		}

		tags := cur.tags
		if len(tags) == 0 {
			tags = []string{"markdown", "bootstrap"}
		}

		d := Draft{
			Summary: truncate(summary+": "+truncate(body, 80), 120),
			Tags:    tags,
			Content: map[string]any{
				"heading": cur.heading,
				"body":    body,
			},
			Provenance: ProvenanceMeta{
				SourceFile:   src.Path,
				SourceLines:  lineRange(cur.start+1, endLine),
				SourceHash:   srcHash,
				TriggerEvent: TriggerEvent,
			},
		}
		drafts = append(drafts, d)
		cur = nil
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect ATX headings (# ## ###).
		if strings.HasPrefix(trimmed, "#") {
			flush(i)

			heading := strings.TrimLeft(trimmed, "# ")
			tags := []string{"markdown", "bootstrap"}

			// Add semantic tags for known heading types.
			lower := strings.ToLower(heading)
			switch {
			case strings.Contains(lower, "decision"):
				tags = append(tags, "decision")
			case strings.Contains(lower, "pattern"):
				tags = append(tags, "pattern")
			case strings.Contains(lower, "architecture"), strings.Contains(lower, "adr"):
				tags = append(tags, "decision")
			}

			cur = &section{heading: heading, tags: tags, start: i}
			continue
		}

		// Start capturing if no heading yet (file without headings).
		if cur == nil {
			cur = &section{heading: "", tags: []string{"markdown", "bootstrap"}, start: i}
		}

		// Accumulate body for current section.
		cur.body.WriteString(line)
		cur.body.WriteByte('\n')
	}
	flush(len(lines))

	return drafts, nil
}

// fileBaseName returns the file name without extension.
func fileBaseName(path string) string {
	base := strings.TrimSuffix(path, fileExt(path))
	parts := strings.Split(base, "/")
	return parts[len(parts)-1]
}

// fileExt returns the lowercase extension of a path.
func fileExt(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return strings.ToLower(path[i:])
}

// truncate cuts s to at most n runes, appending "..." if truncated.
func truncate(s string, n int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n-3]) + "..."
}
