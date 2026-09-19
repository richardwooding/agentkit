package agentkit_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

func TestApproveWithRewritesArguments(t *testing.T) {
	client := &scripted{responses: []*core.Response{toolCalls(call("c1", "add", `{"A":1,"B":2}`)), text("done")}}
	var seen agentkit.Call
	approver := agentkit.ApproverFunc(func(_ context.Context, c agentkit.Call) (agentkit.Decision, error) {
		seen = c
		return agentkit.AllowWith(json.RawMessage(`{"A":10,"B":20}`)), nil
	})
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()), agentkit.WithMiddleware(agentkit.ApproveWith(approver)))
	res, err := a.Run(context.Background(), "go")
	if err != nil || res.Output != "done" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if seen.Call.ID != "c1" || seen.Call.Name != "add" || seen.RunID != res.RunID {
		t.Fatalf("approver saw %+v", seen)
	}
	if got := res.New[2].ToolResults()[0]; got.IsError || got.Text() != "30" {
		t.Fatalf("result = %+v", got)
	}
	if got := string(res.New[1].ToolCalls()[0].Arguments); got != `{"A":1,"B":2}` {
		t.Fatalf("assistant message must keep the model's arguments, got %s", got)
	}
}

func TestApproveWithDenyFeedsModel(t *testing.T) {
	client := &scripted{responses: []*core.Response{toolCalls(call("c1", "add", `{"A":1,"B":2}`)), text("understood")}}
	var hookErr error
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()),
		agentkit.WithMiddleware(agentkit.ApproveWith(agentkit.ApproverFunc(func(context.Context, agentkit.Call) (agentkit.Decision, error) {
			return agentkit.Deny("too risky"), nil
		}))),
		agentkit.WithHooks(agentkit.Hooks{OnToolResult: func(i agentkit.ToolResultInfo) { hookErr = i.Err }}))
	res, err := a.Run(context.Background(), "go")
	if err != nil || res.Output != "understood" || res.Steps != 2 {
		t.Fatalf("run must continue after a denial: res=%+v err=%v", res, err)
	}
	if got := res.New[2].ToolResults()[0]; !got.IsError || got.Text() != "not approved: too risky" {
		t.Fatalf("result = %+v", got)
	}
	if !errors.Is(hookErr, agentkit.ErrApprovalDenied) || !strings.Contains(hookErr.Error(), "too risky") {
		t.Fatalf("hook err = %v", hookErr)
	}
	if fed := client.seen[1].Messages[2].ToolResults()[0].Text(); fed != "not approved: too risky" {
		t.Fatalf("model saw %q", fed)
	}

	// The legacy Approve(fn) is the same middleware with an error-only verdict.
	client = &scripted{responses: []*core.Response{toolCalls(call("c1", "add", `{"A":1,"B":2}`)), text("ok")}}
	a, _ = agentkit.NewFromClient(client, agentkit.WithTools(adder()),
		agentkit.WithMiddleware(agentkit.Approve(func(context.Context, agentkit.Call) error { return errors.New("nope") })))
	res, err = a.Run(context.Background(), "go")
	if err != nil || res.New[2].ToolResults()[0].Text() != "not approved: nope" {
		t.Fatalf("legacy Approve: res=%+v err=%v", res, err)
	}
}

func TestApproveWithIsAsynchronous(t *testing.T) {
	client := &scriptedStream{&scripted{responses: []*core.Response{toolCalls(call("c1", "add", `{"A":1,"B":2}`)), text("done")}}}
	release := make(chan struct{})
	approver := agentkit.ApproverFunc(func(ctx context.Context, _ agentkit.Call) (agentkit.Decision, error) {
		select {
		case <-release:
			return agentkit.Allow(), nil
		case <-ctx.Done():
			return agentkit.Decision{}, ctx.Err()
		case <-time.After(5 * time.Second):
			return agentkit.Decision{}, errors.New("request event never observed")
		}
	})
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()), agentkit.WithMiddleware(agentkit.ApproveWith(approver)), agentkit.WithParallel(2))
	var events []agentkit.Event
	for e, err := range a.Stream(context.Background(), "go") {
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
		if e.Kind == agentkit.EventApprovalRequest {
			if e.ToolCall == nil || e.ToolCall.ID != "c1" {
				t.Fatalf("request = %+v", e)
			}
			close(release)
		}
	}
	got := kinds(events)
	want := []agentkit.EventKind{
		agentkit.EventStep, agentkit.EventUsage, agentkit.EventToolCall, agentkit.EventApprovalRequest, agentkit.EventApprovalResult,
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
	if d := events[4].Decision; d == nil || !d.Allow {
		t.Fatalf("result event = %+v", events[4])
	}
	if events[5].ToolResult.Text() != "3" {
		t.Fatalf("tool result = %+v", events[5])
	}
}

func TestApproveWithCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &scripted{responses: []*core.Response{toolCalls(call("c1", "add", `{"A":1,"B":2}`), call("c2", "add", `{"A":3,"B":4}`)), text("never")}}
	approver := agentkit.ApproverFunc(func(ctx context.Context, _ agentkit.Call) (agentkit.Decision, error) {
		cancel()
		<-ctx.Done()
		return agentkit.Allow(), nil
	})
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()), agentkit.WithMiddleware(agentkit.ApproveWith(approver)))
	res, err := a.Run(ctx, "go")
	if !errors.Is(err, context.Canceled) || res.StopReason != agentkit.StopCancelled || client.calls != 1 {
		t.Fatalf("res=%+v err=%v calls=%d", res, err, client.calls)
	}
	last := res.New[len(res.New)-1]
	results := last.ToolResults()
	if last.Role != core.RoleTool || len(results) != 2 || !results[0].IsError || !results[1].IsError ||
		!strings.Contains(results[0].Text(), "not executed") || !strings.Contains(results[1].Text(), "not executed") {
		t.Fatalf("results = %+v", results)
	}
}
