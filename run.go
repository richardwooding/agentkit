package agentkit

import (
	"context"
	"iter"
	"time"

	"github.com/richardwooding/llmkit/core"
)

// StopReason says why a run ended.
type StopReason string

// Stop reasons.
const (
	StopCompleted    StopReason = "completed"
	StopFinalAnswer  StopReason = "final_answer"
	StopMaxSteps     StopReason = "max_steps"
	StopMaxTokens    StopReason = "max_tokens"
	StopMaxToolCalls StopReason = "max_tool_calls"
	StopDeadline     StopReason = "deadline"
	StopCancelled    StopReason = "canceled"
	StopError        StopReason = "error"
)

// Result is the outcome of a run. On error it is partial but always present.
type Result struct {
	RunID        string
	Agent        string
	Output       string
	Message      core.Message
	Messages     []core.Message
	New          []core.Message
	Usage        core.Usage
	Steps        int
	ToolCalls    int
	Handoffs     int
	FinishReason core.FinishReason
	StopReason   StopReason
	Duration     time.Duration
}

// RunOption configures one run.
type RunOption func(*runConfig)

type runConfig struct {
	session    string
	parts      []core.Part
	hooks      Hooks
	outputMode OutputMode
	inbox      *Inbox
}

// WithSession loads history from the agent's Store before the run and appends
// the new messages afterwards.
func WithSession(id string) RunOption { return func(c *runConfig) { c.session = id } }

// WithParts adds parts (images, files) to the user turn built from input.
func WithParts(parts ...core.Part) RunOption {
	return func(c *runConfig) { c.parts = append(c.parts, parts...) }
}

// WithRunHooks adds observers for this run only.
func WithRunHooks(h Hooks) RunOption { return func(c *runConfig) { c.hooks = c.hooks.Join(h) } }

// WithOutputMode overrides how Run[T] obtains structured output.
func WithOutputMode(m OutputMode) RunOption { return func(c *runConfig) { c.outputMode = m } }

func userMessage(input string, parts []core.Part) core.Message {
	all := make([]core.Part, 0, len(parts)+1)
	if input != "" {
		all = append(all, core.Text(input))
	}
	return core.User(append(all, parts...)...)
}

// Run sends input as a user turn and drives the tool loop to completion.
func (a *Agent) Run(ctx context.Context, input string, opts ...RunOption) (*Result, error) {
	cfg := applyRunOptions(opts)
	return a.RunMessages(ctx, []core.Message{userMessage(input, cfg.parts)}, opts...)
}

// RunMessages is Run with caller-built messages appended to the session.
func (a *Agent) RunMessages(ctx context.Context, msgs []core.Message, opts ...RunOption) (*Result, error) {
	r := newRun(a, applyRunOptions(opts), nil, nil)
	return r.execute(ctx, msgs)
}

// Stream is Run yielding events as they happen. The final event is EventFinish.
func (a *Agent) Stream(ctx context.Context, input string, opts ...RunOption) iter.Seq2[Event, error] {
	cfg := applyRunOptions(opts)
	return a.StreamMessages(ctx, []core.Message{userMessage(input, cfg.parts)}, opts...)
}

// StreamMessages is Stream with caller-built messages.
func (a *Agent) StreamMessages(ctx context.Context, msgs []core.Message, opts ...RunOption) iter.Seq2[Event, error] {
	return a.streamRun(ctx, msgs, applyRunOptions(opts), nil)
}

// streamRun executes the run on its own goroutine and yields every event from
// the iterator goroutine, so consumers never see events from pool workers.
// After the consumer breaks, the loop keeps draining so producers never block;
// the run itself is canceled through ctx.
func (a *Agent) streamRun(ctx context.Context, msgs []core.Message, cfg runConfig, typed *typedOutput) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		events := make(chan Event)
		done := make(chan struct{})
		emit := func(e Event) {
			select {
			case events <- e:
			case <-done:
			}
		}
		r := newRun(a, cfg, emit, typed)
		var (
			res    *Result
			runErr error
		)
		go func() {
			defer close(done)
			res, runErr = r.execute(ctx, msgs)
		}()
		stopped, running := false, true
		for running {
			select {
			case e := <-events:
				if !stopped && !yield(e, nil) {
					stopped = true
					cancel()
				}
			case <-done:
				running = false
			}
		}
		if !stopped {
			yield(Event{Kind: EventFinish, RunID: res.RunID, Agent: res.Agent, Depth: r.depth, Result: res}, runErr)
		}
	}
}

func applyRunOptions(opts []RunOption) runConfig {
	var cfg runConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	return cfg
}
