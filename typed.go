package agentkit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/richardwooding/llmkit/core"
)

// OutputMode selects how Run[T] obtains structured output.
type OutputMode uint8

// Output modes.
const (
	OutputAuto   OutputMode = iota // schema for tool-less agents, tool otherwise
	OutputSchema                   // provider json_schema response format
	OutputTool                     // synthetic final_answer tool
)

// FinalAnswerTool is the name of the tool Run[T] injects in OutputTool mode.
const FinalAnswerTool = "final_answer"

const finalAnswerRecorded = "recorded"

type typedOutput struct {
	schema   json.RawMessage
	resolved *jsonschema.Resolved
	mode     OutputMode
	decode   func(json.RawMessage) error
	done     bool
}

func (t *typedOutput) decideMode(hasTools bool) {
	if t.mode != OutputAuto {
		return
	}
	if hasTools {
		t.mode = OutputTool
	} else {
		t.mode = OutputSchema
	}
}

func (t *typedOutput) apply(req *core.Request, nudged bool) {
	switch t.mode {
	case OutputSchema:
		req.Format = &core.ResponseFormat{Type: core.FormatJSONSchema, Name: "output", Schema: t.schema}
	case OutputTool:
		req.Tools = append(req.Tools, core.Tool{
			Name:        FinalAnswerTool,
			Description: "Return the final answer once the task is complete. Call this exactly once, as the last step.",
			Parameters:  t.schema,
		})
		if nudged {
			req.ToolChoice = core.ToolChoice{Mode: core.ToolChoiceNamed, Name: FinalAnswerTool}
		}
	default:
	}
}

func (t *typedOutput) record(args json.RawMessage) (Output, error) {
	if err := t.validate(args); err != nil {
		return Errorf("invalid final answer: %v", err), fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	if err := t.decode(args); err != nil {
		return Errorf("invalid final answer: %v", err), fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	t.done = true
	return Text(finalAnswerRecorded), nil
}

func (t *typedOutput) decodeText(text string) error {
	raw := json.RawMessage(stripFences(text))
	if err := t.validate(raw); err != nil {
		return fmt.Errorf("%w: %w", ErrNoFinalAnswer, err)
	}
	if err := t.decode(raw); err != nil {
		return fmt.Errorf("%w: %w", ErrNoFinalAnswer, err)
	}
	t.done = true
	return nil
}

func (t *typedOutput) validate(raw json.RawMessage) error {
	if t.resolved == nil {
		return nil
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return err
	}
	return t.resolved.Validate(instance)
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(s, "```"))
}

func newTypedOutput[T any](out *T, mode OutputMode) (*typedOutput, error) {
	var cfg funcConfig
	raw, resolved, err := buildSchema[T](&cfg)
	if err != nil {
		return nil, err
	}
	return &typedOutput{
		schema:   raw,
		resolved: resolved,
		mode:     mode,
		decode: func(b json.RawMessage) error {
			var v T
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			*out = v
			return nil
		},
	}, nil
}

// Run drives the agent and decodes its final answer into T. Agents with tools
// receive a final_answer tool; tool-less agents get a JSON-schema response
// format, falling back to the tool when the provider lacks it.
func Run[T any](ctx context.Context, a *Agent, input string, opts ...RunOption) (T, *Result, error) {
	cfg := applyRunOptions(opts)
	return RunMessages[T](ctx, a, []core.Message{userMessage(input, cfg.parts)}, opts...)
}

// RunMessages is Run[T] with caller-built messages.
func RunMessages[T any](ctx context.Context, a *Agent, msgs []core.Message, opts ...RunOption) (T, *Result, error) {
	var out T
	cfg := applyRunOptions(opts)
	typed, err := newTypedOutput(&out, cfg.outputMode)
	if err != nil {
		return out, nil, err
	}
	r := newRun(a, cfg, nil, typed)
	res, err := r.execute(ctx, msgs)
	if err == nil && !typed.done {
		err = ErrNoFinalAnswer
	}
	return out, res, err
}
