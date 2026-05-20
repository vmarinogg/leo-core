package drafter

import (
	"regexp"
	"strings"
)

// sanitizeForTags applies a stricter cleaning pass on text before
// tag extraction. Removes XML tags, code blocks, file paths, URLs,
// and other noise that would pollute RAKE/BM25 tag generation while
// being fine in content.
func sanitizeForTags(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") {
			continue
		}
		if looksLikePath(line) {
			continue
		}
		if looksLikeCode(line) {
			continue
		}
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			continue
		}
		line = xmlTagRe.ReplaceAllString(line, " ")
		line = strings.ReplaceAll(line, "```", "")
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "##", "")
		line = strings.Join(strings.Fields(line), " ")
		if len(line) > 2 {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

var xmlTagRe = regexp.MustCompile(`<[^>]+>`)

func looksLikePath(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "./") {
		return true
	}
	slashes := strings.Count(trimmed, "/")
	if slashes >= 3 && float64(slashes)/float64(len(trimmed)) > 0.05 {
		return true
	}
	return false
}

func looksLikeCode(line string) bool {
	trimmed := strings.TrimSpace(line)
	codeIndicators := []string{
		"func ", "import ", "package ", "return ", "if err",
		"var ", "const ", "type ", "fmt.", "os.", "json.",
		"func(", "map[", "[]", ":=", "!=", "==",
		"```", "{}", "();",
	}
	for _, indicator := range codeIndicators {
		if strings.Contains(trimmed, indicator) {
			return true
		}
	}
	return false
}
