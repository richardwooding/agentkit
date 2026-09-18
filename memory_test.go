package agentkit_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit"
)

func TestFileStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	store := agentkit.NewFileStore(dir)
	ctx := context.Background()
	got, err := store.Load(ctx, "missing")
	if err != nil || got != nil {
		t.Fatalf("missing = %v %v", got, err)
	}
	msgs := []core.Message{
		core.UserText("hi"),
		core.Assistant(core.ReasoningPart{Text: "r", Signature: "s"}, core.ToolCall{ID: "1", Name: "f", Arguments: json.RawMessage(`{"a":1}`)}),
		core.ToolResults(core.ToolResult{CallID: "1", Name: "f", Content: []core.Part{core.Image([]byte{1}, "image/png")}}),
	}
	if err := store.Append(ctx, "../evil", msgs[:1]...); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, "../evil", msgs[1:]...); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "..%2Fevil.json" {
		t.Fatalf("entries = %v", entries)
	}
	got, err = store.Load(ctx, "../evil")
	if err != nil || !reflect.DeepEqual(got, msgs) {
		t.Fatalf("round trip = %+v %v", got, err)
	}
	if err := store.Delete(ctx, "../evil"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Load(ctx, "../evil"); got != nil {
		t.Fatal("delete failed")
	}
}

func TestWindowKeepsToolPairs(t *testing.T) {
	msgs := []core.Message{
		core.System("s"),
		core.UserText("q1"),
		core.Assistant(core.ToolCall{ID: "1", Name: "f"}),
		core.ToolResults(core.ToolResultText("1", "f", "r")),
		core.Assistant(core.Text("a1")),
		core.UserText("q2"),
		core.Assistant(core.ToolCall{ID: "2", Name: "f"}),
		core.ToolResults(core.ToolResultText("2", "f", "r")),
		core.Assistant(core.Text("a2")),
		core.UserText("q3"),
	}
	out, err := agentkit.Window(2).Compact(context.Background(), msgs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 6 || out[0].Role != core.RoleSystem || out[1].Text() != "q2" || out[5].Text() != "q3" {
		t.Fatalf("window = %+v", out)
	}
	for i, m := range out {
		if len(m.ToolCalls()) > 0 && len(out[i+1].ToolResults()) == 0 {
			t.Fatalf("orphaned tool call at %d", i)
		}
	}
	tight, _ := agentkit.Window(0).Compact(context.Background(), msgs, 10)
	if len(tight) != 2 || tight[1].Text() != "q3" {
		t.Fatalf("budget window = %+v", tight)
	}
	est := agentkit.CharEstimator{}.Estimate(msgs)
	if est <= 0 || (agentkit.CharEstimator{}).Estimate(msgs[:1]) >= est {
		t.Fatalf("estimate = %d", est)
	}
}

func TestSummarize(t *testing.T) {
	summarizer := &scripted{responses: []*core.Response{text("SUMMARY"), text("SUMMARY2")}}
	c := agentkit.Summarize(summarizer, 1)
	msgs := []core.Message{core.System("s"), core.UserText("q1"), core.Assistant(core.Text("a1")), core.UserText("q2"), core.Assistant(core.Text("a2")), core.UserText("q3")}
	out, err := c.Compact(context.Background(), msgs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].Role != core.RoleSystem || out[1].Role != core.RoleUser || out[1].Text() != "Summary of the earlier conversation:\nSUMMARY" || out[2].Text() != "q3" {
		t.Fatalf("out = %+v", out)
	}
	sent := summarizer.seen[0].Messages
	if len(sent) != 5 || sent[0].Text() != "q1" || sent[4].Role != core.RoleUser {
		t.Fatalf("summary request = %+v", sent)
	}
	if _, err := c.Compact(context.Background(), msgs, 0); err != nil || summarizer.calls != 1 {
		t.Fatalf("memoisation failed: calls=%d err=%v", summarizer.calls, err)
	}
	short := []core.Message{core.UserText("only")}
	if out, _ := c.Compact(context.Background(), short, 0); len(out) != 1 {
		t.Fatalf("short history must be untouched: %+v", out)
	}
	chained, _ := agentkit.Chain(agentkit.Window(5), c).Compact(context.Background(), msgs, 0)
	if len(chained) != 3 {
		t.Fatalf("chain = %+v", chained)
	}
}

type fakeReranker struct{}

func (fakeReranker) Rerank(_ context.Context, req *core.RerankRequest) (*core.RerankResponse, error) {
	out := &core.RerankResponse{}
	for i := range slices.Backward(req.Documents) {
		out.Results = append(out.Results, core.RerankResult{Index: i, Score: 1})
	}
	return out, nil
}

func TestVectorMemoryAndRecall(t *testing.T) {
	emb := fakeEmbedder{vectors: map[string][]float32{
		"cats purr": {1, 0, 0}, "dogs bark": {0, 1, 0}, "fish swim": {0.1, 0, 1}, "feline?": {0.9, 0.1, 0},
	}}
	mem := agentkit.NewVectorMemory(emb)
	if err := mem.Add(context.Background(), "cats purr", "dogs bark", "fish swim"); err != nil {
		t.Fatal(err)
	}
	hits, err := mem.Retrieve(context.Background(), "feline?", 2)
	if err != nil || len(hits) != 2 || hits[0] != "cats purr" || mem.Len() != 3 {
		t.Fatalf("hits = %v err = %v", hits, err)
	}
	reranked := agentkit.NewVectorMemory(emb, agentkit.WithReranker(fakeReranker{}))
	_ = reranked.Add(context.Background(), "cats purr", "dogs bark")
	hits, _ = reranked.Retrieve(context.Background(), "feline?", 1)
	if len(hits) != 1 || hits[0] != "dogs bark" {
		t.Fatalf("reranked = %v", hits)
	}
	recall := agentkit.Recall(mem, 1)
	res, err := recall.Call(context.Background(), json.RawMessage(`{"query":"feline?"}`))
	if err != nil || res.Text() != "cats purr" || recall.Definition().Name != agentkit.RecallToolName {
		t.Fatalf("recall = %+v %v", res, err)
	}
	client := &scripted{responses: []*core.Response{text("ok")}}
	a, _ := agentkit.NewFromClient(client, agentkit.WithRetriever(mem, 2))
	if _, err := a.Run(context.Background(), "x"); err != nil || len(client.seen[0].Tools) != 1 || client.seen[0].Tools[0].Name != "recall" {
		t.Fatalf("retriever tool missing: %v %+v", err, client.seen[0].Tools)
	}
}
