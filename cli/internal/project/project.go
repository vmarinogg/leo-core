// Package project resolves the project identity for a working directory
// by walking up the filesystem looking for a `.mom-project.yaml` file
// (ADR 0016). The resolved `id` is what gets stamped on every captured
// memory's `project_id` column.
//
// MOM does not warn or block based on nesting patterns. The resolver
// simply returns the longest-ancestor match — users decide how to
// organise their files.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/momhq/mom/cli/internal/pathutil"
	"gopkg.in/yaml.v3"
)

// BindFilename is the name of the project-binding file users check into
// their repositories. The ADR-0016-locked filename.
const BindFilename = ".mom-project.yaml"

// MaxIdLength caps the id length to prevent pathological values. The
// id is a user-chosen string, but unbounded length would be abuse.
const MaxIdLength = 256

// bindFile is the on-disk shape of .mom-project.yaml.
type bindFile struct {
	Version string `yaml:"version"`
	ID      string `yaml:"id"`
}

// ResolveProject walks up from cwd looking for the nearest
// .mom-project.yaml file and returns its declared id. Returns
// (id, sourceFile, true, nil) on success.
//
// When no bind file exists in any ancestor, returns ("", "", false, nil)
// — not an error. The caller decides fallback policy.
//
// Per ADR 0016 the resolver picks the LONGEST-ancestor match (the
// nearest file to cwd). It does not validate, warn about, or block on
// nesting patterns.
//
// Path canonicalisation (symlinks, /tmp ↔ /private/tmp on macOS) goes
// through pathutil.CanonicalDir, matching the watcher and registry
// invariants.
//
// Returns an error only for I/O problems other than not-found, malformed
// YAML, or a non-pathological-id rule violation.
func ResolveProject(cwd string) (id string, sourceFile string, found bool, err error) {
	canonical := pathutil.CanonicalDir(cwd)
	dir := canonical
	for {
		candidate := filepath.Join(dir, BindFilename)
		info, statErr := os.Stat(candidate)
		if statErr == nil && !info.IsDir() {
			id, err = readBindFile(candidate)
			if err != nil {
				return "", "", false, err
			}
			return id, candidate, true, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false, nil
		}
		dir = parent
	}
}

// readBindFile parses the YAML and validates the id.
func readBindFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var bf bindFile
	if err := yaml.Unmarshal(data, &bf); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validateId(bf.ID); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return bf.ID, nil
}

// validateId is the lax sanity check on a project id. Per ADR 0016 the
// data layer does not enforce a strict slug regex — users may choose
// whatever string identifies their project (mixed case, spaces, emoji
// all allowed). We reject only pathological values that would cause
// data layer problems.
func validateId(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("project id must not be empty")
	}
	if strings.ContainsAny(id, "\n\r\x00") {
		return fmt.Errorf("project id must not contain newlines or NULL bytes")
	}
	if len(id) > MaxIdLength {
		return fmt.Errorf("project id exceeds max length %d (got %d)", MaxIdLength, len(id))
	}
	return nil
}
