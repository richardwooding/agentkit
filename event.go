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
	EventUsage
	EventToolProgress
	EventApprovalRequest
	EventApprovalResult
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
	case EventUsage:
		return "usage"
	case EventToolProgress:
		return "tool_progress"
	case EventApprovalRequest:
		return "approval_request"
	case EventApprovalResult:
		return "approval_result"
	default:
		return "unknown"
	}
}

// Event is one item of Agent.Stream. Events are always delivered on the
// goroutine that ranges over the stream, whatever WithParallel is set to.
// Exactly one EventFinish for the run itself ends a stream and carries the
// Result; on failure it is paired with the error. Events forwarded from a
// sub-agent (AsTool with WithForwardEvents) carry the child's RunID, a Depth
// greater than the run's own and Parent set to the enclosing run's ID; they
// include the child's own EventFinish.
type Event struct {
	Kind   EventKind
	RunID  string
	Agent  string
	Step   int
	Depth  int
	Parent string

	Text         string         // EventText, EventReasoning, EventToolProgress
	ToolCall     *core.ToolCall // tool events and approval events
	ToolResult   *core.ToolResult
	Duration     time.Duration // EventToolResult, EventUsage
	Err          error         // EventRetry, EventToolResult (the tool's Go error)
	Usage        *core.Usage   // EventUsage: this model call only
	FinishReason core.FinishReason
	Decision     *Decision // EventApprovalResult
	Attempt      int
	Delay        time.Duration
	Before       int
	After        int
	Handoff      *HandoffInfo
	Result       *Result
}
