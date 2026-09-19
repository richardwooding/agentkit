package agentkit_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

func TestAgentWith(t *testing.T) {
	var wraps atomic.Int32
	counting := func(next agentkit.Tool) agentkit.Tool {
		return agentkit.Wrap(next, agentkit.Observe(func(agentkit.Call, agentkit.Output, error, time.Duration) { wraps.Add(1) }))
	}
	client := &scripted{responses: []*core.Response{toolCalls(call("c1", "add", `{"A":1,"B":2}`)), text("done")}}
	base, err := agentkit.NewFromClient(client, agentkit.WithName("base"), agentkit.WithInstructions("sys"), agentkit.WithTools(adder()), agentkit.WithMiddleware(counting))
	if err != nil {
		t.Fatal(err)
	}
	derived, err := base.With(agentkit.WithName("derived"), agentkit.WithAdditionalInstructions("more"), agentkit.WithParallel(3))
	if err != nil {
		t.Fatal(err)
	}
	if base.Name() != "base" || derived.Name() != "derived" || derived.Client() != client || base.Client() != client {
		t.Fatalf("names/clients = %q %q", base.Name(), derived.Name())
	}
	res, err := derived.Run(context.Background(), "go")
	if err != nil || res.Output != "done" || res.Agent != "derived" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if wraps.Load() != 1 {
		t.Fatalf("middleware applied %d times, want once", wraps.Load())
	}
	if sys := client.seen[0].Messages[0].Text(); sys != "sys\n\nmore" {
		t.Fatalf("system = %q", sys)
	}
	if len(derived.Tools()) != 1 || len(base.Tools()) != 1 {
		t.Fatalf("tools = %v %v", derived.Tools().Names(), base.Tools().Names())
	}
	if _, err := derived.Tools()[0].Call(context.Background(), json.RawMessage(`{"A":1,"B":1}`)); err != nil || wraps.Load() != 2 {
		t.Fatalf("direct call: err=%v wraps=%d", err, wraps.Load())
	}
}

func TestWithCache(t *testing.T) {
	client := &scripted{responses: []*core.Response{text("ok")}}
	agent, err := agentkit.NewFromClient(client, agentkit.WithCache(core.CacheConfig{System: true, Turns: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	got := client.seen[0].Cache
	if got == nil || !got.System || got.Turns != 1 {
		t.Fatalf("request cache = %+v, want {System:true Turns:1}", got)
	}
}
