package skills

import "strings"

const promptPreamble = "The following skills provide specialized instructions for specific tasks. " +
	"When a task matches a skill's description, call the " + ToolName + " tool with the skill's name " +
	"to load its full instructions before proceeding. Files a skill refers to can be read with the " +
	FileToolName + " tool."

// Prompt renders the skill catalog for the system prompt: a short instruction on
// how to activate skills followed by an <available_skills> block listing each
// name and description. It returns "" for an empty set so nothing is injected.
func (s *Set) Prompt() string {
	if s.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(promptPreamble)
	b.WriteString("\n\n<available_skills>\n")
	for _, sk := range s.skills {
		b.WriteString("  <skill>\n    <name>")
		b.WriteString(escape(sk.Name))
		b.WriteString("</name>\n    <description>")
		b.WriteString(escape(sk.Description))
		b.WriteString("</description>\n  </skill>\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escape(s string) string { return escaper.Replace(s) }
