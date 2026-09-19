package agentkit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/richardwooding/llmkit"
	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit/internal/pool"
)

const (
	compactProactive     = "proactive"
	compactContextLength = "context_length"
	notExecutedMessage   = "not executed: "
)

type run struct {
	agent   *Agent
	origin  *Agent
	cfg     runConfig
	hooks   Hooks
	emit    func(Event)
	typed   *typedOutput
	runID   string
	depth   int
	start   time.Time
	msgs    []core.Message
	newMsgs []core.Message
	res     Result
	model   core.Usage
	sink    *usageSink
	parent  *usageSink
	stop    error
	handoff *Agent
	nudged  bool
	pins    []pin
}

func newRun(a *Agent, cfg runConfig, emit func(Event), typed *typedOutput) *run {
	return &run{agent: a, origin: a, cfg: cfg, hooks: a.hooks.Join(cfg.hooks), emit: emit, typed: typed, sink: &usageSink{}}
}

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (r *run) execute(ctx context.Context, input []core.Message) (*Result, error) {
	ctx, cancel := r.prepare(ctx, input)
	defer cancel()
	for r.stop == nil {
		if !r.step(ctx) {
			break
		}
	}
	return r.finish(ctx)
}

func (r *run) prepare(ctx context.Context, input []core.Message) (context.Context, context.CancelFunc) {
	r.runID = newRunID()
	r.start = time.Now()
	r.depth = depthFrom(ctx)
	r.parent = parentSink(ctx)
	cancel := context.CancelFunc(func() {})
	if r.origin.budget.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, r.origin.budget.Timeout)
	}
	if r.agent.instructions != "" {
		r.msgs = append(r.msgs, core.System(r.agent.instructions))
	}
	if r.cfg.session != "" && r.origin.store != nil {
		history, err := r.origin.store.Load(ctx, r.cfg.session)
		if err != nil {
			r.stop = fmt.Errorf("agentkit: load session: %w", err)
			r.res.StopReason = StopError
		}
		r.msgs = append(r.msgs, history...)
		r.collectPins(history)
	}
	r.msgs = append(r.msgs, input...)
	r.newMsgs = append(r.newMsgs, input...)
	if r.typed != nil {
		r.typed.decideMode(len(r.agent.tools) > 0)
	}
	r.fireRunStart()
	return withRunState(ctx, r), cancel
}

// step performs one model call and its tool calls; it returns false when the
// run is over.
func (r *run) step(ctx context.Context) bool {
	if r.res.Steps >= r.origin.budget.maxSteps() {
		r.stopWith(StopMaxSteps, &BudgetError{Limit: "steps", Used: r.res.Steps, Max: r.origin.budget.maxSteps()})
		return false
	}
	if err := r.compactProactively(ctx); err != nil {
		r.stopWith(StopError, err)
		return false
	}
	r.drainInbox()
	resp, err := r.callModel(ctx)
	if err != nil {
		r.stopWith(stopReasonFor(err), err)
		return false
	}
	r.res.FinishReason = resp.FinishReason
	r.res.Message = resp.Message
	r.append(resp.Message)
	calls := resp.ToolCalls()
	if len(calls) == 0 {
		return r.noToolCalls(resp)
	}
	return r.toolCalls(ctx, calls)
}

func (r *run) noToolCalls(resp *core.Response) bool {
	if r.typed != nil && r.typed.mode == OutputTool {
		if r.nudged {
			r.stopWith(StopError, ErrNoFinalAnswer)
			return false
		}
		r.nudged = true
		r.append(core.UserText("Call " + FinalAnswerTool + " now with your final answer."))
		return true
	}
	r.res.Output = resp.Text()
	if r.typed != nil {
		if err := r.typed.decodeText(r.res.Output); err != nil {
			r.stopWith(StopError, err)
			return false
		}
		r.res.StopReason = StopFinalAnswer
		return false
	}
	if r.drainInbox() {
		return true
	}
	r.res.StopReason = StopCompleted
	return false
}

// drainInbox appends queued user messages and reports whether there were any.
func (r *run) drainInbox() bool {
	if r.cfg.inbox == nil {
		return false
	}
	msgs := r.cfg.inbox.drain()
	for _, m := range msgs {
		r.append(m)
	}
	return len(msgs) > 0
}

func (r *run) toolCalls(ctx context.Context, calls []core.ToolCall) bool {
	b := r.origin.budget
	if b.MaxTokens > 0 && r.usage().TotalTokens > b.MaxTokens {
		r.append(core.ToolResults(r.skipped(calls, "token budget exceeded")...))
		r.stopWith(StopMaxTokens, &BudgetError{Limit: "tokens", Used: r.usage().TotalTokens, Max: b.MaxTokens})
		return false
	}
	var budgetStop error
	if b.MaxToolCalls > 0 && r.res.ToolCalls+len(calls) > b.MaxToolCalls {
		budgetStop = &BudgetError{Limit: "tool_calls", Used: r.res.ToolCalls + len(calls), Max: b.MaxToolCalls}
	}
	results := r.execTools(ctx, calls)
	r.res.ToolCalls += len(calls)
	r.append(core.ToolResults(results...))
	r.collectPins(r.msgs[len(r.msgs)-1:])
	switch {
	case r.typed != nil && r.typed.done:
		r.res.StopReason = StopFinalAnswer
		return false
	case budgetStop != nil:
		r.stopWith(StopMaxToolCalls, budgetStop)
		return false
	case ctx.Err() != nil:
		r.stopWith(stopReasonFor(ctx.Err()), ctx.Err())
		return false
	case r.handoff != nil:
		return r.switchAgent(r.handoff)
	}
	return true
}

func (r *run) skipped(calls []core.ToolCall, reason string) []core.ToolResult {
	out := make([]core.ToolResult, len(calls))
	for i, c := range calls {
		out[i] = core.ToolResult{CallID: c.ID, Name: c.Name, IsError: true, Content: []core.Part{core.Text(notExecutedMessage + reason)}}
	}
	return out
}

func (r *run) execTools(ctx context.Context, calls []core.ToolCall) []core.ToolResult {
	results := make([]core.ToolResult, len(calls))
	var concurrent, sequential []int
	for i, c := range calls {
		if r.runsConcurrently(c) {
			concurrent = append(concurrent, i)
		} else {
			sequential = append(sequential, i)
		}
	}
	if len(concurrent) > 0 {
		_, started := pool.Map(ctx, r.agent.parallel, concurrent, func(ctx context.Context, _ int, i int) struct{} {
			results[i] = r.callTool(ctx, calls[i])
			return struct{}{}
		})
		for j, i := range concurrent {
			if !started[j] {
				results[i] = r.skipped(calls[i:i+1], ctx.Err().Error())[0]
			}
		}
	}
	for _, i := range sequential {
		if ctx.Err() != nil {
			results[i] = r.skipped(calls[i:i+1], ctx.Err().Error())[0]
			continue
		}
		results[i] = r.callTool(ctx, calls[i])
	}
	return results
}

func (r *run) runsConcurrently(c core.ToolCall) bool {
	if r.agent.parallel <= 1 || c.Name == FinalAnswerTool {
		return false
	}
	t, ok := r.agent.tools.Lookup(c.Name)
	return ok && !isSequential(t)
}

func (r *run) callTool(ctx context.Context, tc core.ToolCall) core.ToolResult {
	call := Call{RunID: r.runID, Agent: r.agent.name, Session: r.cfg.session, Step: r.res.Steps, Depth: r.depth, Call: tc}
	ctx = WithCall(ctx, call)
	var send func(Event)
	if r.emit != nil {
		send = r.send
	}
	ctx = withToolCtx(ctx, tc, send)
	if r.hooks.OnToolCall != nil {
		r.hooks.OnToolCall(ToolCallInfo{Call: call})
	}
	r.send(Event{Kind: EventToolCall, ToolCall: &tc})
	start := time.Now()
	out, err := r.invoke(ctx, tc)
	if err != nil && !out.IsError {
		out = Errorf("%v", err)
	}
	if out.handoff != nil {
		if r.handoff == nil {
			r.handoff = out.handoff
		} else {
			out = Errorf("a handoff to %s was already requested this step", r.handoff.name)
		}
	}
	dur := time.Since(start)
	tr := out.toolResult(tc)
	if r.hooks.OnToolResult != nil {
		r.hooks.OnToolResult(ToolResultInfo{Call: call, Output: out, Err: err, Duration: dur})
	}
	r.send(Event{Kind: EventToolResult, ToolCall: &tc, ToolResult: &tr, Duration: dur, Err: err})
	return tr
}

func (r *run) invoke(ctx context.Context, tc core.ToolCall) (Output, error) {
	if r.typed != nil && tc.Name == FinalAnswerTool {
		return r.typed.record(tc.Arguments)
	}
	t, ok := r.agent.tools.Lookup(tc.Name)
	if !ok {
		err := fmt.Errorf("%w: %q", ErrToolNotFound, tc.Name)
		return Errorf("%v", err), err
	}
	return t.Call(ctx, tc.Arguments)
}

func (r *run) switchAgent(target *Agent) bool {
	r.res.Handoffs++
	info := HandoffInfo{RunID: r.runID, From: r.agent.name, To: target.name}
	if r.hooks.OnHandoff != nil {
		r.hooks.OnHandoff(info)
	}
	r.send(Event{Kind: EventHandoff, Handoff: &info})
	r.handoff = nil
	if r.res.Handoffs > r.origin.budget.maxHandoffs() {
		r.stopWith(StopError, fmt.Errorf("%w: %d handoffs", ErrHandoffLoop, r.res.Handoffs))
		return false
	}
	r.agent = target
	if len(r.msgs) > 0 && r.msgs[0].Role == core.RoleSystem {
		r.msgs = r.msgs[1:]
	}
	if target.instructions != "" {
		r.msgs = append([]core.Message{core.System(target.instructions)}, r.msgs...)
	}
	return true
}

func (r *run) finish(ctx context.Context) (*Result, error) {
	r.res.RunID = r.runID
	r.res.Agent = r.agent.name
	r.res.Messages = r.msgs
	r.res.New = r.newMsgs
	r.res.Usage = r.usage()
	r.res.Duration = time.Since(r.start)
	if r.res.StopReason == "" {
		r.res.StopReason = StopError
	}
	if r.cfg.session != "" && r.origin.store != nil && r.res.Steps > 0 {
		if err := r.origin.store.Append(context.WithoutCancel(ctx), r.cfg.session, r.newMsgs...); err != nil && r.stop == nil {
			r.stop = fmt.Errorf("agentkit: append session: %w", err)
		}
	}
	if r.parent != nil {
		r.parent.add(r.res.Usage)
	}
	if r.hooks.OnRunEnd != nil {
		r.hooks.OnRunEnd(&r.res, r.stop)
	}
	return &r.res, r.stop
}

func (r *run) append(m core.Message) {
	r.msgs = append(r.msgs, m)
	r.newMsgs = append(r.newMsgs, m)
}

func (r *run) stopWith(reason StopReason, err error) {
	r.res.StopReason = reason
	r.stop = err
}

func (r *run) usage() core.Usage { return r.model.Add(r.sink.total()) }

func (r *run) send(e Event) {
	if r.emit == nil {
		return
	}
	e.RunID, e.Agent, e.Step, e.Depth = r.runID, r.agent.name, r.res.Steps, r.depth
	r.emit(e)
}

func (r *run) fireRunStart() {
	if r.hooks.OnRunStart != nil {
		r.hooks.OnRunStart(RunInfo{RunID: r.runID, Agent: r.agent.name, Session: r.cfg.session, Depth: r.depth})
	}
}

func stopReasonFor(err error) StopReason {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return StopDeadline
	case errors.Is(err, context.Canceled):
		return StopCancelled
	default:
		return StopError
	}
}

// buildRequest assembles the request for the current step.
func (r *run) buildRequest() *core.Request {
	req := r.agent.template
	req.Messages = r.visible()
	req.Tools = r.agent.tools.Definitions()
	if r.typed != nil {
		r.typed.apply(&req, r.nudged)
	}
	return &req
}

func (r *run) callModel(ctx context.Context) (*core.Response, error) {
	compacted := false
	for {
		resp, err := r.callModelWithRetry(ctx)
		switch {
		case err == nil:
			return resp, nil
		case errors.Is(err, core.ErrContextLength) && !compacted && r.origin.compactor != nil:
			compacted = true
			if cerr := r.compact(ctx, compactContextLength, r.origin.estimator.Estimate(r.visible())/2); cerr != nil {
				return nil, cerr
			}
		case errors.Is(err, core.ErrUnsupported) && r.typed != nil && r.typed.mode == OutputSchema:
			r.typed.mode = OutputTool
		default:
			return nil, err
		}
	}
}

func (r *run) callModelWithRetry(ctx context.Context) (*core.Response, error) {
	policy := r.agent.retry.withDefaults()
	r.res.Steps++
	if r.hooks.OnStep != nil {
		r.hooks.OnStep(StepInfo{RunID: r.runID, Agent: r.agent.name, Step: r.res.Steps})
	}
	r.send(Event{Kind: EventStep})
	for attempt := 1; ; attempt++ {
		req := r.buildRequest()
		if r.hooks.OnModelCall != nil {
			r.hooks.OnModelCall(ModelCallInfo{RunID: r.runID, Agent: r.agent.name, Step: r.res.Steps, Attempt: attempt, Request: req})
		}
		start := time.Now()
		resp, emitted, err := r.oneModelCall(ctx, req)
		if r.hooks.OnModelResponse != nil {
			r.hooks.OnModelResponse(ModelResponseInfo{RunID: r.runID, Agent: r.agent.name, Step: r.res.Steps, Response: resp, Duration: time.Since(start), Err: err})
		}
		if err == nil {
			r.model = r.model.Add(resp.Usage)
			u := resp.Usage
			r.send(Event{Kind: EventUsage, Usage: &u, Duration: time.Since(start), FinishReason: resp.FinishReason})
			return resp, nil
		}
		if emitted || attempt >= policy.MaxAttempts || !policy.Retryable(err) {
			return nil, err
		}
		delay := policy.Delay(attempt, err)
		if r.hooks.OnRetry != nil {
			r.hooks.OnRetry(RetryInfo{RunID: r.runID, Agent: r.agent.name, Step: r.res.Steps, Attempt: attempt, Delay: delay, Err: err})
		}
		r.send(Event{Kind: EventRetry, Attempt: attempt, Delay: delay, Err: err})
		if err := sleepCtx(ctx, delay); err != nil {
			return nil, err
		}
	}
}

// oneModelCall streams when the run is streaming and the client supports it;
// emitted reports whether any delta reached the consumer, which rules out a retry.
func (r *run) oneModelCall(ctx context.Context, req *core.Request) (resp *core.Response, emitted bool, err error) {
	if r.emit == nil || r.agent.stream == nil {
		resp, err = r.agent.chat.Chat(ctx, req)
		if err == nil && r.emit != nil && resp.Text() != "" {
			r.send(Event{Kind: EventText, Text: resp.Text()})
		}
		return resp, false, err
	}
	seq := r.agent.stream.Stream(ctx, req)
	resp, err = llmkit.Collect(func(yield func(core.Chunk, error) bool) {
		for ch, cerr := range seq {
			if cerr == nil {
				emitted = r.emitChunk(ch) || emitted
			}
			if !yield(ch, cerr) {
				return
			}
		}
	})
	return resp, emitted, err
}

func (r *run) emitChunk(ch core.Chunk) bool {
	switch ch.Kind {
	case core.ChunkText:
		r.send(Event{Kind: EventText, Text: ch.Text})
		return true
	case core.ChunkReasoning:
		r.send(Event{Kind: EventReasoning, Text: ch.Text})
		return true
	default:
		return false
	}
}

func (r *run) compactProactively(ctx context.Context) error {
	if r.origin.window <= 0 || r.origin.compactor == nil {
		return nil
	}
	if r.origin.estimator.Estimate(r.visible()) <= r.origin.window*9/10 {
		return nil
	}
	return r.compact(ctx, compactProactive, r.origin.window*7/10)
}

func (r *run) compact(ctx context.Context, reason string, budget int) error {
	before := r.origin.estimator.Estimate(r.visible())
	msgs, err := r.origin.compactor.Compact(ctx, r.msgs, budget)
	if err != nil {
		return fmt.Errorf("agentkit: compact history: %w", err)
	}
	r.msgs = msgs
	after := r.origin.estimator.Estimate(r.visible())
	if r.hooks.OnCompact != nil {
		r.hooks.OnCompact(CompactInfo{RunID: r.runID, Agent: r.agent.name, Reason: reason, Before: before, After: after})
	}
	r.send(Event{Kind: EventCompact, Before: before, After: after})
	return nil
}
