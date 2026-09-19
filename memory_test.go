package agentkit_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
	if len(entries) != 1 || entries[0].Name() != "..%2Fevil.jsonl" {
		t.Fatalf("entries = %v", entries)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "..%2Fevil.jsonl"))
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 3 || len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatalf("file = %q", raw)
	}
	for _, line := range lines {
		var rec struct {
			T time.Time       `json:"t"`
			M json.RawMessage `json:"m"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.T.IsZero() || len(rec.M) == 0 {
			t.Fatalf("line %q: %v", line, err)
		}
	}
	if info, _ := os.Stat(filepath.Join(dir, "..%2Fevil.jsonl")); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
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

func TestFileStoreMigratesLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	legacy := []core.Message{core.UserText("old question"), core.Assistant(core.Text("old answer"))}
	b, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, "s1.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	store := agentkit.NewFileStore(dir)
	got, err := store.Load(ctx, "s1")
	if err != nil || !reflect.DeepEqual(got, legacy) {
		t.Fatalf("legacy load = %+v %v", got, err)
	}
	infos, err := store.List(ctx)
	if err != nil || len(infos) != 1 || infos[0].ID != "s1" || infos[0].Messages != 2 || infos[0].Title != "old question" {
		t.Fatalf("legacy list = %+v %v", infos, err)
	}
	if err := store.Append(ctx, "s1", core.UserText("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy file should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.jsonl")); err != nil {
		t.Fatalf("migrated file missing: %v", err)
	}
	got, err = store.Load(ctx, "s1")
	if err != nil || len(got) != 3 || got[0].Text() != "old question" || got[2].Text() != "new" {
		t.Fatalf("after migration = %+v %v", got, err)
	}
	if err := store.Delete(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("entries after delete = %v", entries)
	}
}

func TestFileStoreList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	store := agentkit.NewFileStore(dir)
	ctx := context.Background()
	if infos, err := store.List(ctx); err != nil || len(infos) != 0 {
		t.Fatalf("empty list = %+v %v", infos, err)
	}
	long := strings.Repeat("é", 100)
	before := time.Now().Add(-time.Second)
	if err := store.Append(ctx, "a", core.UserText("  first line\nsecond line"), core.Assistant(core.Text("a1"))); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, "b/c", core.System("s"), core.UserText(long)); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, "b/c", core.Assistant(core.Text("x"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	infos, err := store.List(ctx)
	if err != nil || len(infos) != 2 {
		t.Fatalf("list = %+v %v", infos, err)
	}
	byID := map[string]agentkit.SessionInfo{}
	for _, i := range infos {
		byID[i.ID] = i
	}
	a, bc := byID["a"], byID["b/c"]
	if a.Messages != 2 || a.Title != "first line" || a.Created.Before(before) || a.Updated.Before(a.Created) {
		t.Fatalf("a = %+v", a)
	}
	if bc.Messages != 3 || len([]rune(bc.Title)) != 80 || !strings.HasSuffix(bc.Title, "…") {
		t.Fatalf("b/c = %+v", bc)
	}
	if !infos[0].Updated.Before(infos[1].Updated) && infos[0].ID != "b/c" {
		t.Fatalf("order = %v %v", infos[0].ID, infos[1].ID)
	}
	if _, ok := any(store).(agentkit.Lister); !ok {
		t.Fatal("FileStore must implement Lister")
	}
}

func TestFileStoreTornTail(t *testing.T) {
	dir := t.TempDir()
	store := agentkit.NewFileStore(dir)
	ctx := context.Background()
	if err := store.Append(ctx, "s", core.UserText("q"), core.Assistant(core.Text("a"))); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "s.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"t":"2026-09-19T10:00:00Z","m":{"role":"us`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got, err := store.Load(ctx, "s")
	if err != nil || len(got) != 2 || got[1].Text() != "a" {
		t.Fatalf("torn load = %+v %v", got, err)
	}
	infos, err := store.List(ctx)
	if err != nil || len(infos) != 1 || infos[0].Messages != 2 {
		t.Fatalf("torn list = %+v %v", infos, err)
	}
	if err := store.Append(ctx, "s", core.UserText("q2")); err != nil {
		t.Fatal(err)
	}
	got, err = store.Load(ctx, "s")
	if err != nil || len(got) != 3 || got[2].Text() != "q2" {
		t.Fatalf("after repair = %+v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.jsonl"), []byte("{oops}\n{\"t\":\"2026-09-19T10:00:00Z\",\"m\":{}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, "bad"); err == nil {
		t.Fatal("corruption before the last line must be reported")
	}
}

func TestMemoryStoreList(t *testing.T) {
	store := agentkit.NewMemoryStore()
	ctx := context.Background()
	if infos, err := store.List(ctx); err != nil || len(infos) != 0 {
		t.Fatalf("empty = %+v %v", infos, err)
	}
	_ = store.Append(ctx, "x", core.UserText("hello there"))
	time.Sleep(2 * time.Millisecond)
	_ = store.Append(ctx, "y", core.System("s"), core.UserText("second\nline"), core.Assistant(core.Text("r")))
	infos, err := store.List(ctx)
	if err != nil || len(infos) != 2 || infos[0].ID != "y" || infos[1].ID != "x" {
		t.Fatalf("list = %+v %v", infos, err)
	}
	if infos[0].Messages != 3 || infos[0].Title != "second" || infos[1].Title != "hello there" || infos[1].Created.IsZero() || infos[1].Updated.Before(infos[1].Created) {
		t.Fatalf("infos = %+v", infos)
	}
	var l agentkit.Lister = store
	_ = l
	_ = store.Delete(ctx, "x")
	if infos, _ := store.List(ctx); len(infos) != 1 {
		t.Fatalf("after delete = %+v", infos)
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

func TestSummarizeKeepZero(t *testing.T) {
	summarizer := &scripted{responses: []*core.Response{text("ALL")}}
	c := agentkit.Summarize(summarizer, 0)
	msgs := []core.Message{core.System("s"), core.UserText("q1"), core.Assistant(core.Text("a1")), core.UserText("q2")}
	out, err := c.Compact(context.Background(), msgs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Role != core.RoleSystem || out[1].Role != core.RoleUser || out[1].Text() != "Summary of the earlier conversation:\nALL" {
		t.Fatalf("out = %+v", out)
	}
	if sent := summarizer.seen[0].Messages; len(sent) != 4 || sent[2].Text() != "q2" {
		t.Fatalf("summary request = %+v", sent)
	}
	if out, _ := c.Compact(context.Background(), []core.Message{core.System("s")}, 0); len(out) != 1 {
		t.Fatalf("no turns must be untouched: %+v", out)
	}
}

func TestStripReasoning(t *testing.T) {
	msgs := []core.Message{
		core.System("s"),
		core.UserText("q"),
		core.Assistant(core.ReasoningPart{Text: "thinking", Signature: "sig"}, core.ToolCall{ID: "1", Name: "f"}),
		core.ToolResults(core.ToolResultText("1", "f", "r")),
		core.Assistant(core.ReasoningPart{Text: "only thoughts"}),
		core.Assistant(core.ReasoningPart{Text: "more"}, core.Text("answer")),
	}
	out, err := agentkit.StripReasoning().Compact(context.Background(), msgs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 5 || len(out[2].Parts) != 1 || len(out[2].ToolCalls()) != 1 || out[4].Text() != "answer" || len(out[4].Parts) != 1 {
		t.Fatalf("out = %+v", out)
	}
	for _, m := range out {
		for _, p := range m.Parts {
			if _, ok := p.(core.ReasoningPart); ok {
				t.Fatalf("reasoning survived: %+v", m)
			}
		}
	}
	if len(msgs[2].Parts) != 2 {
		t.Fatal("input mutated")
	}
	chained, _ := agentkit.Chain(agentkit.StripReasoning(), agentkit.Window(1)).Compact(context.Background(), msgs, 0)
	if len(chained) != 5 || chained[0].Role != core.RoleSystem {
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
