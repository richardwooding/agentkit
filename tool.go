package agentkit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/richardwooding/llmkit/core"
)

// Tool is anything the model may call.
type Tool interface {
	Definition() core.Tool
	Call(ctx context.Context, args json.RawMessage) (Output, error)
}

// Sequential is implemented by tools that must not run concurrently with other
// tool calls from the same step. Serial adds it; MCP tools may declare it.
type Sequential interface {
	Sequential() bool
}

// Output is what a tool hands back to the model. IsError marks a tool-level
// failure the model should see; a Go error from Call means the same and is also
// reported to Hooks.
type Output struct {
	Content []core.Part
	IsError bool

	handoff *Agent
}

// Text builds a Output with one text part.
func Text(s string) Output { return Output{Content: []core.Part{core.Text(s)}} }

// Errorf builds an error Output the model will see.
func Errorf(format string, args ...any) Output {
	return Output{Content: []core.Part{core.Text(fmt.Sprintf(format, args...))}, IsError: true}
}

// JSON builds a Output whose text is v encoded as JSON.
func JSON(v any) (Output, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return Output{}, fmt.Errorf("agentkit: encode result: %w", err)
	}
	return Text(string(b)), nil
}

// Text returns the concatenated text parts of the result.
func (r Output) Text() string {
	var b strings.Builder
	for _, p := range r.Content {
		if t, ok := p.(core.TextPart); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func (r Output) toolResult(call core.ToolCall) core.ToolResult {
	content := r.Content
	if len(content) == 0 {
		content = []core.Part{core.Text("ok")}
	}
	return core.ToolResult{CallID: call.ID, Name: call.Name, Content: content, IsError: r.IsError}
}

// Call is the per-invocation context a tool can read via CallFrom.
type Call struct {
	RunID   string
	Agent   string
	Session string
	Step    int
	Depth   int
	Call    core.ToolCall
}

type callKey struct{}

// CallFrom returns the Call metadata the loop attached to ctx.
func CallFrom(ctx context.Context) (Call, bool) {
	c, ok := ctx.Value(callKey{}).(Call)
	return c, ok
}

// WithCall attaches Call metadata to ctx, for invoking tools outside a run.
func WithCall(ctx context.Context, c Call) context.Context {
	return context.WithValue(ctx, callKey{}, c)
}

type rawTool struct {
	def core.Tool
	fn  func(context.Context, json.RawMessage) (Output, error)
}

// Definition implements Tool.
func (t *rawTool) Definition() core.Tool { return t.def }

// Call implements Tool.
func (t *rawTool) Call(ctx context.Context, args json.RawMessage) (Output, error) {
	return t.fn(ctx, args)
}

// Raw builds a Tool from a hand-written JSON Schema.
func Raw(name, description string, params json.RawMessage, fn func(context.Context, json.RawMessage) (Output, error)) Tool {
	if len(params) == 0 {
		params = json.RawMessage(`{"type":"object"}`)
	}
	return &rawTool{def: core.Tool{Name: name, Description: description, Parameters: params}, fn: fn}
}

// Toolset is an ordered list of tools.
type Toolset []Tool

// Lookup finds a tool by name.
func (ts Toolset) Lookup(name string) (Tool, bool) {
	for _, t := range ts {
		if t.Definition().Name == name {
			return t, true
		}
	}
	return nil, false
}

// Names lists tool names in order.
func (ts Toolset) Names() []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Definition().Name
	}
	return out
}

// Definitions returns the core.Tool definitions in order.
func (ts Toolset) Definitions() []core.Tool {
	out := make([]core.Tool, len(ts))
	for i, t := range ts {
		out[i] = t.Definition()
	}
	return out
}

// Prefix returns a Toolset whose tool names carry the given prefix.
func (ts Toolset) Prefix(p string) Toolset {
	out := make(Toolset, len(ts))
	for i, t := range ts {
		out[i] = Rename(t, p+t.Definition().Name)
	}
	return out
}

// Merge concatenates toolsets, failing on duplicate names.
func Merge(sets ...Toolset) (Toolset, error) {
	var out Toolset
	seen := map[string]bool{}
	for _, set := range sets {
		for _, t := range set {
			name := t.Definition().Name
			if seen[name] {
				return nil, fmt.Errorf("%w: %q", ErrDuplicateTool, name)
			}
			seen[name] = true
			out = append(out, t)
		}
	}
	return out, nil
}

type renamed struct {
	Tool
	name string
}

// Definition implements Tool.
func (r renamed) Definition() core.Tool {
	d := r.Tool.Definition()
	d.Name = r.name
	return d
}

// Sequential implements Sequential.
func (r renamed) Sequential() bool { return isSequential(r.Tool) }

// Rename returns t exposed under a different name.
func Rename(t Tool, name string) Tool { return renamed{Tool: t, name: name} }

func isSequential(t Tool) bool {
	s, ok := t.(Sequential)
	return ok && s.Sequential()
}
