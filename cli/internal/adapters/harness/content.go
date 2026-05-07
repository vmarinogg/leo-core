package harness

import (
	"fmt"
	"strings"
)

// BuildMinimalContextContent generates a slim boot file for MCP-first delivery.
// The behavioral protocol is delivered on-demand via mom_status.
func BuildMinimalContextContent() string {
	return `# MOM — Memory Oriented Machine

You have MOM tools via MCP. Call ` + "`mom_status`" + ` at the start of every session.

For memory operations: mom_recall, mom_get, mom_landmarks, mom_record.

Do NOT skip mom_status — it contains your operating instructions.
`
}

// BuildContextContent generates the shared Markdown content used by all adapters.
// Each adapter calls this and writes the result to its specific output file.
func BuildContextContent(config Config, constraints []Constraint, skills []Skill, identity *Identity) string {
	var b strings.Builder

	// Header
	b.WriteString("# MOM — Memory Oriented Machine\n\n")
	if identity != nil {
		b.WriteString(identity.What)
		b.WriteString("\n\n")
	} else {
		b.WriteString("MOM (Memory Oriented Machine) — persistent memory for AI agents. She remembers, so you don't have to.\n\n")
	}

	// Voice
	b.WriteString("## Voice\n\n")
	b.WriteString("You are MOM. Direct, warm, lightly playful. ")
	b.WriteString("You affirm, you don't sell. You remember, you don't instruct. You care, you don't control. ")
	b.WriteString("When a household metaphor works as well as jargon, use the metaphor. ")
	b.WriteString("Dry humor welcome, never silly. No emoji.\n\n")

	// Memory
	b.WriteString("## Memory\n\n")
	b.WriteString("Your memory lives in `.mom/`. Index, constraints, skills, logs — everything you need to recall is here.\n")
	if config.HasMCP {
		b.WriteString("You have MOM tools via MCP — prefer them over raw file reads where available.\n")
	}
	b.WriteString("Read only what you need. Never load everything upfront — that's hoarding, not remembering.\n\n")

	// During work
	b.WriteString("## During work\n\n")
	b.WriteString("- Need context? Check the index by tags, read only the relevant docs\n")
	b.WriteString("- New knowledge goes to `.mom/memory/` as structured JSON\n")
	b.WriteString("- Follow `.mom/schema.json` — every doc needs: id, scope, tags, created, created_by, content\n\n")

	// Constraints
	if len(constraints) > 0 {
		b.WriteString("## Constraints\n\n")
		b.WriteString("Always-active guardrails loaded from memory. Read the full doc when you need detailed guidance.\n\n")
		for _, c := range constraints {
			fmt.Fprintf(&b, "- **%s**: %s → `.mom/constraints/%s.json`\n", c.ID, c.Summary, c.ID)
		}
		b.WriteString("\n")
	}

	// Skills
	if len(skills) > 0 {
		b.WriteString("## Skills\n\n")
		b.WriteString("Composable procedures invoked by trigger or by MOM. Read the full doc for steps and output format.\n\n")
		for _, s := range skills {
			fmt.Fprintf(&b, "- **%s**: %s → `.mom/skills/%s.json`\n", s.ID, s.Summary, s.ID)
		}
		b.WriteString("\n")
	}

	// Language, autonomy, communication-mode directives
	b.WriteString(LanguageInstructions(config.User.Language))
	b.WriteString("\n\n")
	b.WriteString(CommunicationModeInstructions(config.User.CommunicationMode))
	b.WriteString("\n\n")
	b.WriteString(AutonomyInstructions(config.User.Autonomy))
	b.WriteString("\n")

	return b.String()
}
