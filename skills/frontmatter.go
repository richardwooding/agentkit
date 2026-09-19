package skills

import (
	"fmt"
	"strings"
)

const (
	delimiter          = "---"
	fieldName          = "name"
	fieldDescription   = "description"
	fieldLicense       = "license"
	fieldCompatibility = "compatibility"
	fieldMetadata      = "metadata"
	fieldAllowedTools  = "allowed-tools"
)

// Parse reads one SKILL.md: YAML frontmatter between "---" lines, then the
// Markdown body. The frontmatter parser covers what skills use in practice:
// "key: value" with plain, quoted or block (| and >) scalars, "#" comments,
// and a one-level mapping for metadata. Unquoted values may contain colons.
// Unknown keys are ignored; metadata values that are not scalars are dropped.
// Parse fails when name or description is missing or empty.
func Parse(data []byte) (*Skill, error) {
	front, body, err := splitFrontmatter(string(data))
	if err != nil {
		return nil, err
	}
	fm := parseMapping(front)
	s := &Skill{
		Name:          fm.scalars[fieldName],
		Description:   fm.scalars[fieldDescription],
		License:       fm.scalars[fieldLicense],
		Compatibility: fm.scalars[fieldCompatibility],
		Metadata:      fm.maps[fieldMetadata],
		AllowedTools:  strings.Fields(fm.scalars[fieldAllowedTools]),
		Body:          body,
	}
	if s.Name == "" {
		return nil, fmt.Errorf("%w: %s", ErrMissingField, fieldName)
	}
	if s.Description == "" {
		return nil, fmt.Errorf("%w: %s", ErrMissingField, fieldDescription)
	}
	return s, nil
}

// splitFrontmatter returns the lines between the delimiters and the trimmed body.
func splitFrontmatter(text string) (front []string, body string, err error) {
	text = strings.TrimPrefix(text, "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != delimiter {
		return nil, "", ErrNoFrontmatter
	}
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimRight(lines[i], " \t"); t == delimiter || t == "..." {
			return lines[1:i], strings.TrimSpace(strings.Join(lines[i+1:], "\n")), nil
		}
	}
	return nil, "", fmt.Errorf("%w: no closing %s", ErrNoFrontmatter, delimiter)
}

type frontmatter struct {
	scalars map[string]string
	maps    map[string]map[string]string
}

// parseMapping reads top-level "key: value" entries. A key with no inline value
// is followed either by an indented mapping or by nothing (empty string).
func parseMapping(lines []string) frontmatter {
	fm := frontmatter{scalars: map[string]string{}, maps: map[string]map[string]string{}}
	for i := 0; i < len(lines); {
		line := lines[i]
		if skippable(line) || indent(line) > 0 {
			i++
			continue
		}
		key, rest, ok := splitKey(line)
		if !ok {
			i++
			continue
		}
		block := blockLines(lines, i+1)
		switch {
		case isBlockIndicator(rest):
			fm.scalars[key] = blockScalar(rest, block)
		case rest == "" && len(block) > 0:
			fm.maps[key] = nestedMapping(block)
		default:
			fm.scalars[key] = scalar(rest)
		}
		i += 1 + len(block)
	}
	return fm
}

// nestedMapping reads the indented "key: value" lines under a top-level key.
// Deeper nesting and sequences are dropped.
func nestedMapping(lines []string) map[string]string {
	out := map[string]string{}
	base := -1
	for i := 0; i < len(lines); {
		line := lines[i]
		if skippable(line) {
			i++
			continue
		}
		if base < 0 {
			base = indent(line)
		}
		key, rest, ok := splitKey(line)
		block := blockLines(lines, i+1)
		if ok && indent(line) == base {
			if isBlockIndicator(rest) {
				out[key] = blockScalar(rest, block)
			} else if rest != "" {
				out[key] = scalar(rest)
			}
		}
		i += 1 + len(block)
	}
	return out
}

// blockLines returns the run of lines from start that are blank or indented
// more than the line before start, i.e. the body of a block scalar or mapping.
func blockLines(lines []string, start int) []string {
	if start <= 0 || start > len(lines) {
		return nil
	}
	parent := indent(lines[start-1])
	end := start
	for end < len(lines) && (strings.TrimSpace(lines[end]) == "" || indent(lines[end]) > parent) {
		end++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func splitKey(line string) (key, rest string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "- ") {
		return "", "", false
	}
	i := strings.Index(trimmed, ":")
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(trimmed[:i])
	rest = strings.TrimSpace(trimmed[i+1:])
	if strings.ContainsAny(key, " \t\"'") {
		return "", "", false
	}
	return key, rest, true
}

func isBlockIndicator(rest string) bool {
	return rest != "" && (rest[0] == '|' || rest[0] == '>') && strings.Trim(rest[1:], "+-0123456789") == ""
}

// blockScalar joins the indented lines of a | or > scalar. Literal blocks keep
// line breaks; folded blocks join lines with spaces and keep blank lines as
// breaks. Trailing newlines are trimmed in both cases.
func blockScalar(indicator string, lines []string) string {
	base := -1
	for _, l := range lines {
		if strings.TrimSpace(l) != "" && (base < 0 || indent(l) < base) {
			base = indent(l)
		}
	}
	parts := make([]string, len(lines))
	for i, l := range lines {
		if len(l) >= base && base >= 0 {
			parts[i] = l[base:]
		} else {
			parts[i] = strings.TrimSpace(l)
		}
	}
	if indicator[0] == '|' {
		return strings.TrimRight(strings.Join(parts, "\n"), "\n")
	}
	var b strings.Builder
	for i, p := range parts {
		switch {
		case p == "":
			b.WriteByte('\n')
		case i > 0 && parts[i-1] != "":
			b.WriteByte(' ')
			b.WriteString(p)
		default:
			b.WriteString(p)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// scalar decodes an inline value: double quotes with escapes, single quotes
// with ” as the escape, or plain text taken verbatim up to a " #" comment.
func scalar(v string) string {
	switch {
	case len(v) >= 2 && v[0] == '"':
		return unquoteDouble(v[1:])
	case len(v) >= 2 && v[0] == '\'':
		return unquoteSingle(v[1:])
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

func unquoteDouble(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '"':
			return b.String()
		case c == '\\' && i+1 < len(v):
			i++
			switch v[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(v[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func unquoteSingle(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '\'' {
			b.WriteByte(v[i])
			continue
		}
		if i+1 < len(v) && v[i+1] == '\'' {
			b.WriteByte('\'')
			i++
			continue
		}
		return b.String()
	}
	return b.String()
}

func skippable(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

func indent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}
