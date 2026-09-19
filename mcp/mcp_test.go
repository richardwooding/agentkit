package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
	agentmcp "github.com/richardwooding/agentkit/mcp"
)

type echoIn struct {
	Message string `json:"message" jsonschema:"text to echo back"`
}

type structuredOut struct {
	Sum int `json:"sum"`
}

var pngBytes = []byte{0x89, 'P', 'N', 'G'}

func newTestServer() *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "0.0.1"}, nil)
	sdk.AddTool(srv, &sdk.Tool{Name: "echo", Description: "Echoes its input"},
		func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "echo: " + in.Message}}}, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "image", Description: "Returns a picture"},
		func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.ImageContent{Data: pngBytes, MIMEType: "image/png"}}}, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "fail", Description: "Always fails"},
		func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "boom"}}}, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "typed", Description: "Typed structured output"},
		func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, structuredOut, error) {
			return nil, structuredOut{Sum: 3}, nil
		})
	srv.AddTool(&sdk.Tool{Name: "structured", Description: "Structured only", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{StructuredContent: map[string]any{"sum": 3}}, nil
		})
	srv.AddTool(&sdk.Tool{Name: "resource", Description: "Embedded resources", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///a.txt", MIMEType: "text/plain", Text: "hello"}},
				&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///a.pdf", MIMEType: "application/pdf", Blob: []byte("%PDF")}},
				&sdk.ResourceLink{URI: "file:///b.txt", Name: "b"},
				&sdk.AudioContent{Data: []byte{1, 2}, MIMEType: "audio/wav"},
			}}, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "annotated", Description: "Carries hints", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, Title: "Read only", OpenWorldHint: new(false)}},
		func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "hinted"}}}, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "fs.read/file", Description: "Awkward name"},
		func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "read"}}}, nil, nil
		})
	return srv
}

func connect(t *testing.T, opts ...agentmcp.Option) *agentmcp.Server {
	t.Helper()
	ctx := context.Background()
	clientT, serverT := sdk.NewInMemoryTransports()
	ss, err := newTestServer().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })
	s, err := agentmcp.Connect(ctx, clientT, opts...)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func tools(t *testing.T, opts ...agentmcp.Option) agentkit.Toolset {
	t.Helper()
	ts, err := connect(t, opts...).Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	return ts
}

func lookup(t *testing.T, ts agentkit.Toolset, name string) agentkit.Tool {
	t.Helper()
	tool, ok := ts.Lookup(name)
	if !ok {
		t.Fatalf("tool %q not found in %v", name, ts.Names())
	}
	return tool
}

func TestToolsNames(t *testing.T) {
	want := []string{"annotated", "echo", "fail", "fs_read_file", "image", "resource", "structured", "typed"}
	got := tools(t).Names()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestWithPrefix(t *testing.T) {
	ts := tools(t, agentmcp.WithPrefix("fs_"))
	for _, name := range ts.Names() {
		if !strings.HasPrefix(name, "fs_") {
			t.Errorf("name %q lacks prefix", name)
		}
	}
	tool := lookup(t, ts, "fs_echo")
	out, err := tool.Call(context.Background(), json.RawMessage(`{"message":"hi"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Text() != "echo: hi" {
		t.Fatalf("text = %q", out.Text())
	}
}

func TestWithFilter(t *testing.T) {
	ts := tools(t, agentmcp.WithFilter(func(name string) bool { return name == "echo" || name == "fail" }))
	if got := strings.Join(ts.Names(), ","); got != "echo,fail" {
		t.Fatalf("names = %q", got)
	}
}

func TestDefinitionSchema(t *testing.T) {
	def := lookup(t, tools(t), "echo").Definition()
	if def.Description != "Echoes its input" {
		t.Errorf("description = %q", def.Description)
	}
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		t.Fatalf("parameters not JSON: %v: %s", err, def.Parameters)
	}
	if schema.Type != "object" {
		t.Errorf("type = %q", schema.Type)
	}
	if _, ok := schema.Properties["message"]; !ok {
		t.Errorf("schema lacks message property: %s", def.Parameters)
	}
	if !strings.Contains(string(def.Parameters), "text to echo back") {
		t.Errorf("schema lacks property description: %s", def.Parameters)
	}
}

func TestDefinitionEmptySchema(t *testing.T) {
	def := lookup(t, tools(t), "structured").Definition()
	if string(def.Parameters) != `{"type":"object"}` {
		t.Fatalf("parameters = %s", def.Parameters)
	}
}

func TestCallEcho(t *testing.T) {
	out, err := lookup(t, tools(t), "echo").Call(context.Background(), json.RawMessage(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.IsError {
		t.Error("IsError = true")
	}
	if out.Text() != "echo: hello" {
		t.Errorf("text = %q", out.Text())
	}
}

func TestCallEmptyArgs(t *testing.T) {
	for _, args := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("null"), json.RawMessage(" ")} {
		out, err := lookup(t, tools(t), "image").Call(context.Background(), args)
		if err != nil {
			t.Fatalf("Call(%q): %v", args, err)
		}
		if len(out.Content) != 1 {
			t.Fatalf("Call(%q): %d parts", args, len(out.Content))
		}
	}
}

func TestCallImage(t *testing.T) {
	out, err := lookup(t, tools(t), "image").Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(out.Content) != 1 {
		t.Fatalf("%d parts", len(out.Content))
	}
	img, ok := out.Content[0].(core.ImagePart)
	if !ok {
		t.Fatalf("part is %T", out.Content[0])
	}
	if img.MIME != "image/png" || string(img.Data) != string(pngBytes) {
		t.Errorf("image = %+v", img)
	}
}

func TestCallFail(t *testing.T) {
	out, err := lookup(t, tools(t), "fail").Call(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("err = nil")
	}
	if !out.IsError {
		t.Error("IsError = false")
	}
	if out.Text() != "boom" {
		t.Errorf("text = %q", out.Text())
	}
	if !strings.Contains(err.Error(), "fail") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}

func TestCallStructured(t *testing.T) {
	for _, name := range []string{"structured", "typed"} {
		out, err := lookup(t, tools(t), name).Call(context.Background(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("%s: Call: %v", name, err)
		}
		var got structuredOut
		if err := json.Unmarshal([]byte(out.Text()), &got); err != nil {
			t.Fatalf("%s: text %q is not JSON: %v", name, out.Text(), err)
		}
		if got.Sum != 3 {
			t.Errorf("%s: sum = %d", name, got.Sum)
		}
	}
}

func TestCallResources(t *testing.T) {
	out, err := lookup(t, tools(t), "resource").Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(out.Content) != 4 {
		t.Fatalf("%d parts: %#v", len(out.Content), out.Content)
	}
	if txt, ok := out.Content[0].(core.TextPart); !ok || txt.Text != "hello" {
		t.Errorf("part 0 = %#v", out.Content[0])
	}
	if f, ok := out.Content[1].(core.FilePart); !ok || f.MIME != "application/pdf" || f.Name != "file:///a.pdf" || string(f.Data) != "%PDF" {
		t.Errorf("part 1 = %#v", out.Content[1])
	}
	if txt, ok := out.Content[2].(core.TextPart); !ok || txt.Text != "file:///b.txt" {
		t.Errorf("part 2 = %#v", out.Content[2])
	}
	if a, ok := out.Content[3].(core.AudioPart); !ok || a.MIME != "audio/wav" || len(a.Data) != 2 {
		t.Errorf("part 3 = %#v", out.Content[3])
	}
}

func TestSanitizedNameCallsOriginal(t *testing.T) {
	out, err := lookup(t, tools(t), "fs_read_file").Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Text() != "read" {
		t.Errorf("text = %q", out.Text())
	}
}

func TestSequential(t *testing.T) {
	seq := func(tool agentkit.Tool) bool {
		s, ok := tool.(agentkit.Sequential)
		return ok && s.Sequential()
	}
	if seq(lookup(t, tools(t), "echo")) {
		t.Error("Sequential() = true without WithSequential")
	}
	if !seq(lookup(t, tools(t, agentmcp.WithSequential()), "echo")) {
		t.Error("Sequential() = false with WithSequential")
	}
}

func TestAnnotations(t *testing.T) {
	ts := tools(t)
	hinted, ok := lookup(t, ts, "annotated").(agentmcp.Annotated)
	if !ok {
		t.Fatal("tool does not implement Annotated")
	}
	ann := hinted.Annotations()
	if ann == nil || !ann.ReadOnlyHint || ann.Title != "Read only" || ann.OpenWorldHint == nil || *ann.OpenWorldHint {
		t.Fatalf("annotations = %+v", ann)
	}
	if plain := lookup(t, ts, "echo").(agentmcp.Annotated); plain.Annotations() != nil {
		t.Fatalf("echo annotations = %+v", plain.Annotations())
	}
}

func TestCloseTwice(t *testing.T) {
	s := connect(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	_, err := s.Tools(context.Background())
	if err == nil {
		t.Fatal("Tools after Close succeeded")
	}
}

func TestSession(t *testing.T) {
	s := connect(t)
	if s.Session() == nil {
		t.Fatal("Session() = nil")
	}
	if err := s.Session().Ping(context.Background(), nil); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestCallAfterCloseReturnsError(t *testing.T) {
	s := connect(t)
	ts, err := s.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	s.Close()
	out, err := lookup(t, ts, "echo").Call(context.Background(), json.RawMessage(`{"message":"x"}`))
	if err == nil {
		t.Fatal("err = nil after Close")
	}
	if !errors.Is(err, sdk.ErrConnectionClosed) {
		t.Logf("err = %v (not wrapping ErrConnectionClosed)", err)
	}
	if out.IsError || len(out.Content) != 0 {
		t.Errorf("out = %+v, want zero", out)
	}
}

func TestWithImplementation(t *testing.T) {
	srv := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "0.0.1"}, nil)
	clientT, serverT := sdk.NewInMemoryTransports()
	ss, err := srv.Connect(context.Background(), serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()
	s, err := agentmcp.Connect(context.Background(), clientT, agentmcp.WithImplementation("myagent", "9.9.9"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer s.Close()
	got := ss.InitializeParams().ClientInfo
	if got.Name != "myagent" || got.Version != "9.9.9" {
		t.Fatalf("client info = %+v", got)
	}
}

func TestTransports(t *testing.T) {
	if _, ok := agentmcp.Command("echo", "hi").(*sdk.CommandTransport); !ok {
		t.Error("Command did not return *sdk.CommandTransport")
	}
	ht, ok := agentmcp.HTTP("http://127.0.0.1:0/mcp").(*sdk.StreamableClientTransport)
	if !ok {
		t.Fatal("HTTP did not return *sdk.StreamableClientTransport")
	}
	if ht.Endpoint != "http://127.0.0.1:0/mcp" || ht.HTTPClient != nil {
		t.Errorf("transport = %+v", ht)
	}
}
