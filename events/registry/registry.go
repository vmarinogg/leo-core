// Package registry is the Schema Registry for canonical events
// (ADR 0018 + ADR 0019). It loads JSON schemas from disk, validates
// filenames against the family.subject.verb taxonomy, and offers a
// runtime Validate API used by the Editor (ADR 0020) before publish.
//
// Governance is level B (ADR 0019):
//   - CI rejects mis-named or malformed schema files at PR time.
//   - Runtime is permissive: missing required fields and type mismatches
//     produce a non-nil Result whose Marker() is true; unknown fields
//     are tolerated and surfaced as warnings. Callers (Editor) attach
//     the _schema_violation marker to the event but still publish.
//
// Schema files live at:
//
//	events/registry/schemas/<family>/<subject>.<verb>.json
//
// The filename — not a field inside the JSON — is the source of truth
// for the event name. The directory name is the family.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// EventNameRegex is the canonical pattern every registered event must
// match: family.subject.verb where family is one of the v1 set, and
// subject/verb are lowercase alphanumeric (underscores allowed).
//
// The `bootstrap` family is reserved (ADR 0018) but not yet accepted
// by the registry — Cartographer revival flips this when it lands.
var EventNameRegex = regexp.MustCompile(
	`^(capture|lifecycle|interaction|operational)\.[a-z0-9_]+\.[a-z0-9_]+$`,
)

// AllowedFamiliesV1 is the closed set of families accepted by the v1
// registry. Adding a family requires an ADR update.
var AllowedFamiliesV1 = []string{"capture", "lifecycle", "interaction", "operational"}

// AllowedFieldTypes is the closed set of JSON-shaped type names a
// schema may declare for a field.
var AllowedFieldTypes = map[string]bool{
	"string": true,
	"number": true,
	"bool":   true,
	"array":  true,
	"object": true,
}

// Schema is a single registered event schema. The wire format is the
// JSON file under events/registry/schemas/<family>/<subject>.<verb>.json;
// Name comes from the filename, not from the JSON, but is duplicated
// inside the file for human readability and round-trip clarity.
type Schema struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Fields      map[string]FieldSpec `json:"fields"`
}

// FieldSpec declares the type and required/optional shape of a single
// payload field. Values, when non-empty, declares a bounded enum
// (string fields only).
type FieldSpec struct {
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Values   []string `json:"values,omitempty"`
}

// Registry is an in-memory index of loaded schemas, keyed by event name.
type Registry struct {
	schemas map[string]Schema
}

// Result is the outcome of validating a single event payload. A zero
// Result (Valid=true, no markers) means the event matched its schema.
type Result struct {
	Valid          bool
	UnknownFields  []string       // present in payload, absent from schema — pass-through, warn
	MissingFields  []string       // declared required, absent from payload — marker
	TypeMismatches []TypeMismatch // declared type ≠ payload type — marker; field dropped by caller
	EnumViolations []EnumViolation
}

// TypeMismatch records a field whose runtime type does not match the
// schema's declared type. Caller (Editor) drops the offending field
// from the published event and attaches _schema_violation.
type TypeMismatch struct {
	Field string
	Want  string
	Got   string
}

// EnumViolation records a field declared with a bounded enum whose
// runtime value is outside the enum. Treated as a marker, not a drop.
type EnumViolation struct {
	Field string
	Value string
	Want  []string
}

// Marker reports whether the Result represents a level-B schema
// violation that should attach _schema_violation to the event. Unknown
// fields alone do NOT trigger the marker (they're informational).
func (r Result) Marker() bool {
	return len(r.MissingFields) > 0 || len(r.TypeMismatches) > 0 || len(r.EnumViolations) > 0
}

// Load reads every schema under dir and returns a Registry. Filenames
// must match the family.subject.verb.json pattern; mismatches are
// returned as a non-nil error so CI catches the violation. dir is the
// schemas root directory (i.e. the parent of the family dirs).
func Load(dir string) (*Registry, error) {
	r := &Registry{schemas: make(map[string]Schema)}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		// Empty registry is valid — useful in tests and during
		// the v0.50 bootstrap before any schemas are registered.
		return r, nil
	}
	var loadErrs []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("registry: read schemas dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			// README.md is the documented anchor at the schemas root.
			// Anything else as a stray file is rejected.
			if e.Name() == "README.md" {
				continue
			}
			loadErrs = append(loadErrs, fmt.Sprintf("registry: unexpected non-dir entry %q at schemas root — schemas must live in a family directory", e.Name()))
			continue
		}
		family := e.Name()
		if !isAllowedFamily(family) {
			loadErrs = append(loadErrs, fmt.Sprintf("registry: unknown family directory %q (allowed: %v)", family, AllowedFamiliesV1))
			continue
		}
		familyDir := filepath.Join(dir, family)
		fEntries, err := os.ReadDir(familyDir)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Sprintf("registry: read family dir %s: %v", familyDir, err))
			continue
		}
		for _, fe := range fEntries {
			if fe.IsDir() || !strings.HasSuffix(fe.Name(), ".json") {
				loadErrs = append(loadErrs, fmt.Sprintf("registry: %s/%s — schema files must end in .json and be top-level inside the family dir", family, fe.Name()))
				continue
			}
			expectedNamePart := strings.TrimSuffix(fe.Name(), ".json")
			fullName := family + "." + expectedNamePart
			if !EventNameRegex.MatchString(fullName) {
				loadErrs = append(loadErrs, fmt.Sprintf("registry: %s/%s — composed name %q does not match family.subject.verb regex", family, fe.Name(), fullName))
				continue
			}
			path := filepath.Join(familyDir, fe.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				loadErrs = append(loadErrs, fmt.Sprintf("registry: read %s: %v", path, err))
				continue
			}
			var s Schema
			if err := json.Unmarshal(raw, &s); err != nil {
				loadErrs = append(loadErrs, fmt.Sprintf("registry: parse %s: %v", path, err))
				continue
			}
			if s.Name != fullName {
				loadErrs = append(loadErrs, fmt.Sprintf("registry: %s — schema.name=%q does not match filename-derived %q", path, s.Name, fullName))
				continue
			}
			if err := validateSchemaShape(s); err != nil {
				loadErrs = append(loadErrs, fmt.Sprintf("registry: %s: %v", path, err))
				continue
			}
			if _, dup := r.schemas[s.Name]; dup {
				loadErrs = append(loadErrs, fmt.Sprintf("registry: duplicate schema %q (already registered)", s.Name))
				continue
			}
			r.schemas[s.Name] = s
		}
	}
	if len(loadErrs) > 0 {
		sort.Strings(loadErrs)
		return nil, fmt.Errorf("registry: %d load error(s):\n  %s", len(loadErrs), strings.Join(loadErrs, "\n  "))
	}
	return r, nil
}

// Has reports whether a schema is registered for eventType.
func (r *Registry) Has(eventType string) bool {
	_, ok := r.schemas[eventType]
	return ok
}

// Names returns the registered event names in sorted order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.schemas))
	for n := range r.schemas {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Validate checks payload against the schema for eventType under
// level-B governance (ADR 0019). If eventType is not registered,
// Validate returns Valid=true with no markers — unregistered types
// are tolerated at runtime; CI catches the gap when a producer ships.
//
// Returned Result.UnknownFields is informational; Result.MissingFields,
// TypeMismatches, and EnumViolations are level-B markers. Caller is
// expected to attach _schema_violation to the event when Marker()
// returns true.
func (r *Registry) Validate(eventType string, payload map[string]any) Result {
	s, ok := r.schemas[eventType]
	if !ok {
		return Result{Valid: true}
	}
	res := Result{Valid: true}

	// Required-field check.
	for fieldName, spec := range s.Fields {
		if !spec.Required {
			continue
		}
		if _, present := payload[fieldName]; !present {
			res.MissingFields = append(res.MissingFields, fieldName)
		}
	}

	// Per-field type + enum check.
	for fieldName, value := range payload {
		spec, declared := s.Fields[fieldName]
		if !declared {
			res.UnknownFields = append(res.UnknownFields, fieldName)
			continue
		}
		gotType := runtimeType(value)
		if spec.Type != "" && gotType != "null" && gotType != spec.Type {
			res.TypeMismatches = append(res.TypeMismatches, TypeMismatch{
				Field: fieldName, Want: spec.Type, Got: gotType,
			})
			continue
		}
		if len(spec.Values) > 0 {
			str, _ := value.(string)
			if !contains(spec.Values, str) {
				res.EnumViolations = append(res.EnumViolations, EnumViolation{
					Field: fieldName, Value: str, Want: append([]string(nil), spec.Values...),
				})
			}
		}
	}
	sort.Strings(res.UnknownFields)
	sort.Strings(res.MissingFields)
	sort.Slice(res.TypeMismatches, func(i, j int) bool { return res.TypeMismatches[i].Field < res.TypeMismatches[j].Field })
	sort.Slice(res.EnumViolations, func(i, j int) bool { return res.EnumViolations[i].Field < res.EnumViolations[j].Field })
	res.Valid = !res.Marker()
	return res
}

// validateSchemaShape is the CI-level structural check: every field
// declares an allowed type; enums only apply to string fields.
func validateSchemaShape(s Schema) error {
	if !EventNameRegex.MatchString(s.Name) {
		return fmt.Errorf("schema name %q does not match family.subject.verb", s.Name)
	}
	if len(s.Fields) == 0 {
		return fmt.Errorf("schema %q declares no fields", s.Name)
	}
	for name, spec := range s.Fields {
		if !AllowedFieldTypes[spec.Type] {
			return fmt.Errorf("field %q declares unknown type %q (allowed: %v)", name, spec.Type, sortedKeys(AllowedFieldTypes))
		}
		if len(spec.Values) > 0 && spec.Type != "string" {
			return fmt.Errorf("field %q declares enum Values but Type is %q (enums only valid on string)", name, spec.Type)
		}
	}
	return nil
}

// runtimeType returns the schema-level type name for a JSON-decoded
// runtime value. Numbers, bools, strings, arrays, and objects each
// map to one of AllowedFieldTypes; nil maps to "null".
func runtimeType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case float64, float32, int, int32, int64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func isAllowedFamily(f string) bool {
	for _, a := range AllowedFamiliesV1 {
		if a == f {
			return true
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
