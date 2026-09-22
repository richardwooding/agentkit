package agentkit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

// TestTimeoutExcludesTimeWaitingOnAHuman is the behavior this exists for. A
// run's Timeout bounds the agent's work — a loop that will not converge, a
// tool that hangs — but it used to be a plain context deadline set when the
// run began, so it also bounded the person answering an approval prompt. A
// user who stepped away lost the whole run: two of three in one real session
// died at exactly the budget with the agent idle the entire time.
func TestTimeoutExcludesTimeWaitingOnAHuman(t *testing.T) {
	t.Parallel()
	const budget = 150 * time.Millisecond
	// The approver takes longer than the entire budget, as a person would.
	approver := agentkit.ApproverFunc(func(_ context.Context, _ agentkit.Call) (agentkit.Decision, error) {
		time.Sleep(budget * 2)
		return agentkit.Allow(), nil
	})
	client := &scripted{responses: []*core.Response{
		toolCalls(call("c", "add", `{"A":1,"B":1}`)),
		text("done"),
	}}
	a, err := agentkit.NewFromClient(client,
		agentkit.WithTools(adder()),
		agentkit.WithMiddleware(agentkit.ApproveWith(approver)),
		agentkit.WithBudget(agentkit.Budget{Timeout: budget}),
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("the run died while a human was deciding: %v", err)
	}
	if res.StopReason != agentkit.StopCompleted {
		t.Errorf("StopReason = %q, want the run to have finished normally", res.StopReason)
	}
	// And the wall clock really did exceed the budget, so the test is not
	// passing because everything happened to be fast.
	if res.Duration < budget {
		t.Fatalf("run took %s, which is under the %s budget; this test proves nothing", res.Duration, budget)
	}
}

// TestTimeoutStillStopsAnAgentThatWillNotStop: the budget must still bite when
// it is the *agent* burning the time, or pausing for humans would have turned
// the limit off.
func TestTimeoutStillStopsAnAgentThatWillNotStop(t *testing.T) {
	t.Parallel()
	slow := agentkit.Func("slow", "sleeps", func(ctx context.Context, _ struct{}) (string, error) {
		select {
		case <-time.After(5 * time.Second):
			return "done", nil
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
	})
	client := &scripted{responses: []*core.Response{toolCalls(call("c", "slow", `{}`))}}
	a, err := agentkit.NewFromClient(client,
		agentkit.WithTools(slow),
		agentkit.WithBudget(agentkit.Budget{Timeout: 100 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Run(context.Background(), "go")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline to have stopped it", err)
	}
	// The reason must stay "deadline": the budget now cancels with a cause,
	// and a run that ran out of time must not be reported as a user's
	// interrupt — one is a failure to surface, the other is not.
	if res == nil || res.StopReason != agentkit.StopDeadline {
		t.Fatalf("StopReason = %+v, want deadline", res)
	}
}

// TestConcurrentPromptsCountAsOneWait: tools run in parallel, so several
// prompts can be open at once. One person answering three of them over a
// minute has cost the run a minute, not three — summing the waits would hand
// back time nobody spent and effectively disable the budget.
func TestConcurrentPromptsCountAsOneWait(t *testing.T) {
	t.Parallel()
	const hold = 200 * time.Millisecond
	approver := agentkit.ApproverFunc(func(_ context.Context, _ agentkit.Call) (agentkit.Decision, error) {
		time.Sleep(hold)
		return agentkit.Allow(), nil
	})
	// Three calls in one step, held concurrently.
	client := &scripted{responses: []*core.Response{
		toolCalls(call("a", "add", `{"A":1,"B":1}`), call("b", "add", `{"A":1,"B":1}`), call("c", "add", `{"A":1,"B":1}`)),
		text("done"),
	}}
	a, err := agentkit.NewFromClient(client,
		agentkit.WithTools(adder()),
		agentkit.WithParallel(3),
		agentkit.WithMiddleware(agentkit.ApproveWith(approver)),
		// Comfortably more than one hold, comfortably less than three.
		agentkit.WithBudget(agentkit.Budget{Timeout: hold * 2}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("three concurrent prompts should cost one wait: %v", err)
	}
}
