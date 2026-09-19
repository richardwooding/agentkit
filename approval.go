package agentkit

import (
	"context"
	"encoding/json"
)

// Decision is an Approver's verdict on one tool call. Arguments, when set,
// replace the arguments handed to the tool; the assistant message in the
// transcript keeps what the model wrote.
type Decision struct {
	Allow     bool
	Arguments json.RawMessage
	Reason    string
}

// Allow approves the call as the model made it.
func Allow() Decision { return Decision{Allow: true} }

// AllowWith approves the call with rewritten arguments.
func AllowWith(args json.RawMessage) Decision { return Decision{Allow: true, Arguments: args} }

// Deny refuses the call; reason is fed back to the model.
func Deny(reason string) Decision { return Decision{Reason: reason} }

// Approver decides whether a tool call may run. Approve may block for as long
// as it needs (a person may be asked); return ctx.Err() when ctx ends. It is
// called from a tool worker goroutine when WithParallel is above one.
type Approver interface {
	Approve(ctx context.Context, c Call) (Decision, error)
}

// ApproverFunc adapts a function to Approver.
type ApproverFunc func(ctx context.Context, c Call) (Decision, error)

// Approve implements Approver.
func (f ApproverFunc) Approve(ctx context.Context, c Call) (Decision, error) { return f(ctx, c) }
