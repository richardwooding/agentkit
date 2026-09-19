package agentkit

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/richardwooding/llmkit/core"
)

// Middleware wraps a Tool. Wrap applies the first middleware outermost.
type Middleware func(Tool) Tool

// Wrap applies mws to t; nil middleware is skipped.
func Wrap(t Tool, mws ...Middleware) Tool {
	for _, mw := range slices.Backward(mws) {
		if mw != nil {
			t = mw(t)
		}
	}
	return t
}

type wrapped struct {
	Tool
	call func(ctx context.Context, next Tool, args json.RawMessage) (Output, error)
}

// Call implements Tool.
func (w wrapped) Call(ctx context.Context, args json.RawMessage) (Output, error) {
	return w.call(ctx, w.Tool, args)
}

// Sequential implements Sequential.
func (w wrapped) Sequential() bool { return isSequential(w.Tool) }

// Pinned implements Pinned.
func (w wrapped) Pinned() bool { return isPinned(w.Tool) }

func middleware(fn func(ctx context.Context, next Tool, args json.RawMessage) (Output, error)) Middleware {
	return func(t Tool) Tool { return wrapped{Tool: t, call: fn} }
}

// Timeout bounds each call of the tool.
func Timeout(d time.Duration) Middleware {
	return middleware(func(ctx context.Context, next Tool, args json.RawMessage) (Output, error) {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return next.Call(ctx, args)
	})
}

// Approve gates each call; a non-nil error from fn is fed back to the model as
// an error result wrapping ErrApprovalDenied. Cancel ctx inside fn to abort
// the run instead.
func Approve(fn func(ctx context.Context, c Call) error) Middleware {
	return middleware(func(ctx context.Context, next Tool, args json.RawMessage) (Output, error) {
		c, _ := CallFrom(ctx)
		if c.Call.Name == "" {
			c.Call = core.ToolCall{Name: next.Definition().Name, Arguments: args}
		}
		if err := fn(ctx, c); err != nil {
			return Errorf("not approved: %v", err), fmt.Errorf("%w: %w", ErrApprovalDenied, err)
		}
		return next.Call(ctx, args)
	})
}

// Recover turns a panic inside the tool into an error result.
func Recover() Middleware {
	return middleware(func(ctx context.Context, next Tool, args json.RawMessage) (res Output, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("agentkit: tool %s panicked: %v", next.Definition().Name, r)
				res = Errorf("%v", err)
			}
		}()
		return next.Call(ctx, args)
	})
}

// Observe reports every call's outcome and duration.
func Observe(fn func(c Call, r Output, err error, d time.Duration)) Middleware {
	return middleware(func(ctx context.Context, next Tool, args json.RawMessage) (Output, error) {
		start := time.Now()
		res, err := next.Call(ctx, args)
		c, _ := CallFrom(ctx)
		fn(c, res, err, time.Since(start))
		return res, err
	})
}

type serial struct{ Tool }

// Sequential implements Sequential.
func (serial) Sequential() bool { return true }

// Serial marks the tool as never running concurrently with other tool calls.
func Serial() Middleware {
	return func(t Tool) Tool { return serial{Tool: t} }
}
