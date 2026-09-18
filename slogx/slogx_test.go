package slogx_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
	"github.com/richardwooding/agentkit/slogx"
)

func TestHooksLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := slogx.Hooks(logger, slogx.WithArguments())
	call := agentkit.Call{RunID: "r1", Agent: "a", Call: core.ToolCall{ID: "c1", Name: "add", Arguments: []byte(`{"a":1}`)}}
	h.OnRunStart(agentkit.RunInfo{RunID: "r1", Agent: "a"})
	h.OnStep(agentkit.StepInfo{RunID: "r1", Step: 1})
	h.OnModelCall(agentkit.ModelCallInfo{RunID: "r1", Request: &core.Request{}})
	h.OnModelResponse(agentkit.ModelResponseInfo{RunID: "r1", Response: &core.Response{}})
	h.OnModelResponse(agentkit.ModelResponseInfo{RunID: "r1", Err: errors.New("down")})
	h.OnToolCall(agentkit.ToolCallInfo{Call: call})
	h.OnToolResult(agentkit.ToolResultInfo{Call: call, Output: agentkit.Text("2"), Duration: time.Millisecond})
	h.OnToolResult(agentkit.ToolResultInfo{Call: call, Err: errors.New("bad")})
	h.OnRetry(agentkit.RetryInfo{RunID: "r1", Attempt: 1, Delay: time.Second, Err: errors.New("429")})
	h.OnCompact(agentkit.CompactInfo{RunID: "r1", Reason: "proactive", Before: 10, After: 5})
	h.OnHandoff(agentkit.HandoffInfo{RunID: "r1", From: "a", To: "b"})
	h.OnRunEnd(&agentkit.Result{RunID: "r1", StopReason: agentkit.StopCompleted}, nil)
	h.OnRunEnd(&agentkit.Result{RunID: "r1", StopReason: agentkit.StopError}, errors.New("boom"))
	out := buf.String()
	for _, want := range []string{
		`level=INFO msg="agent run start"`, `msg="agent step"`, `level=DEBUG msg="model call"`, `level=WARN msg="model error"`,
		`msg="tool call" run=r1 agent=a tool=add call=c1 args="{\"a\":1}"`, `msg="tool result"`, `result=2`,
		`level=WARN msg="tool error"`, `level=WARN msg="model retry"`, `msg="history compacted"`, `msg=handoff`,
		`msg="agent run end"`, `level=WARN msg="agent run failed"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	quiet := slogx.Hooks(nil, slogx.WithLevel(slog.LevelDebug))
	quiet.OnToolCall(agentkit.ToolCallInfo{Call: call})
	_ = context.Background()
}
