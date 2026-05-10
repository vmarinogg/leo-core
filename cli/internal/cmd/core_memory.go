package cmd

type generatedCentralDoc struct {
	DirName string
	Kind    string
	Name    string
}

var knownGeneratedCentralDocs = []generatedCentralDoc{
	{DirName: "constraints", Kind: "constraint", Name: "anti-hallucination"},
	{DirName: "constraints", Kind: "constraint", Name: "escalation-triggers"},
	{DirName: "skills", Kind: "skill", Name: "session-wrap-up"},
}

// defaultIdentity returns the default identity.json content.
func defaultIdentity() string {
	return `{
  "what": "MOM (Memory Oriented Machine) — a living knowledge infrastructure where humans and agents think, decide, and evolve together.",
  "philosophy": "MOM is the memory and knowledge layer above any AI harness. The harness handles task execution; MOM handles persistence, governance, and organizational knowledge. What the harness forgets, MOM remembers.",
  "constraints": [
    "All memory content is JSON — harness files (CLAUDE.md, AGENTS.md) are generated artifacts",
    "Core artifacts are English only — interaction language is personal choice",
    "No rule change without explicit approval from the user",
    "Scripts must never require AI tokens — if it's deterministic, it's a script"
  ]
}`
}
