package agentkit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

type answer struct {
	City string `json:"city"`
	Temp int    `json:"temp"`
}

func TestRunTypedSchemaMode(t *testing.T) {
	client := &scripted{responses: []*core.Response{text("```json\n{\"city\":\"Cape Town\",\"temp\":24}\n```")}}
	a, _ := agentkit.NewFromClient(client)
	out, res, err := agentkit.Run[answer](context.Background(), a, "weather?")
	if err != nil || out.City != "Cape Town" || out.Temp != 24 || res.StopReason != agentkit.StopFinalAnswer {
		t.Fatalf("out=%+v res=%+v err=%v", out, res, err)
	}
	f := client.seen[0].Format
	if f == nil || f.Type != core.FormatJSONSchema || len(f.Schema) == 0 || len(client.seen[0].Tools) != 0 {
		t.Fatalf("request = %+v", client.seen[0])
	}
}

func TestRunTypedToolMode(t *testing.T) {
	client := &scripted{responses: []*core.Response{
		toolCalls(call("c1", "add", `{"A":20,"B":4}`)),
		toolCalls(call("c2", agentkit.FinalAnswerTool, `{"city":"Cape Town","temp":24}`)),
	}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()))
	out, res, err := agentkit.Run[answer](context.Background(), a, "weather?")
	if err != nil || out.Temp != 24 || res.StopReason != agentkit.StopFinalAnswer || res.Steps != 2 {
		t.Fatalf("out=%+v res=%+v err=%v", out, res, err)
	}
	names := make([]string, 0, 2)
	for _, tl := range client.seen[0].Tools {
		names = append(names, tl.Name)
	}
	if len(names) != 2 || names[1] != agentkit.FinalAnswerTool || client.seen[0].Format != nil {
		t.Fatalf("tools = %v format = %v", names, client.seen[0].Format)
	}
	last := res.New[len(res.New)-1].ToolResults()
	if len(last) != 1 || last[0].CallID != "c2" || last[0].Text() != "recorded" {
		t.Fatalf("final answer pair = %+v", last)
	}
}

func TestRunTypedNudgeAndFailure(t *testing.T) {
	client := &scripted{responses: []*core.Response{text("it is 24 degrees"), toolCalls(call("c", agentkit.FinalAnswerTool, `{"city":"CT","temp":24}`))}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithTools(adder()))
	out, _, err := agentkit.Run[answer](context.Background(), a, "weather?")
	if err != nil || out.Temp != 24 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	second := client.seen[1]
	if second.ToolChoice.Mode != core.ToolChoiceNamed || second.ToolChoice.Name != agentkit.FinalAnswerTool || second.Messages[len(second.Messages)-1].Role != core.RoleUser {
		t.Fatalf("nudge request = %+v", second)
	}

	client = &scripted{responses: []*core.Response{text("no"), text("still no")}}
	a, _ = agentkit.NewFromClient(client, agentkit.WithTools(adder()))
	_, res, err := agentkit.Run[answer](context.Background(), a, "weather?")
	if !errors.Is(err, agentkit.ErrNoFinalAnswer) || res == nil {
		t.Fatalf("err = %v", err)
	}

	client = &scripted{responses: []*core.Response{toolCalls(call("c", agentkit.FinalAnswerTool, `{"city":1}`)), toolCalls(call("d", agentkit.FinalAnswerTool, `{"city":"x","temp":1}`))}}
	a, _ = agentkit.NewFromClient(client, agentkit.WithTools(adder()))
	out, _, err = agentkit.Run[answer](context.Background(), a, "weather?")
	if err != nil || out.City != "x" {
		t.Fatalf("invalid then valid: out=%+v err=%v", out, err)
	}
	if r := client.seen[1].Messages[len(client.seen[1].Messages)-1].ToolResults(); len(r) != 1 || !r[0].IsError {
		t.Fatalf("invalid final answer must be fed back as error: %+v", r)
	}

	client = &scripted{errs: []error{core.Unsupported("fake", "json_schema")}, responses: []*core.Response{nil, toolCalls(call("c", agentkit.FinalAnswerTool, `{"city":"y","temp":2}`))}}
	a, _ = agentkit.NewFromClient(client)
	out, _, err = agentkit.Run[answer](context.Background(), a, "weather?", agentkit.WithOutputMode(agentkit.OutputSchema))
	if err != nil || out.City != "y" || client.seen[1].Format != nil || len(client.seen[1].Tools) != 1 {
		t.Fatalf("fallback: out=%+v err=%v req=%+v", out, err, client.seen[1])
	}
}
