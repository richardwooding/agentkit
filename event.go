package agentkit

import (
	"time"

	"github.com/richardwooding/llmkit/core"
)

// EventKind discriminates streamed run events.
type EventKind uint8

// Event kinds.
const (
	EventText EventKind = iota + 1
	EventReasoning
	EventStep
	EventToolCall
	EventToolResult
	EventRetry
	EventCompact
	EventHandoff
	EventFinish
)

// String returns the kind's name.
func (k EventKind) String() string {
	switch k {
	case EventText:
		return "text"
	case EventReasoning:
		return "reasoning"
	case EventStep:
		return "step"
	case EventToolCall:
		return "tool_call"
	case EventToolResult:
		return "tool_result"
	case EventRetry:
		return "retry"
	case EventCompact:
		return "compact"
	case EventHandoff:
		return "handoff"
	case EventFinish:
		return "finish"
	default:
		return "unknown"
	}
}

// Event is one item of Agent.Stream. Exactly one EventFinish ends a stream and
// carries the Result; on failure it is paired with the error.
type Event struct {
	Kind       EventKind
	RunID      string
	Agent      string
	Step       int
	Text       string
	ToolCall   *core.ToolCall
	ToolResult *core.ToolResult
	Duration   time.Duration
	Attempt    int
	Delay      time.Duration
	Err        error
	Before     int
	After      int
	Handoff    *HandoffInfo
	Result     *Result
}
