package agentkit_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

func TestProgressEvents(t *testing.T) {
	build := agentkit.Func("build", "", func(ctx context.Context, _ struct{}) (string, error) {
		agentkit.Progress(ctx, "compiling")
		fmt.Fprint(agentkit.ProgressWriter(ctx), "linking")
		return "ok", nil
	})
	client := &scriptedStream{&scripted{responses: []*core.Response{toolCalls(call("c1", "build", `{}`)), text("done")}}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(build))
	var events []agentkit.Event
	for e, err := range a.Stream(context.Background(), "go") {
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	got := kinds(events)
	want := []agentkit.EventKind{
		agentkit.EventStep, agentkit.EventUsage, agentkit.EventToolCall, agentkit.EventToolProgress, agentkit.EventToolProgress,
		agentkit.EventToolResult, agentkit.EventStep, agentkit.EventText, agentkit.EventText, agentkit.EventUsage, agentkit.EventFinish,
	}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v want %v", got, want)
		}
	}
	if events[3].Text != "compiling" || events[4].Text != "linking" || events[3].ToolCall == nil || events[3].ToolCall.ID != "c1" {
		t.Fatalf("progress = %+v %+v", events[3], events[4])
	}
	res := events[len(events)-1].Result
	if len(res.New) != 4 {
		t.Fatalf("progress must not touch the transcript: %+v", res.New)
	}

	// Outside a streaming run both helpers are silent no-ops.
	client = &scriptedStream{&scripted{responses: []*core.Response{toolCalls(call("c1", "build", `{}`)), text("done")}}}
	a, _ = agentkit.NewFromClient(client, agentkit.WithTools(build))
	res, err := a.Run(context.Background(), "go")
	if err != nil || len(res.New) != 4 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if n, err := agentkit.ProgressWriter(context.Background()).Write([]byte("x")); n != 1 || err != nil {
		t.Fatalf("discard writer = %d %v", n, err)
	}
}
