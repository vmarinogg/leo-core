package cartographer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// dependencyManifestNames is the set of file basenames handled by DependencyManifestExtractor.
var dependencyManifestNames = map[string]string{
	"package.json":     "javascript",
	"go.mod":           "go",
	"requirements.txt": "python",
	"Cargo.toml":       "rust",
	"pyproject.toml":   "python",
}

// DependencyManifestExtractor extracts one fact per direct dependency.
type DependencyManifestExtractor struct{}

// NewDependencyManifestExtractor returns an initialised DependencyManifestExtractor.
func NewDependencyManifestExtractor() *DependencyManifestExtractor {
	return &DependencyManifestExtractor{}
}

func (e *DependencyManifestExtractor) Name() string { return "dependencies" }

func (e *DependencyManifestExtractor) Matches(path string) bool {
	base := filepath.Base(path)
	_, ok := dependencyManifestNames[base]
	return ok
}

func (e *DependencyManifestExtractor) Extract(_ context.Context, src Source) ([]Draft, error) {
	base := filepath.Base(src.Path)
	lang := dependencyManifestNames[base]
	srcHash := hashBytes(src.Content)

	var deps []dependency
	var parseErr error

	switch base {
	case "package.json":
		deps, parseErr = parsePackageJSON(src.Content)
	case "go.mod":
		deps, parseErr = parseGoMod(src.Content)
	case "requirements.txt":
		deps, parseErr = parseRequirementsTxt(src.Content)
	case "Cargo.toml":
		deps, parseErr = parseCargoToml(src.Content)
	case "pyproject.toml":
		deps, parseErr = parsePyprojectToml(src.Content)
	}
	if parseErr != nil {
		return nil, parseErr
	}

	drafts := make([]Draft, 0, len(deps))
	for _, dep := range deps {
		summary := fmt.Sprintf("Depends on %s", dep.name)
		if dep.version != "" {
			summary = fmt.Sprintf("Depends on %s %s", dep.name, dep.version)
		}
		drafts = append(drafts, Draft{
			Summary: summary,
			Tags:    []string{"dependency", lang, "bootstrap"},
			Content: map[string]any{
				"package":  dep.name,
				"version":  dep.version,
				"language": lang,
				"manifest": base,
			},
			Provenance: ProvenanceMeta{
				SourceFile:   src.Path,
				SourceLines:  dep.line,
				SourceHash:   srcHash,
				TriggerEvent: TriggerEvent,
			},
		})
	}
	return drafts, nil
}

type dependency struct {
	name    string
	version string
	line    string
}

// ── package.json ─────────────────────────────────────────────────────────────

func parsePackageJSON(data []byte) ([]dependency, error) {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parsing package.json: %w", err)
	}

	var deps []dependency
	for name, ver := range pkg.Dependencies {
		deps = append(deps, dependency{name: name, version: ver})
	}
	// Also include devDependencies as facts.
	for name, ver := range pkg.DevDependencies {
		deps = append(deps, dependency{name: name, version: ver})
	}
	return deps, nil
}

// ── go.mod ───────────────────────────────────────────────────────────────────

var reGoRequire = regexp.MustCompile(`^\s*([^\s]+)\s+([^\s]+)`)

func parseGoMod(data []byte) ([]dependency, error) {
	var deps []dependency
	inRequire := false
	lineNum := 0

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "require (" {
			inRequire = true
			continue
		}
		if inRequire && trimmed == ")" {
			inRequire = false
			continue
		}

		// Single-line require.
		if strings.HasPrefix(trimmed, "require ") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 3 {
				name := parts[1]
				ver := parts[2]
				if !strings.HasSuffix(ver, "// indirect") {
					deps = append(deps, dependency{name: name, version: ver, line: fmt.Sprintf("%d", lineNum)})
				}
			}
			continue
		}

		if inRequire && trimmed != "" {
			// Skip indirect deps.
			if strings.Contains(trimmed, "// indirect") {
				continue
			}
			if m := reGoRequire.FindStringSubmatch(trimmed); m != nil {
				deps = append(deps, dependency{name: m[1], version: m[2], line: fmt.Sprintf("%d", lineNum)})
			}
		}
	}
	return deps, nil
}

// ── requirements.txt ─────────────────────────────────────────────────────────

var reRequirement = regexp.MustCompile(`^([a-zA-Z0-9_.-]+)([=<>!~]+(.+))?`)

func parseRequirementsTxt(data []byte) ([]dependency, error) {
	var deps []dependency
	lineNum := 0

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if m := reRequirement.FindStringSubmatch(line); m != nil {
			ver := ""
			if len(m) >= 4 {
				ver = m[2] // includes operator, e.g. "==1.2.3"
			}
			deps = append(deps, dependency{name: m[1], version: strings.TrimSpace(ver), line: fmt.Sprintf("%d", lineNum)})
		}
	}
	return deps, nil
}

// ── Cargo.toml ───────────────────────────────────────────────────────────────

var reTomlDep = regexp.MustCompile(`^([a-zA-Z0-9_-]+)\s*=\s*"([^"]+)"`)
var reTomlDepTable = regexp.MustCompile(`^([a-zA-Z0-9_-]+)\s*=\s*\{`)

func parseCargoToml(data []byte) ([]dependency, error) {
	var deps []dependency
	inDeps := false
	lineNum := 0

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "[dependencies]" || trimmed == "[dev-dependencies]" || trimmed == "[build-dependencies]" {
			inDeps = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inDeps = false
			continue
		}

		if inDeps {
			if m := reTomlDep.FindStringSubmatch(trimmed); m != nil {
				deps = append(deps, dependency{name: m[1], version: m[2], line: fmt.Sprintf("%d", lineNum)})
			} else if m := reTomlDepTable.FindStringSubmatch(trimmed); m != nil {
				// version may be inside the inline table; skip complex parsing.
				deps = append(deps, dependency{name: m[1], version: "", line: fmt.Sprintf("%d", lineNum)})
			}
		}
	}
	return deps, nil
}

// ── pyproject.toml ────────────────────────────────────────────────────────────

var rePyDep = regexp.MustCompile(`^"?([a-zA-Z0-9_.-]+[a-zA-Z0-9]).*"?,?\s*$`)

func parsePyprojectToml(data []byte) ([]dependency, error) {
	var deps []dependency
	inDeps := false
	lineNum := 0

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "[tool.poetry.dependencies]" || trimmed == "[project.dependencies]" ||
			strings.HasSuffix(trimmed, ".dependencies]") {
			inDeps = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inDeps = false
			continue
		}

		if inDeps {
			// Handle PEP 508 dependency strings in [[project]] tables.
			if strings.HasPrefix(trimmed, `"`) || (len(trimmed) > 0 && trimmed[0] != '#') {
				// Try key = "version" style (poetry).
				if m := reTomlDep.FindStringSubmatch(trimmed); m != nil {
					deps = append(deps, dependency{name: m[1], version: m[2], line: fmt.Sprintf("%d", lineNum)})
					continue
				}
				// Try bare string style (PEP 508).
				if m := rePyDep.FindStringSubmatch(strings.Trim(trimmed, `",`)); m != nil && m[1] != "" {
					deps = append(deps, dependency{name: m[1], version: "", line: fmt.Sprintf("%d", lineNum)})
				}
			}
		}
	}
	return deps, nil
}
