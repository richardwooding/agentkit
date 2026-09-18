package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

const (
	maxNameLen    = 64
	emptyObject   = `{}`
	defaultSchema = `{"type":"object"}`
)

type tool struct {
	session    *sdk.ClientSession
	def        core.Tool
	original   string
	sequential bool
}

func newTool(session *sdk.ClientSession, t *sdk.Tool, cfg config) *tool {
	return &tool{
		session: session,
		def: core.Tool{
			Name:        sanitize(cfg.prefix + t.Name),
			Description: t.Description,
			Parameters:  schema(t.InputSchema),
		},
		original:   t.Name,
		sequential: cfg.sequential,
	}
}

// Definition implements agentkit.Tool.
func (t *tool) Definition() core.Tool { return t.def }

// Sequential implements agentkit.Sequential.
func (t *tool) Sequential() bool { return t.sequential }

// Call implements agentkit.Tool.
func (t *tool) Call(ctx context.Context, args json.RawMessage) (agentkit.Output, error) {
	if trimmed := bytes.TrimSpace(args); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		args = json.RawMessage(emptyObject)
	}
	res, err := t.session.CallTool(ctx, &sdk.CallToolParams{Name: t.original, Arguments: args})
	if err != nil {
		return agentkit.Output{}, fmt.Errorf("mcp: call tool %s: %w", t.original, err)
	}
	out := agentkit.Output{Content: parts(res), IsError: res.IsError}
	if res.IsError {
		return out, fmt.Errorf("mcp: tool %s reported an error: %s", t.original, out.Text())
	}
	return out, nil
}

func schema(in any) json.RawMessage {
	if in == nil {
		return json.RawMessage(defaultSchema)
	}
	b, err := json.Marshal(in)
	if err != nil || len(b) == 0 || string(b) == "null" || string(b) == emptyObject {
		return json.RawMessage(defaultSchema)
	}
	return b
}

func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		if b.Len() == maxNameLen {
			break
		}
		if isNameRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func isNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}

func parts(res *sdk.CallToolResult) []core.Part {
	out := make([]core.Part, 0, len(res.Content))
	for _, c := range res.Content {
		if p, ok := part(c); ok {
			out = append(out, p)
		}
	}
	if len(out) == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			out = append(out, core.Text(string(b)))
		}
	}
	return out
}

func part(c sdk.Content) (core.Part, bool) {
	switch c := c.(type) {
	case *sdk.TextContent:
		return core.Text(c.Text), true
	case *sdk.ImageContent:
		return core.Image(c.Data, c.MIMEType), true
	case *sdk.AudioContent:
		return core.Audio(c.Data, c.MIMEType), true
	case *sdk.EmbeddedResource:
		return resourcePart(c.Resource)
	case *sdk.ResourceLink:
		return core.Text(c.URI), true
	default:
		return nil, false
	}
}

func resourcePart(r *sdk.ResourceContents) (core.Part, bool) {
	if r == nil {
		return nil, false
	}
	if len(r.Blob) == 0 {
		return core.Text(r.Text), true
	}
	return core.File(r.Blob, r.MIMEType, r.URI), true
}
