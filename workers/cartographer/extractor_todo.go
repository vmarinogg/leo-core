package cartographer

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// todoMinLength is the minimum character count for a TODO comment body to be
// considered "structured" (i.e., worth capturing as a memory).
const todoMinLength = 20

// reTodoComment matches TODO/FIXME/HACK/NOTE/WHY comment markers.
// Handles: // TODO: ..., # TODO ..., /* TODO: ... */, -- TODO: ...
var reTodoComment = regexp.MustCompile(
	`(?i)(?://|#|/\*|--)\s*(TODO|FIXME|HACK|NOTE|WHY)[:\s]+(.+?)(?:\s*\*/)?$`,
)

// TodoFixmeExtractor captures structured TODO/FIXME/HACK/NOTE/WHY comments.
type TodoFixmeExtractor struct{}

// NewTodoFixmeExtractor returns an initialised TodoFixmeExtractor.
func NewTodoFixmeExtractor() *TodoFixmeExtractor { return &TodoFixmeExtractor{} }

func (e *TodoFixmeExtractor) Name() string { return "todo-fixme" }

// Matches returns true for most source code and text file extensions.
// It excludes binary-ish files and files handled by other extractors.
func (e *TodoFixmeExtractor) Matches(path string) bool {
	ext := fileExt(path)
	skip := map[string]bool{
		// Handled by markdown extractor.
		".md": true, ".mdx": true, ".txt": true, ".rst": true,
		// Binary or generated.
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
		".pdf": true, ".zip": true, ".tar": true, ".gz": true,
		".exe": true, ".dll": true, ".so": true, ".dylib": true, ".a": true,
		".wasm": true, ".bin": true,
		// Manifests handled by dependency extractor.
		// (those are identified by base name, not ext, so no ext conflict)
	}
	return !skip[ext]
}

func (e *TodoFixmeExtractor) Extract(_ context.Context, src Source) ([]Draft, error) {
	lines := linesOf(src.Content)
	srcHash := hashBytes(src.Content)

	var drafts []Draft

	for i, line := range lines {
		m := reTodoComment.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		marker := strings.ToUpper(m[1])
		body := strings.TrimSpace(m[2])

		// Skip trivial one-word or very short comments.
		if len(body) < todoMinLength {
			continue
		}

		// Require at least one space (i.e., multiple words) — single-word "bodies"
		// after trimming are almost always placeholders.
		if !strings.Contains(body, " ") {
			continue
		}

		tags := []string{strings.ToLower(marker), "bootstrap"}
		if marker == "FIXME" || marker == "HACK" {
			tags = append(tags, "tech-debt")
		}

		drafts = append(drafts, Draft{
			Summary: fmt.Sprintf("%s: %s", marker, truncate(body, 120)),
			Tags:    tags,
			Content: map[string]any{
				"marker": marker,
				"text":   body,
			},
			Provenance: ProvenanceMeta{
				SourceFile:   src.Path,
				SourceLines:  fmt.Sprintf("%d", i+1),
				SourceHash:   srcHash,
				TriggerEvent: TriggerEvent,
			},
		})
	}
	return drafts, nil
}
