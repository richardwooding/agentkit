package agentkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit/internal/pool"
)

// AsToolOption configures AsTool.
type AsToolOption func(*subAgentTool)

// WithForwardEvents surfaces the child's events in the parent's stream. Every
// child event, its EventFinish included, is forwarded with the child's own
// RunID and Depth and with Parent set to the enclosing run's ID. Without the
// option, or when the parent is not streaming, the child runs silently.
func WithForwardEvents() AsToolOption { return func(t *subAgentTool) { t.forward = true } }

type subAgentArgs struct {
	Input string `json:"input" jsonschema:"the task or question for this agent"`
}

type subAgentTool struct {
	agent   *Agent
	def     core.Tool
	forward bool
}

// Definition implements Tool.
func (t *subAgentTool) Definition() core.Tool { return t.def }

// Sequential implements Sequential: a sub-agent never runs alongside other calls.
func (t *subAgentTool) Sequential() bool { return true }

// Call implements Tool.
func (t *subAgentTool) Call(ctx context.Context, args json.RawMessage) (Output, error) {
	var in subAgentArgs
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return Errorf("invalid arguments: %v", err), fmt.Errorf("%w: %w", ErrInvalidArgs, err)
		}
	}
	res, err := t.run(ctx, []core.Message{core.UserText(in.Input)})
	if err != nil {
		return Errorf("%s failed: %v", t.agent.name, err), err
	}
	return Text(res.Output), nil
}

func (t *subAgentTool) run(ctx context.Context, msgs []core.Message) (*Result, error) {
	emit, parentID, ok := parentEmitter(ctx)
	if !t.forward || !ok {
		return t.agent.RunMessages(ctx, msgs)
	}
	var (
		res    *Result
		runErr error
	)
	for e, err := range t.agent.streamRun(ctx, msgs, runConfig{}, nil) {
		e.Parent = parentID
		if e.Kind == EventFinish {
			e.Err = err
			res, runErr = e.Result, err
		}
		emit(e)
	}
	return res, runErr
}

// AsTool exposes an agent as a tool taking {"input": string}. The child runs
// with its own budget, one level deeper, and only its final text comes back;
// its token usage is added to the parent's.
func AsTool(a *Agent, name, description string, opts ...AsToolOption) Tool {
	schema, _ := SchemaFor[subAgentArgs]()
	t := &subAgentTool{agent: a, def: core.Tool{Name: name, Description: description, Parameters: schema}}
	for _, o := range opts {
		o(t)
	}
	return t
}

// HandoffToolName is the name of the tool Handoff builds.
const HandoffToolName = "handoff"

type handoffTool struct {
	targets map[string]*Agent
	def     core.Tool
}

// Definition implements Tool.
func (t *handoffTool) Definition() core.Tool { return t.def }

// Sequential implements Sequential.
func (t *handoffTool) Sequential() bool { return true }

// Call implements Tool.
func (t *handoffTool) Call(_ context.Context, args json.RawMessage) (Output, error) {
	var in struct {
		Agent  string `json:"agent"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Errorf("invalid arguments: %v", err), fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	target, ok := t.targets[in.Agent]
	if !ok {
		err := fmt.Errorf("%w: unknown handoff target %q", ErrInvalidArgs, in.Agent)
		return Errorf("%v", err), err
	}
	return Output{Content: []core.Part{core.Text("Transferred to " + target.name + ".")}, handoff: target}, nil
}

// Handoff builds a tool that transfers the conversation to one of the target
// agents: the system prompt, tools and model switch while the transcript is
// carried over. Give targets their own Handoff tool to allow handing back.
func Handoff(targets ...*Agent) Tool {
	t := &handoffTool{targets: map[string]*Agent{}}
	names := make([]string, 0, len(targets))
	var desc strings.Builder
	desc.WriteString("Transfer the conversation to another agent. Available agents:")
	for _, a := range targets {
		t.targets[a.name] = a
		names = append(names, fmt.Sprintf("%q", a.name))
		desc.WriteString("\n- " + a.name)
		if a.instructions != "" {
			desc.WriteString(": " + firstLine(a.instructions))
		}
	}
	schema := fmt.Sprintf(`{"type":"object","properties":{"agent":{"type":"string","enum":[%s],"description":"name of the agent to hand off to"},"reason":{"type":"string","description":"why the handoff is needed"}},"required":["agent"],"additionalProperties":false}`, strings.Join(names, ","))
	t.def = core.Tool{Name: HandoffToolName, Description: desc.String(), Parameters: json.RawMessage(schema)}
	return t
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// Map runs fn over items with bounded concurrency and returns results in input
// order. Errors are joined; on cancellation unrun items stay zero and the
// joined error includes ctx.Err().
func Map[In, Out any](ctx context.Context, parallel int, items []In, fn func(context.Context, In) (Out, error)) ([]Out, error) {
	type pair struct {
		out Out
		err error
	}
	pairs, started := pool.Map(ctx, parallel, items, func(ctx context.Context, _ int, in In) pair {
		out, err := fn(ctx, in)
		return pair{out, err}
	})
	outs := make([]Out, len(items))
	var errs []error
	skipped := false
	for i, p := range pairs {
		outs[i] = p.out
		if !started[i] {
			skipped = true
			continue
		}
		if p.err != nil {
			errs = append(errs, fmt.Errorf("item %d: %w", i, p.err))
		}
	}
	if skipped && ctx.Err() != nil {
		errs = append(errs, ctx.Err())
	}
	return outs, errors.Join(errs...)
}
