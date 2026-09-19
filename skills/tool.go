package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

// Tool names registered by Use.
const (
	ToolName     = "skill"
	FileToolName = "skill_file"
)

const (
	maxResourceListing = 50
	maxFileBytes       = 1 << 20
	maxDescLine        = 160
)

// ErrFileTooLarge is returned for bundled files over 1 MiB.
var ErrFileTooLarge = errors.New("skills: file exceeds 1 MiB")

var imageMIMEs = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// Tools returns the activation and file tools for set, or an empty Toolset
// when set has no skills so that nothing is registered.
func Tools(set *Set) agentkit.Toolset {
	if set.Len() == 0 {
		return nil
	}
	return agentkit.Toolset{Tool(set), FileTool(set)}
}

// Tool builds the "skill" tool: given a skill name, it returns the full
// SKILL.md body wrapped in <skill_content> together with a listing of the
// files bundled with the skill. Its results are pinned so they survive
// compaction. The set must not be empty.
func Tool(set *Set) agentkit.Tool {
	if set.Len() == 0 {
		panic("skills: Tool requires a non-empty set")
	}
	var desc strings.Builder
	desc.WriteString("Load the full instructions of a skill before doing a task it covers. Available skills:")
	for _, sk := range set.skills {
		desc.WriteString("\n- " + sk.Name + ": " + firstLine(sk.Description))
	}
	schema := fmt.Sprintf(`{"type":"object","properties":{"name":{"type":"string","enum":%s,"description":"name of the skill to activate"}},"required":["name"],"additionalProperties":false}`, enum(set))
	return pinned{agentkit.Raw(ToolName, desc.String(), json.RawMessage(schema), func(_ context.Context, args json.RawMessage) (agentkit.Output, error) {
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return agentkit.Errorf("invalid arguments: %v", err), err
		}
		sk, ok := set.Lookup(in.Name)
		if !ok {
			return unknown(set, in.Name)
		}
		return agentkit.Text(render(sk)), nil
	})}
}

// FileTool builds the "skill_file" tool, which reads a file bundled with a
// skill by its path relative to the skill directory. Text files are returned
// as text and common image formats as an image part.
func FileTool(set *Set) agentkit.Tool {
	if set.Len() == 0 {
		panic("skills: FileTool requires a non-empty set")
	}
	schema := fmt.Sprintf(`{"type":"object","properties":{"skill":{"type":"string","enum":%s,"description":"name of the skill the file belongs to"},"path":{"type":"string","description":"path relative to the skill directory, e.g. references/REFERENCE.md"}},"required":["skill","path"],"additionalProperties":false}`, enum(set))
	return agentkit.Raw(FileToolName, "Read a file bundled with a skill, such as a reference document, template or script.", json.RawMessage(schema),
		func(_ context.Context, args json.RawMessage) (agentkit.Output, error) {
			var in struct {
				Skill string `json:"skill"`
				Path  string `json:"path"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return agentkit.Errorf("invalid arguments: %v", err), err
			}
			sk, ok := set.Lookup(in.Skill)
			if !ok {
				return unknown(set, in.Skill)
			}
			return readFile(sk, in.Path)
		})
}

type pinned struct{ agentkit.Tool }

// Pinned implements agentkit.Pinned.
func (pinned) Pinned() bool { return true }

func readFile(sk *Skill, name string) (agentkit.Output, error) {
	data, err := sk.ReadFile(name)
	if err != nil {
		return agentkit.Errorf("%v", err), err
	}
	if mime, ok := imageMIMEs[strings.ToLower(path.Ext(name))]; ok {
		return agentkit.Output{Content: []core.Part{core.Image(data, mime)}}, nil
	}
	if !utf8.Valid(data) {
		err := fmt.Errorf("skills: %s is not a text or image file", name)
		return agentkit.Errorf("%v", err), err
	}
	return agentkit.Text(string(data)), nil
}

func unknown(set *Set, name string) (agentkit.Output, error) {
	err := fmt.Errorf("%w: %q (available: %s)", ErrUnknownSkill, name, strings.Join(set.Names(), ", "))
	return agentkit.Errorf("%v", err), err
}

func render(sk *Skill) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<skill_content name=%q>\n", sk.Name)
	b.WriteString(sk.Body)
	b.WriteString("\n\nSkill directory: ")
	b.WriteString(sk.Dir)
	b.WriteString("\nRelative paths in this skill are relative to the skill directory; read them with the " + FileToolName + " tool.")
	files, truncated, err := sk.Resources(maxResourceListing)
	if err == nil && len(files) > 0 {
		b.WriteString("\n\n<skill_resources>\n")
		for _, f := range files {
			b.WriteString("  <file>" + escape(f) + "</file>\n")
		}
		if truncated {
			fmt.Fprintf(&b, "  <note>listing truncated to %d files</note>\n", maxResourceListing)
		}
		b.WriteString("</skill_resources>")
	}
	b.WriteString("\n</skill_content>")
	return b.String()
}

func enum(set *Set) string {
	b, _ := json.Marshal(set.Names())
	return string(b)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > maxDescLine {
		s = s[:maxDescLine] + "…"
	}
	return s
}
