package agentkit_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

func TestAsTool(t *testing.T) {
	var depths []int
	probe := agentkit.Func("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
		c, _ := agentkit.CallFrom(ctx)
		depths = append(depths, c.Depth)
		return "probed", nil
	})
	childClient := &scripted{responses: []*core.Response{toolCalls(call("p", "probe", `{}`)), text("child says hi")}}
	child, _ := agentkit.NewFromClient(childClient, agentkit.WithName("child"), agentkit.WithTools(probe))
	parentClient := &scripted{responses: []*core.Response{toolCalls(call("c", "ask_child", `{"input":"hello"}`)), text("parent done")}}
	parent, _ := agentkit.NewFromClient(parentClient, agentkit.WithName("parent"), agentkit.WithTools(agentkit.AsTool(child, "ask_child", "delegate")))
	res, err := parent.Run(context.Background(), "go")
	if err != nil || res.Output != "parent done" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Usage.TotalTokens != 60 {
		t.Fatalf("usage should include the child: %+v", res.Usage)
	}
	for _, m := range res.New {
		if strings.Contains(m.Text(), "child says hi") && m.Role != core.RoleTool {
			t.Fatalf("child transcript leaked: %+v", m)
		}
	}
	if got := res.New[2].ToolResults()[0].Text(); got != "child says hi" {
		t.Fatalf("tool result = %q", got)
	}
	if len(depths) != 1 || depths[0] != 1 {
		t.Fatalf("depths = %v", depths)
	}
	if childClient.seen[0].Messages[0].Text() != "hello" {
		t.Fatalf("child input = %+v", childClient.seen[0].Messages)
	}
}

func TestHandoff(t *testing.T) {
	billingClient := &scripted{responses: []*core.Response{text("refund issued")}}
	billing, _ := agentkit.NewFromClient(billingClient, agentkit.WithName("billing"), agentkit.WithInstructions("You handle refunds."), agentkit.WithTools(adder()))
	frontClient := &scripted{responses: []*core.Response{toolCalls(call("h", agentkit.HandoffToolName, `{"agent":"billing","reason":"refund"}`))}}
	var handoffs []agentkit.HandoffInfo
	front, _ := agentkit.NewFromClient(frontClient, agentkit.WithName("front"), agentkit.WithInstructions("Triage."),
		agentkit.WithTools(agentkit.Handoff(billing)), agentkit.WithHooks(agentkit.Hooks{OnHandoff: func(i agentkit.HandoffInfo) { handoffs = append(handoffs, i) }}))
	res, err := front.Run(context.Background(), "I want a refund")
	if err != nil || res.Output != "refund issued" || res.Agent != "billing" || res.Handoffs != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(handoffs) != 1 || handoffs[0].From != "front" || handoffs[0].To != "billing" {
		t.Fatalf("handoffs = %+v", handoffs)
	}
	req := billingClient.seen[0]
	if req.Messages[0].Text() != "You handle refunds." || len(req.Tools) != 1 || req.Tools[0].Name != "add" {
		t.Fatalf("billing request = %+v", req)
	}
	if req.Messages[1].Text() != "I want a refund" || len(req.Messages[2].ToolCalls()) != 1 || req.Messages[3].ToolResults()[0].Text() != "Transferred to billing." {
		t.Fatalf("transcript not carried: %+v", req.Messages)
	}
	if !strings.Contains(string(front.Tools()[0].Definition().Parameters), `"enum":["billing"]`) {
		t.Fatalf("handoff schema = %s", front.Tools()[0].Definition().Parameters)
	}

	cClient := &scripted{responses: []*core.Response{text("c done")}}
	c, _ := agentkit.NewFromClient(cClient, agentkit.WithName("c"))
	bClient := &scripted{responses: []*core.Response{toolCalls(call("h2", agentkit.HandoffToolName, `{"agent":"c"}`))}}
	b, _ := agentkit.NewFromClient(bClient, agentkit.WithName("b"), agentkit.WithTools(agentkit.Handoff(c)))
	aClient := &scripted{responses: []*core.Response{toolCalls(call("h1", agentkit.HandoffToolName, `{"agent":"b"}`))}}
	a, _ := agentkit.NewFromClient(aClient, agentkit.WithName("a"), agentkit.WithTools(agentkit.Handoff(b)), agentkit.WithBudget(agentkit.Budget{MaxHandoffs: 1}))
	res, err = a.Run(context.Background(), "x")
	if !errors.Is(err, agentkit.ErrHandoffLoop) || res.Handoffs != 2 || cClient.calls != 0 {
		t.Fatalf("err=%v res=%+v", err, res)
	}
}

func TestMap(t *testing.T) {
	out, err := agentkit.Map(context.Background(), 2, []int{1, 2, 3}, func(_ context.Context, n int) (int, error) {
		time.Sleep(time.Duration(4-n) * 5 * time.Millisecond)
		if n == 2 {
			return 0, errors.New("two failed")
		}
		return n * 10, nil
	})
	if out[0] != 10 || out[2] != 30 || err == nil || !strings.Contains(err.Error(), "item 1: two failed") {
		t.Fatalf("out=%v err=%v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	out, err = agentkit.Map(ctx, 1, []int{1, 2, 3}, func(context.Context, int) (int, error) {
		cancel()
		return 1, nil
	})
	if !errors.Is(err, context.Canceled) || out[0] != 1 || out[2] != 0 {
		t.Fatalf("cancel: out=%v err=%v", out, err)
	}
}

func TestAsToolForwardsEvents(t *testing.T) {
	run := func(forward bool) []agentkit.Event {
		childClient := &scriptedStream{&scripted{responses: []*core.Response{toolCalls(call("p", "add", `{"A":1,"B":1}`)), text("child says hi")}}}
		child, _ := agentkit.NewFromClient(childClient, agentkit.WithName("child"), agentkit.WithTools(adder()))
		var opts []agentkit.AsToolOption
		if forward {
			opts = append(opts, agentkit.WithForwardEvents())
		}
		parentClient := &scriptedStream{&scripted{responses: []*core.Response{toolCalls(call("c", "ask_child", `{"input":"hello"}`)), text("parent done")}}}
		parent, _ := agentkit.NewFromClient(parentClient, agentkit.WithName("parent"), agentkit.WithTools(agentkit.AsTool(child, "ask_child", "delegate", opts...)))
		var events []agentkit.Event
		for e, err := range parent.Stream(context.Background(), "go") {
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, e)
		}
		return events
	}

	events := run(true)
	var child, parent []agentkit.Event
	for _, e := range events {
		if e.Depth == 1 {
			child = append(child, e)
		} else {
			parent = append(parent, e)
		}
	}
	if len(child) == 0 {
		t.Fatalf("no child events forwarded: %v", kinds(events))
	}
	parentID := events[len(events)-1].RunID
	for _, e := range child {
		if e.Parent != parentID || e.Agent != "child" || e.RunID == parentID || e.RunID == "" {
			t.Fatalf("child event = %+v", e)
		}
	}
	childKinds := kinds(child)
	wantChild := []agentkit.EventKind{
		agentkit.EventStep, agentkit.EventUsage, agentkit.EventToolCall, agentkit.EventToolResult, agentkit.EventStep,
		agentkit.EventText, agentkit.EventText, agentkit.EventUsage, agentkit.EventFinish,
	}
	if len(childKinds) != len(wantChild) {
		t.Fatalf("child kinds = %v", childKinds)
	}
	for i := range wantChild {
		if childKinds[i] != wantChild[i] {
			t.Fatalf("child kinds = %v want %v", childKinds, wantChild)
		}
	}
	if fin := child[len(child)-1]; fin.Result == nil || fin.Result.Output != "child says hi" || fin.Err != nil {
		t.Fatalf("child finish = %+v", fin)
	}
	for _, e := range parent {
		if e.Parent != "" || e.Agent != "parent" || e.Depth != 0 {
			t.Fatalf("parent event = %+v", e)
		}
	}
	// Child events sit between the parent's tool call and its result.
	var callIdx, resultIdx, firstChild int
	for i, e := range events {
		switch {
		case e.Kind == agentkit.EventToolCall && e.Depth == 0:
			callIdx = i
		case e.Kind == agentkit.EventToolResult && e.Depth == 0:
			resultIdx = i
		case e.Depth == 1 && firstChild == 0:
			firstChild = i
		}
	}
	if callIdx >= firstChild || firstChild >= resultIdx {
		t.Fatalf("ordering: call=%d child=%d result=%d", callIdx, firstChild, resultIdx)
	}
	if last := events[len(events)-1]; last.Kind != agentkit.EventFinish || last.Depth != 0 || last.Result.Output != "parent done" || last.Result.Usage.TotalTokens != 60 {
		t.Fatalf("last = %+v", last)
	}

	for _, e := range run(false) {
		if e.Depth != 0 || e.Parent != "" {
			t.Fatalf("event forwarded without the option: %+v", e)
		}
	}
}
