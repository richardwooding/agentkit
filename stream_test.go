package agentkit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

func kinds(events []agentkit.Event) []agentkit.EventKind {
	out := make([]agentkit.EventKind, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func TestStreamEvents(t *testing.T) {
	client := &scriptedStream{&scripted{responses: []*core.Response{
		toolCalls(call("c1", "add", `{"A":1,"B":2}`)),
		{Message: core.Assistant(core.ReasoningPart{Text: "hmm"}, core.Text("three")), FinishReason: core.FinishStop},
	}}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()))
	var events []agentkit.Event
	var finalErr error
	for e, err := range a.Stream(context.Background(), "1+2") {
		finalErr = err
		events = append(events, e)
	}
	if finalErr != nil {
		t.Fatal(finalErr)
	}
	want := []agentkit.EventKind{
		agentkit.EventStep, agentkit.EventUsage, agentkit.EventToolCall, agentkit.EventToolResult, agentkit.EventStep,
		agentkit.EventReasoning, agentkit.EventText, agentkit.EventText, agentkit.EventUsage, agentkit.EventFinish,
	}
	if got := kinds(events); len(got) != len(want) {
		t.Fatalf("kinds = %v", got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("kinds = %v want %v", got, want)
			}
		}
	}
	if events[2].ToolCall.ID != "c1" || events[3].ToolResult.Text() != "3" || events[3].Duration <= 0 || events[3].Err != nil {
		t.Fatalf("tool events = %+v %+v", events[2], events[3])
	}
	if u := events[1]; u.Usage == nil || u.Usage.TotalTokens != 15 || u.FinishReason != core.FinishToolCalls || u.Depth != 0 {
		t.Fatalf("usage event = %+v", u)
	}
	fin := events[len(events)-1]
	if fin.Result == nil || fin.Result.Output != "three" || fin.Result.StopReason != agentkit.StopCompleted || fin.RunID == "" {
		t.Fatalf("finish = %+v", fin)
	}
}

func TestStreamFallsBackToChat(t *testing.T) {
	client := &scripted{responses: []*core.Response{text("whole")}}
	a, _ := agentkit.NewFromClient(client)
	var texts []string
	for e, err := range a.Stream(context.Background(), "x") {
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == agentkit.EventText {
			texts = append(texts, e.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "whole" {
		t.Fatalf("texts = %v", texts)
	}
}

func TestStreamBreakAndError(t *testing.T) {
	client := &scriptedStream{&scripted{responses: []*core.Response{toolCalls(call("c1", "add", `{"A":1,"B":2}`)), text("done")}}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()))
	n := 0
	for range a.Stream(context.Background(), "x") {
		n++
		if n == 2 {
			break
		}
	}
	if client.calls != 1 {
		t.Fatalf("break must stop further model calls, got %d", client.calls)
	}

	failing := &scripted{errs: []error{errors.New("boom")}, responses: []*core.Response{nil}}
	a, _ = agentkit.NewFromClient(failing)
	var last agentkit.Event
	var lastErr error
	for e, err := range a.Stream(context.Background(), "x") {
		last, lastErr = e, err
	}
	if lastErr == nil || last.Kind != agentkit.EventFinish || last.Result == nil || last.Result.StopReason != agentkit.StopError {
		t.Fatalf("last = %+v err = %v", last, lastErr)
	}
}

func TestStreamParallelToolsUnderRace(t *testing.T) {
	client := &scriptedStream{&scripted{responses: []*core.Response{
		toolCalls(call("c1", "add", `{"A":1,"B":1}`), call("c2", "add", `{"A":2,"B":2}`), call("c3", "add", `{"A":3,"B":3}`), call("c4", "add", `{"A":4,"B":4}`)),
		text("done"),
	}}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()), agentkit.WithParallel(4))
	var events []agentkit.Event
	for e, err := range a.Stream(context.Background(), "go") {
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	calls, results := 0, 0
	for _, e := range events {
		switch e.Kind {
		case agentkit.EventToolCall:
			calls++
		case agentkit.EventToolResult:
			results++
		default:
		}
	}
	if calls != 4 || results != 4 {
		t.Fatalf("calls=%d results=%d kinds=%v", calls, results, kinds(events))
	}
	if last := events[len(events)-1]; last.Kind != agentkit.EventFinish || last.Result == nil || last.Result.Output != "done" {
		t.Fatalf("last = %+v", last)
	}
	for _, e := range events[:len(events)-1] {
		if e.Kind == agentkit.EventFinish {
			t.Fatal("more than one finish event")
		}
	}
}
