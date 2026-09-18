package agentkit

import (
	"context"
	"sync"

	"github.com/richardwooding/llmkit/core"
)

type usageSink struct {
	mu sync.Mutex
	u  core.Usage
}

func (s *usageSink) add(u core.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.u = s.u.Add(u)
}

func (s *usageSink) total() core.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.u
}

type runStateKey struct{}

type runState struct {
	depth int
	sink  *usageSink
}

func withRunState(ctx context.Context, r *run) context.Context {
	return context.WithValue(ctx, runStateKey{}, runState{depth: r.depth + 1, sink: r.sink})
}

// depthFrom returns the nesting depth for a run started under ctx.
func depthFrom(ctx context.Context) int {
	if s, ok := ctx.Value(runStateKey{}).(runState); ok {
		return s.depth
	}
	return 0
}

// parentSink returns the usage accumulator of the enclosing run, if any.
func parentSink(ctx context.Context) *usageSink {
	if s, ok := ctx.Value(runStateKey{}).(runState); ok {
		return s.sink
	}
	return nil
}
