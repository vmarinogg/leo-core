package mcp

// Authorizer determines whether a subject may access a given document.
// This is a stub for future Enterprise (OFFICE) ABAC implementation.
type Authorizer interface {
	// CanAccess returns true if subject is allowed to access the document
	// identified by docID.
	CanAccess(docID, subject string) bool
}

// LocalAuthorizer is the default authorizer for local stdio transport.
// It grants unconditional access — local stdio implies the process owner
// is the same person running the server.
type LocalAuthorizer struct{}

// CanAccess always returns true for local stdio transport (no auth check).
func (LocalAuthorizer) CanAccess(_, _ string) bool { return true }
