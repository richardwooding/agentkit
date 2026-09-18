package agentkit

import (
	"time"

	"github.com/richardwooding/llmkit/core"
)

// Hooks receive callbacks during a run. Every field is optional. Tool hooks may
// fire from worker goroutines when WithParallel is above one.
type Hooks struct {
	OnRunStart      func(RunInfo)
	OnStep          func(StepInfo)
	OnModelCall     func(ModelCallInfo)
	OnModelResponse func(ModelResponseInfo)
	OnToolCall      func(ToolCallInfo)
	OnToolResult    func(ToolResultInfo)
	OnRetry         func(RetryInfo)
	OnCompact       func(CompactInfo)
	OnHandoff       func(HandoffInfo)
	OnRunEnd        func(*Result, error)
}

// RunInfo describes a starting run.
type RunInfo struct {
	RunID   string
	Agent   string
	Session string
	Depth   int
}

// StepInfo describes the start of one model call.
type StepInfo struct {
	RunID string
	Agent string
	Step  int
}

// ModelCallInfo describes a request about to be sent; treat Request as read-only.
type ModelCallInfo struct {
	RunID   string
	Agent   string
	Step    int
	Attempt int
	Request *core.Request
}

// ModelResponseInfo describes a completed model call.
type ModelResponseInfo struct {
	RunID    string
	Agent    string
	Step     int
	Response *core.Response
	Duration time.Duration
	Err      error
}

// ToolCallInfo describes a tool about to run.
type ToolCallInfo struct {
	Call Call
}

// ToolResultInfo describes a finished tool call.
type ToolResultInfo struct {
	Call     Call
	Output   Output
	Err      error
	Duration time.Duration
}

// RetryInfo describes a model-call retry.
type RetryInfo struct {
	RunID   string
	Agent   string
	Step    int
	Attempt int
	Delay   time.Duration
	Err     error
}

// CompactInfo describes a history compaction; Reason is "proactive" or
// "context_length".
type CompactInfo struct {
	RunID  string
	Agent  string
	Reason string
	Before int
	After  int
}

// HandoffInfo describes control passing to another agent.
type HandoffInfo struct {
	RunID  string
	From   string
	To     string
	Reason string
}

// Join returns hooks that call h then o.
func (h Hooks) Join(o Hooks) Hooks {
	return Hooks{
		OnRunStart:      join2(h.OnRunStart, o.OnRunStart),
		OnStep:          join2(h.OnStep, o.OnStep),
		OnModelCall:     join2(h.OnModelCall, o.OnModelCall),
		OnModelResponse: join2(h.OnModelResponse, o.OnModelResponse),
		OnToolCall:      join2(h.OnToolCall, o.OnToolCall),
		OnToolResult:    join2(h.OnToolResult, o.OnToolResult),
		OnRetry:         join2(h.OnRetry, o.OnRetry),
		OnCompact:       join2(h.OnCompact, o.OnCompact),
		OnHandoff:       join2(h.OnHandoff, o.OnHandoff),
		OnRunEnd:        joinEnd(h.OnRunEnd, o.OnRunEnd),
	}
}

func join2[T any](a, b func(T)) func(T) {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	return func(v T) { a(v); b(v) }
}

func joinEnd(a, b func(*Result, error)) func(*Result, error) {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	return func(r *Result, err error) { a(r, err); b(r, err) }
}
