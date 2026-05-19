// Package harness defines the Adapter interface for AI Harness integrations.
package harness

// Config represents the user's .mom/config.yaml configuration.
type Config struct {
	Version  string
	User     UserConfig
	HasMCP   bool
	Delivery string // "mcp" (default) or "context-file"
}

// UserConfig holds user preferences.
type UserConfig struct {
	Language          string
	Autonomy          string
	CommunicationMode string
}

// Constraint represents a memory constraint document.
type Constraint struct {
	ID      string
	Summary string
	Tags    []string
}

// Skill represents a memory skill document.
type Skill struct {
	ID      string
	Summary string
	Tags    []string
}

// Identity represents the .mom/identity.json file.
type Identity struct {
	What        string
	Philosophy  string
	Constraints []string
}

// AdapterCapability describes which MRP v0 events an adapter natively supports.
// Loaded from the adapter's embedded YAML capability file.
type AdapterCapability struct {
	// Name is the adapter identifier (matches Name()).
	Name string `yaml:"adapter"`
	// Version is the adapter version string.
	Version string `yaml:"version"`
	// Supports lists MRP events natively supported by this adapter.
	Supports []string `yaml:"supports"`
	// Experimental lists MRP events emitted best-effort — may fire unreliably.
	Experimental []string `yaml:"experimental"`
}

// Tier classifies a Harness's integration quality with MOM.
type Tier int

const (
	// Functional integration: minimal surface, automation may be unreliable.
	Functional Tier = iota
	// Fluent integration: standard idioms (hooks, settings files), good fidelity.
	Fluent
	// Native integration: programmable extensibility, full feature exposure.
	Native
)

// String returns the lowercase tier name.
func (t Tier) String() string {
	switch t {
	case Native:
		return "native"
	case Fluent:
		return "fluent"
	case Functional:
		return "functional"
	default:
		return "unknown"
	}
}

// HookDef defines a hook to register with the Harness.
type HookDef struct {
	Event   string // e.g. "PostToolUse"
	Matcher string // e.g. "Write"
	Command string
}

// Adapter is the interface that Harness integrations must implement.
// Each Harness (Claude, Codex, Pi) provides an Adapter
// that reads from .mom/ and generates Harness-specific files.
type Adapter interface {
	// Name returns the Harness identifier (e.g. "claude", "codex", "pi").
	Name() string

	// Tier returns the Harness's integration quality classification.
	Tier() Tier

	// GenerateContextFile generates the Harness's boot file
	// (e.g. CLAUDE.md, AGENTS.md) from MOM's config,
	// constraints, skills, and identity.
	GenerateContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error

	// DetectHarness checks whether this Harness is present in the project.
	DetectHarness() bool

	// GeneratedFiles returns the list of file paths (relative to project root)
	// that this adapter generates. Used by uninstall to clean up.
	GeneratedFiles() []string

	// GeneratedDirs returns directories (relative to project root) that this
	// adapter creates and that can be removed if empty after file cleanup.
	GeneratedDirs() []string

	// Watermark returns the header comment inserted into generated files.
	// Used to distinguish MOM-generated files from user-created ones.
	Watermark() string

	// Capabilities returns the MRP v0 capability declaration for this adapter.
	// Loaded from the embedded YAML file in capabilities/.
	Capabilities() AdapterCapability

	// RegisterMCP registers the MOM MCP server config for this Harness.
	RegisterMCP() error
}

// HookInstaller is optionally implemented by adapters whose Harness has a
// hook system. The adapter owns its hook list internally.
type HookInstaller interface {
	RegisterHooks() error
}

// TranscriptSource is optionally implemented by adapters whose Harness
// emits a transcript file or directory the watcher should tail.
type TranscriptSource interface {
	// DefaultTranscriptDir returns the path (tilde-expanded if needed) where
	// the Harness writes transcripts. Empty string means none.
	DefaultTranscriptDir() string
}
