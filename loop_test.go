package agentkit_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

func adder() agentkit.Tool {
	return agentkit.Func("add", "adds", func(_ context.Context, in struct{ A, B int }) (int, error) { return in.A + in.B, nil })
}

func TestRunToolLoop(t *testing.T) {
	client := &scripted{responses: []*core.Response{
		toolCalls(call("c1", "add", `{"A":2,"B":3}`), call("c2", "missing", `{}`), call("c3", "add", `{"A":"x"}`)),
		text("The answer is 5"),
	}}
	var toolEvents []string
	hooks := agentkit.Hooks{
		OnToolResult: func(i agentkit.ToolResultInfo) { toolEvents = append(toolEvents, i.Call.Call.Name) },
	}
	a, err := agentkit.NewFromClient(client, agentkit.WithName("calc"), agentkit.WithInstructions("be terse"), agentkit.WithTools(adder()), agentkit.WithHooks(hooks))
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Run(context.Background(), "2+3?")
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "The answer is 5" || res.Steps != 2 || res.ToolCalls != 3 || res.StopReason != agentkit.StopCompleted || res.Agent != "calc" {
		t.Fatalf("res = %+v", res)
	}
	if res.Usage.TotalTokens != 30 || res.RunID == "" || res.Duration <= 0 {
		t.Fatalf("usage/run = %+v", res)
	}
	if len(res.New) != 4 || res.New[0].Role != core.RoleUser || res.New[1].Role != core.RoleAssistant || res.New[2].Role != core.RoleTool {
		t.Fatalf("new = %+v", res.New)
	}
	results := res.New[2].ToolResults()
	if results[0].Text() != "5" || !results[1].IsError || !strings.Contains(results[1].Text(), "tool not found") || !results[2].IsError {
		t.Fatalf("results = %+v", results)
	}
	first := client.seen[0]
	if first.Messages[0].Role != core.RoleSystem || first.Messages[0].Text() != "be terse" || len(first.Tools) != 1 || first.Tools[0].Name != "add" {
		t.Fatalf("first request = %+v", first)
	}
	if len(client.seen[1].Messages) != 4 {
		t.Fatalf("second request saw %d messages", len(client.seen[1].Messages))
	}
	if len(toolEvents) != 3 {
		t.Fatalf("hooks = %v", toolEvents)
	}
}

func TestParallelToolsPreserveOrder(t *testing.T) {
	var inflight, maxInflight atomic.Int32
	sleeper := agentkit.Func("sleep", "", func(ctx context.Context, in struct{ MS int }) (string, error) {
		cur := inflight.Add(1)
		for {
			old := maxInflight.Load()
			if cur <= old || maxInflight.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(time.Duration(in.MS) * time.Millisecond)
		inflight.Add(-1)
		return "slept " + json.Number(string(rune('0'+in.MS/10))).String(), nil
	})
	serialTool := agentkit.Wrap(agentkit.Func("serial", "", func(context.Context, struct{}) (string, error) {
		if inflight.Load() != 0 {
			return "", errors.New("serial ran concurrently")
		}
		return "serial", nil
	}), agentkit.Serial())
	client := &scripted{responses: []*core.Response{
		toolCalls(call("1", "sleep", `{"MS":30}`), call("2", "serial", `{}`), call("3", "sleep", `{"MS":10}`), call("4", "sleep", `{"MS":20}`)),
		text("done"),
	}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(sleeper, serialTool), agentkit.WithParallel(3))
	start := time.Now()
	res, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 55*time.Millisecond {
		t.Fatalf("not parallel: %v", elapsed)
	}
	if maxInflight.Load() < 2 {
		t.Fatalf("max inflight = %d", maxInflight.Load())
	}
	got := res.New[2].ToolResults()
	if got[0].CallID != "1" || got[1].CallID != "2" || got[2].CallID != "3" || got[3].CallID != "4" || got[1].Text() != "serial" || got[1].IsError {
		t.Fatalf("results = %+v", got)
	}
}

func TestBudgets(t *testing.T) {
	loop := toolCalls(call("c", "add", `{"A":1,"B":1}`))
	tests := []struct {
		name   string
		budget agentkit.Budget
		reason agentkit.StopReason
		limit  string
	}{
		{"steps", agentkit.Budget{MaxSteps: 2}, agentkit.StopMaxSteps, "steps"},
		{"tokens", agentkit.Budget{MaxTokens: 20}, agentkit.StopMaxTokens, "tokens"},
		{"tool calls", agentkit.Budget{MaxToolCalls: 1}, agentkit.StopMaxToolCalls, "tool_calls"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &scripted{responses: []*core.Response{loop, loop, loop, loop}}
			a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()), agentkit.WithBudget(tt.budget))
			res, err := a.Run(context.Background(), "loop")
			var be *agentkit.BudgetError
			if !errors.Is(err, agentkit.ErrBudgetExceeded) || !errors.As(err, &be) || be.Limit != tt.limit {
				t.Fatalf("err = %v", err)
			}
			if res == nil || res.StopReason != tt.reason {
				t.Fatalf("res = %+v", res)
			}
			last := res.New[len(res.New)-1]
			if last.Role != core.RoleTool {
				t.Fatalf("transcript must end with tool results, got %s", last.Role)
			}
			for i, m := range res.New {
				if len(m.ToolCalls()) > 0 && (i+1 >= len(res.New) || len(res.New[i+1].ToolResults()) != len(m.ToolCalls())) {
					t.Fatalf("orphaned tool call at %d", i)
				}
			}
		})
	}
	client := &scripted{responses: []*core.Response{loop, loop}}
	slowAdd := agentkit.Func("add", "", func(ctx context.Context, in struct{ A, B int }) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(slowAdd), agentkit.WithBudget(agentkit.Budget{Timeout: 20 * time.Millisecond}))
	res, err := a.Run(context.Background(), "slow")
	if !errors.Is(err, context.DeadlineExceeded) || res.StopReason != agentkit.StopDeadline {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRetryPolicy(t *testing.T) {
	client := &scripted{
		responses: []*core.Response{nil, text("ok")},
		errs:      []error{&core.APIError{Provider: "p", Status: 429, RetryAfter: 5 * time.Millisecond}},
	}
	var retries []agentkit.RetryInfo
	a, _ := agentkit.NewFromClient(client, agentkit.WithHooks(agentkit.Hooks{OnRetry: func(i agentkit.RetryInfo) { retries = append(retries, i) }}))
	start := time.Now()
	res, err := a.Run(context.Background(), "x")
	if err != nil || res.Output != "ok" || client.calls != 2 || len(retries) != 1 || retries[0].Delay < 4*time.Millisecond {
		t.Fatalf("res=%+v err=%v calls=%d retries=%+v", res, err, client.calls, retries)
	}
	if time.Since(start) < 4*time.Millisecond {
		t.Fatal("did not wait for RetryAfter")
	}
	client = &scripted{responses: []*core.Response{nil, text("ok")}, errs: []error{&core.APIError{Status: 400, Message: "bad"}}}
	a, _ = agentkit.NewFromClient(client)
	res, err = a.Run(context.Background(), "x")
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || client.calls != 1 || res.StopReason != agentkit.StopError {
		t.Fatalf("400 must not retry: %v calls=%d", err, client.calls)
	}
	p := agentkit.DefaultRetry
	if p.Retryable(context.Canceled) || p.Retryable(core.ErrContextLength) || !p.Retryable(&core.APIError{Status: 503}) || !p.Retryable(&core.APIError{Status: 0}) {
		t.Fatal("Retryable classification wrong")
	}
	if d := p.Delay(1, errors.New("x")); d < 350*time.Millisecond || d > 650*time.Millisecond {
		t.Fatalf("delay = %v", d)
	}
}

func TestContextLengthCompaction(t *testing.T) {
	long := strings.Repeat("word ", 400)
	client := &scripted{
		responses: []*core.Response{nil, text("compacted ok")},
		errs:      []error{&core.APIError{Status: 400, Code: "context_length_exceeded"}},
	}
	var compacts []agentkit.CompactInfo
	a, _ := agentkit.NewFromClient(client, agentkit.WithInstructions("sys"), agentkit.WithCompactor(agentkit.Window(1)),
		agentkit.WithHooks(agentkit.Hooks{OnCompact: func(i agentkit.CompactInfo) { compacts = append(compacts, i) }}))
	history := []core.Message{core.UserText(long), core.Assistant(core.Text("a1")), core.UserText(long), core.Assistant(core.Text("a2")), core.UserText("now")}
	res, err := a.RunMessages(context.Background(), history)
	if err != nil || res.Output != "compacted ok" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(compacts) != 1 || compacts[0].Reason != "context_length" || compacts[0].After >= compacts[0].Before {
		t.Fatalf("compacts = %+v", compacts)
	}
	if len(client.seen[1].Messages) >= len(client.seen[0].Messages) || client.seen[1].Messages[0].Role != core.RoleSystem {
		t.Fatalf("second request not compacted: %d vs %d", len(client.seen[1].Messages), len(client.seen[0].Messages))
	}

	client = &scripted{responses: []*core.Response{text("ok")}}
	a, _ = agentkit.NewFromClient(client, agentkit.WithCompactor(agentkit.Window(1)), agentkit.WithContextWindow(100))
	if _, err := a.RunMessages(context.Background(), history); err != nil {
		t.Fatal(err)
	}
	if len(client.seen[0].Messages) != 1 || client.seen[0].Messages[0].Text() != "now" {
		t.Fatalf("proactive compaction failed: %+v", client.seen[0].Messages)
	}
}

func TestSessionStore(t *testing.T) {
	store := agentkit.NewMemoryStore()
	client := &scripted{responses: []*core.Response{text("hi"), text("again")}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithInstructions("sys"), agentkit.WithStore(store))
	if _, err := a.Run(context.Background(), "one", agentkit.WithSession("s1")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "two", agentkit.WithSession("s1")); err != nil {
		t.Fatal(err)
	}
	msgs, _ := store.Load(context.Background(), "s1")
	if len(msgs) != 4 || msgs[0].Role != core.RoleUser || msgs[3].Text() != "again" {
		t.Fatalf("stored = %+v", msgs)
	}
	second := client.seen[1].Messages
	if len(second) != 4 || second[0].Role != core.RoleSystem || second[1].Text() != "one" || second[3].Text() != "two" {
		t.Fatalf("second request = %+v", second)
	}
}

func TestCancelledParallelToolsKeepPairs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	blocker := agentkit.Func("block", "", func(ctx context.Context, _ struct{}) (string, error) {
		cancel()
		<-ctx.Done()
		return "", ctx.Err()
	})
	client := &scripted{responses: []*core.Response{toolCalls(call("1", "block", `{}`), call("2", "block", `{}`), call("3", "block", `{}`))}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(blocker), agentkit.WithParallel(1))
	res, err := a.Run(ctx, "go")
	if !errors.Is(err, context.Canceled) || res.StopReason != agentkit.StopCancelled {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	results := res.New[len(res.New)-1].ToolResults()
	if len(results) != 3 || !results[2].IsError || !strings.Contains(results[2].Text(), "not executed") {
		t.Fatalf("results = %+v", results)
	}
}

type pinnedTool struct{ agentkit.Tool }

func (pinnedTool) Pinned() bool { return true }

func TestPinnedSurvivesCompaction(t *testing.T) {
	client := &scripted{responses: []*core.Response{
		toolCalls(call("c1", "manual", `{}`)),
		text("first"),
		text("second"),
	}}
	manual := pinnedTool{agentkit.Func("manual", "load the manual", func(context.Context, struct{}) (string, error) {
		return "MANUAL: always answer in haiku", nil
	})}
	store := agentkit.NewMemoryStore()
	a, err := agentkit.NewFromClient(client,
		agentkit.WithInstructions("be terse"),
		agentkit.WithTools(manual),
		agentkit.WithStore(store),
		agentkit.WithCompactor(agentkit.Window(1)),
		agentkit.WithContextWindow(20),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "q1", agentkit.WithSession("s")); err != nil {
		t.Fatal(err)
	}
	res, err := a.Run(context.Background(), "q2", agentkit.WithSession("s"))
	if err != nil {
		t.Fatal(err)
	}
	last := client.seen[len(client.seen)-1]
	sys := last.Messages[0]
	if sys.Role != core.RoleSystem || strings.Count(sys.Text(), "MANUAL: always answer in haiku") != 1 || !strings.HasPrefix(sys.Text(), "be terse\n\n") {
		t.Fatalf("system prompt after compaction = %q", sys.Text())
	}
	for _, m := range last.Messages[1:] {
		if len(m.ToolResults()) > 0 {
			t.Fatalf("original tool result should have been compacted away: %+v", last.Messages)
		}
	}
	for _, m := range res.Messages {
		if m.Role == core.RoleSystem && strings.Contains(m.Text(), "MANUAL") {
			t.Fatalf("transcript must not contain the pinned copy: %q", m.Text())
		}
	}
	early := client.seen[0]
	if strings.Contains(early.Messages[0].Text(), "MANUAL") {
		t.Fatalf("pin re-sent while the result was still present: %q", early.Messages[0].Text())
	}
}

func TestAdditionalInstructions(t *testing.T) {
	client := &scripted{responses: []*core.Response{text("ok")}}
	a, err := agentkit.NewFromClient(client,
		agentkit.WithAdditionalInstructions("Skills: none", ""),
		agentkit.WithInstructions("base"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if got := client.seen[0].Messages[0].Text(); got != "base\n\nSkills: none" {
		t.Fatalf("system = %q", got)
	}
}
