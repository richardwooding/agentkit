package agentkit_test

import (
	"context"
	"testing"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

func TestInboxAppendsBeforeNextCall(t *testing.T) {
	inbox := agentkit.NewInbox()
	steer := agentkit.Func("steer", "", func(context.Context, struct{}) (string, error) {
		inbox.Post(core.Text("also check B"))
		return "ok", nil
	})
	client := &scripted{responses: []*core.Response{toolCalls(call("c1", "steer", `{}`)), text("done")}}
	store := agentkit.NewMemoryStore()
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(steer), agentkit.WithStore(store))
	res, err := a.Run(context.Background(), "go", agentkit.WithSession("s"), agentkit.WithInbox(inbox))
	if err != nil || res.Output != "done" || inbox.Len() != 0 {
		t.Fatalf("res=%+v err=%v len=%d", res, err, inbox.Len())
	}
	second := client.seen[1].Messages
	if len(second) != 4 || second[2].Role != core.RoleTool || second[3].Role != core.RoleUser || second[3].Text() != "also check B" {
		t.Fatalf("second request = %+v", second)
	}
	stored, _ := store.Load(context.Background(), "s")
	if len(stored) != 5 || stored[3].Text() != "also check B" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestInboxContinuesAfterFinalText(t *testing.T) {
	inbox := agentkit.NewInbox()
	client := &scripted{responses: []*core.Response{text("first"), text("second")}}
	posted := false
	hooks := agentkit.Hooks{OnModelResponse: func(agentkit.ModelResponseInfo) {
		if !posted {
			posted = true
			inbox.PostMessage(core.UserText("one more thing"))
		}
	}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithHooks(hooks))
	res, err := a.Run(context.Background(), "go", agentkit.WithInbox(inbox))
	if err != nil || res.Output != "second" || res.Steps != 2 || res.StopReason != agentkit.StopCompleted {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	second := client.seen[1].Messages
	if len(second) != 3 || second[1].Text() != "first" || second[2].Role != core.RoleUser || second[2].Text() != "one more thing" {
		t.Fatalf("second request = %+v", second)
	}
	if len(res.New) != 4 {
		t.Fatalf("new = %+v", res.New)
	}

	// Typed runs finish on their answer; a pending message stays queued.
	inbox = agentkit.NewInbox()
	inbox.Post(core.Text("late"))
	posted = false
	client = &scripted{responses: []*core.Response{text(`{"N":1}`)}}
	a, _ = agentkit.NewFromClient(client, agentkit.WithHooks(hooks))
	if _, _, err := agentkit.Run[struct{ N int }](context.Background(), a, "go", agentkit.WithInbox(inbox)); err != nil {
		t.Fatal(err)
	}
	if inbox.Len() != 1 || client.calls != 1 {
		t.Fatalf("typed run must not continue: len=%d calls=%d", inbox.Len(), client.calls)
	}
}
