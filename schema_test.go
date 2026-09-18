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

type color string

type searchArgs struct {
	Query  string    `json:"query" jsonschema:"what to search for"`
	Limit  int       `json:"limit,omitempty" jsonschema:"maximum hits"`
	Tags   []string  `json:"tags,omitempty"`
	Since  time.Time `json:"since,omitzero"`
	Color  color     `json:"color,omitempty"`
	Nested *struct {
		A int `json:"a"`
	} `json:"nested,omitempty"`
	Options map[string]bool `json:"options,omitempty"`
}

func TestSchemaFor(t *testing.T) {
	raw, err := agentkit.SchemaFor[searchArgs](agentkit.WithEnum(color("red"), color("blue")))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Type                 string                     `json:"type"`
		Required             []string                   `json:"required"`
		AdditionalProperties any                        `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Type != "object" || len(s.Required) != 1 || s.Required[0] != "query" || s.AdditionalProperties != false {
		t.Fatalf("schema = %s", raw)
	}
	if !strings.Contains(string(s.Properties["query"]), `"description":"what to search for"`) {
		t.Fatalf("query = %s", s.Properties["query"])
	}
	if !strings.Contains(string(s.Properties["color"]), `"enum":["red","blue"]`) {
		t.Fatalf("color = %s", s.Properties["color"])
	}
	if !strings.Contains(string(s.Properties["since"]), `"string"`) || !strings.Contains(string(s.Properties["nested"]), `"a"`) {
		t.Fatalf("since/nested = %s %s", s.Properties["since"], s.Properties["nested"])
	}
	if _, err := agentkit.NewFunc("bad", "", func(context.Context, map[int]string) (string, error) { return "", nil }); err == nil {
		t.Fatal("non-string map keys must fail")
	}
	if _, err := agentkit.NewFunc("", "", func(context.Context, struct{}) (string, error) { return "", nil }); err == nil {
		t.Fatal("empty name must fail")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Func must panic on unrepresentable input")
		}
	}()
	agentkit.Func("bad", "", func(context.Context, chan int) (string, error) { return "", nil })
}

func TestFuncCallValidationAndOutputs(t *testing.T) {
	type in struct {
		N int `json:"n"`
	}
	type out struct {
		Double int `json:"double"`
	}
	tool := agentkit.Func("double", "doubles", func(_ context.Context, a in) (out, error) { return out{a.N * 2}, nil })
	if tool.Definition().Name != "double" || tool.Definition().Strict {
		t.Fatalf("def = %+v", tool.Definition())
	}
	res, err := tool.Call(context.Background(), json.RawMessage(`{"n":21}`))
	if err != nil || res.Text() != `{"double":42}` || res.IsError {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	res, err = tool.Call(context.Background(), json.RawMessage(`{"n":"x"}`))
	if !errors.Is(err, agentkit.ErrInvalidArgs) || !res.IsError || !strings.Contains(res.Text(), "invalid") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	res, err = tool.Call(context.Background(), json.RawMessage(`{"n":1,"extra":true}`))
	if !errors.Is(err, agentkit.ErrInvalidArgs) {
		t.Fatalf("additional property must fail validation: %v %+v", err, res)
	}
	lax := agentkit.Func("lax", "", func(_ context.Context, a in) (out, error) { return out{a.N}, nil }, agentkit.WithoutValidation())
	if res, err := lax.Call(context.Background(), json.RawMessage(`{"n":1,"extra":true}`)); err != nil || res.Text() != `{"double":1}` {
		t.Fatalf("lax: %+v %v", res, err)
	}

	str := agentkit.Func("s", "", func(context.Context, struct{}) (string, error) { return "plain", nil })
	if res, _ := str.Call(context.Background(), nil); res.Text() != "plain" {
		t.Fatalf("string out = %+v", res)
	}
	empty := agentkit.Func("e", "", func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	if res, _ := empty.Call(context.Background(), json.RawMessage("null")); res.Text() != "ok" {
		t.Fatalf("empty out = %+v", res)
	}
	parts := agentkit.Func("p", "", func(context.Context, struct{}) ([]core.Part, error) {
		return []core.Part{core.Image([]byte{1}, "image/png")}, nil
	})
	if res, _ := parts.Call(context.Background(), nil); len(res.Content) != 1 {
		t.Fatalf("parts out = %+v", res)
	}
	failing := agentkit.Func("f", "", func(context.Context, struct{}) (string, error) { return "", errors.New("boom") })
	if res, err := failing.Call(context.Background(), nil); err == nil || !res.IsError || res.Text() != "boom" {
		t.Fatalf("failing = %+v %v", res, err)
	}
	strict := agentkit.Func("st", "", func(context.Context, in) (string, error) { return "", nil }, agentkit.WithStrict(), agentkit.WithSchema(json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`)))
	if d := strict.Definition(); !d.Strict || !strings.Contains(string(d.Parameters), `"required":["n"]`) {
		t.Fatalf("strict def = %+v", d)
	}
}

func TestToolsetAndMiddleware(t *testing.T) {
	a := agentkit.Raw("a", "", nil, func(context.Context, json.RawMessage) (agentkit.Output, error) { return agentkit.Text("A"), nil })
	b := agentkit.Raw("b", "", nil, func(context.Context, json.RawMessage) (agentkit.Output, error) { panic("kaboom") })
	if _, err := agentkit.Merge(agentkit.Toolset{a}, agentkit.Toolset{a}); !errors.Is(err, agentkit.ErrDuplicateTool) {
		t.Fatalf("err = %v", err)
	}
	set := agentkit.Toolset{a, b}.Prefix("fs_")
	if got := set.Names(); got[0] != "fs_a" || got[1] != "fs_b" {
		t.Fatalf("names = %v", got)
	}
	if _, ok := set.Lookup("fs_b"); !ok {
		t.Fatal("lookup failed")
	}
	if string(a.Definition().Parameters) != `{"type":"object"}` {
		t.Fatalf("raw default schema = %s", a.Definition().Parameters)
	}

	recovered := agentkit.Wrap(b, agentkit.Recover())
	res, err := recovered.Call(context.Background(), nil)
	if err == nil || !res.IsError || !strings.Contains(res.Text(), "kaboom") {
		t.Fatalf("recover: %+v %v", res, err)
	}

	denied := agentkit.Wrap(a, agentkit.Approve(func(_ context.Context, c agentkit.Call) error {
		if c.Call.Name == "a" {
			return errors.New("nope")
		}
		return nil
	}))
	res, err = denied.Call(context.Background(), nil)
	if !errors.Is(err, agentkit.ErrApprovalDenied) || !res.IsError {
		t.Fatalf("approve: %+v %v", res, err)
	}

	slow := agentkit.Raw("slow", "", nil, func(ctx context.Context, _ json.RawMessage) (agentkit.Output, error) {
		<-ctx.Done()
		return agentkit.Text(""), ctx.Err()
	})
	if _, err := agentkit.Wrap(slow, agentkit.Timeout(10*time.Millisecond)).Call(context.Background(), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}

	var observed agentkit.Call
	obs := agentkit.Wrap(a, agentkit.Observe(func(c agentkit.Call, _ agentkit.Output, _ error, d time.Duration) { observed = c }))
	ctx := agentkit.WithCall(context.Background(), agentkit.Call{RunID: "r1", Call: core.ToolCall{Name: "a"}})
	if _, err := obs.Call(ctx, nil); err != nil || observed.RunID != "r1" {
		t.Fatalf("observe: %v %+v", err, observed)
	}
	if s, ok := agentkit.Wrap(a, agentkit.Serial(), agentkit.Recover()).(agentkit.Sequential); !ok || !s.Sequential() {
		t.Fatal("Serial must survive outer middleware")
	}
}
