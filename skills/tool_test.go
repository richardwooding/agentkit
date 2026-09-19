package skills_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
	"github.com/richardwooding/agentkit/skills"
)

type scripted struct {
	mu        sync.Mutex
	responses []*core.Response
	seen      []*core.Request
}

func (s *scripted) Chat(_ context.Context, req *core.Request) (*core.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *req
	cp.Messages = append([]core.Message(nil), req.Messages...)
	cp.Tools = append([]core.Tool(nil), req.Tools...)
	s.seen = append(s.seen, &cp)
	if len(s.responses) == 0 {
		return nil, errors.New("scripted: no more responses")
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	return r, nil
}

func text(s string) *core.Response {
	return &core.Response{Message: core.Assistant(core.Text(s)), FinishReason: core.FinishStop}
}

func toolCall(id, name, args string) *core.Response {
	return &core.Response{Message: core.Assistant(core.ToolCall{ID: id, Name: name, Arguments: []byte(args)}), FinishReason: core.FinishToolCalls}
}

func TestUse(t *testing.T) {
	set, err := skills.LoadAll(testFS(), skills.WithStrict())
	if err != nil {
		t.Fatal(err)
	}
	client := &scripted{responses: []*core.Response{
		toolCall("c1", skills.ToolName, `{"name":"pdf-processing"}`),
		toolCall("c2", skills.FileToolName, `{"skill":"pdf-processing","path":"references/REFERENCE.md"}`),
		toolCall("c3", skills.FileToolName, `{"skill":"pdf-processing","path":"assets/logo.png"}`),
		toolCall("c4", skills.ToolName, `{"name":"nope"}`),
		text("done"),
	}}
	a, err := agentkit.NewFromClient(client, agentkit.WithInstructions("base"), skills.Use(set))
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Run(context.Background(), "extract this pdf")
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "done" || res.ToolCalls != 4 {
		t.Fatalf("res = %+v", res)
	}

	first := client.seen[0]
	sys := first.Messages[0].Text()
	if !strings.HasPrefix(sys, "base\n\n") || !strings.Contains(sys, "<available_skills>") ||
		!strings.Contains(sys, "<name>pdf-processing</name>") || !strings.Contains(sys, "<name>deep-skill</name>") {
		t.Fatalf("system = %q", sys)
	}
	if len(first.Tools) != 2 {
		t.Fatalf("tools = %v", first.Tools)
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(first.Tools[0].Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if e := schema.Properties["name"].Enum; len(e) != 2 || e[0] != "deep-skill" || e[1] != "pdf-processing" {
		t.Fatalf("enum = %v", e)
	}

	results := func(i int) []core.ToolResult {
		return client.seen[i].Messages[len(client.seen[i].Messages)-1].ToolResults()
	}
	body := agentkit.Output{Content: results(1)[0].Content}.Text()
	for _, want := range []string{`<skill_content name="pdf-processing">`, "Do the thing.", "Skill directory: pdf-processing", "<file>references/REFERENCE.md</file>", "</skill_content>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("skill body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<file>SKILL.md</file>") {
		t.Fatalf("SKILL.md listed as a resource:\n%s", body)
	}
	if got := (agentkit.Output{Content: results(2)[0].Content}).Text(); got != "# Reference" {
		t.Fatalf("reference = %q", got)
	}
	img, ok := results(3)[0].Content[0].(core.ImagePart)
	if !ok || img.MIME != "image/png" || len(img.Data) != 4 {
		t.Fatalf("image = %+v", results(3)[0].Content)
	}
	if bad := results(4)[0]; !bad.IsError || !strings.Contains((agentkit.Output{Content: bad.Content}).Text(), "deep-skill, pdf-processing") {
		t.Fatalf("unknown skill result = %+v", bad)
	}
}

func TestFileToolRejectsEscapes(t *testing.T) {
	set, _ := skills.LoadAll(testFS(), skills.WithStrict())
	ft := skills.FileTool(set)
	for _, p := range []string{"../misnamed/SKILL.md", "/abs", "scripts/../../x"} {
		out, err := ft.Call(context.Background(), json.RawMessage(`{"skill":"pdf-processing","path":"`+p+`"}`))
		if err == nil || !out.IsError {
			t.Fatalf("path %q accepted: %+v", p, out)
		}
	}
	out, err := ft.Call(context.Background(), json.RawMessage(`{"skill":"pdf-processing","path":"missing.md"}`))
	if err == nil || !out.IsError {
		t.Fatalf("missing file accepted: %+v", out)
	}
}

func TestSkillToolIsPinned(t *testing.T) {
	set, _ := skills.LoadAll(testFS(), skills.WithStrict())
	if p, ok := skills.Tool(set).(agentkit.Pinned); !ok || !p.Pinned() {
		t.Fatal("skill tool must be pinned")
	}
	if _, ok := skills.FileTool(set).(agentkit.Pinned); ok {
		t.Fatal("file tool must not be pinned")
	}
	odd := skills.NewSet(&skills.Skill{Name: "odd", Description: "A & B <c>"})
	if p := odd.Prompt(); !strings.Contains(p, "<description>A &amp; B &lt;c&gt;</description>") {
		t.Fatalf("prompt escaping: %q", p)
	}
}
