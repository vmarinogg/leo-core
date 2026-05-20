package drafter

import (
	"path/filepath"
	"regexp"
	"strings"
)

var camelCaseRe = regexp.MustCompile(`[A-Z][a-z]+(?:[A-Z][a-z]+)+`)
var snakeCaseRe = regexp.MustCompile(`[a-z]+(?:_[a-z]+){1,}`)

// ExtractFileTags extracts tags from file paths.
func ExtractFileTags(paths []string) []string {
	seen := make(map[string]bool)
	var tags []string
	for _, p := range paths {
		parts := strings.Split(filepath.ToSlash(p), "/")
		for _, part := range parts {
			clean := strings.TrimSuffix(part, filepath.Ext(part))
			if clean != "" && !seen[clean] && len(clean) > 2 {
				seen[clean] = true
				tags = append(tags, strings.ToLower(clean))
			}
		}
	}
	return tags
}

// ExtractIdentifiers extracts CamelCase and snake_case identifiers from text.
func ExtractIdentifiers(text string) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, m := range camelCaseRe.FindAllString(text, -1) {
		lower := strings.ToLower(m)
		if !seen[lower] {
			seen[lower] = true
			ids = append(ids, lower)
		}
	}
	for _, m := range snakeCaseRe.FindAllString(text, -1) {
		// Convert snake_case to kebab-case for valid tags.
		kebab := strings.ReplaceAll(m, "_", "-")
		if !seen[kebab] {
			seen[kebab] = true
			ids = append(ids, kebab)
		}
	}
	return ids
}
