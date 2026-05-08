package librarian

import "errors"

// ErrNotFound is returned by Get when no matching row exists. It is
// only returned when the underlying scan completed with no rows AND
// rows.Err() reported no error — a mid-iteration scan failure is
// surfaced as the underlying error, never silently translated to a
// miss.
var ErrNotFound = errors.New("librarian: not found")

// ErrSubstanceImmutable is returned when an attempt is made to write a
// substance field through a path reserved for operational updates
// (per ADR 0011). Substance fields: id, content, summary, created_at,
// session_id, provenance_actor, provenance_source_type,
// provenance_trigger_event.
var ErrSubstanceImmutable = errors.New("librarian: substance field is immutable")

// ErrEmptyArg is returned when a required identifier (tag name, entity
// type, entity display_name, session_id, etc.) is empty after
// trimming. Empty strings are upstream bugs; Librarian fails loud
// rather than persisting zombie rows.
var ErrEmptyArg = errors.New("librarian: required argument is empty")

// ErrSelfMerge is returned by MergeTags when source == target. Without
// the guard, MergeTags("recall", "recall") wipes every memory_tags
// edge and deletes the tag itself. Comparison is case-sensitive so
// real renames like "mcp" → "MCP" still work.
var ErrSelfMerge = errors.New("librarian: cannot merge a tag into itself")
