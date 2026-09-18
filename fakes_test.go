package agentkit_test

import (
	"context"
	"errors"
	"iter"
	"sync"

	"github.com/richardwooding/llmkit/core"
)

// scripted returns canned responses (or errors) in order and records what it
// was sent.
type scripted struct {
	mu        sync.Mutex
	responses []*core.Response
	errs      []error
	calls     int
	seen      []*core.Request
}

func text(s string) *core.Response {
	return &core.Response{Message: core.Assistant(core.Text(s)), FinishReason: core.FinishStop, Usage: core.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}
}

func toolCalls(calls ...core.ToolCall) *core.Response {
	parts := make([]core.Part, len(calls))
	for i, c := range calls {
		parts[i] = c
	}
	return &core.Response{Message: core.Assistant(parts...), FinishReason: core.FinishToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}
}

func call(id, name, args string) core.ToolCall {
	return core.ToolCall{ID: id, Name: name, Arguments: []byte(args)}
}

func (s *scripted) Chat(_ context.Context, req *core.Request) (*core.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *req
	cp.Messages = append([]core.Message(nil), req.Messages...)
	cp.Tools = append([]core.Tool(nil), req.Tools...)
	s.seen = append(s.seen, &cp)
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	if i >= len(s.responses) {
		return nil, errors.New("scripted: no more responses")
	}
	return s.responses[i], nil
}

// scriptedStream adds a Stream method that replays each response as chunks.
type scriptedStream struct {
	*scripted
}

func (s *scriptedStream) Stream(ctx context.Context, req *core.Request) iter.Seq2[core.Chunk, error] {
	return func(yield func(core.Chunk, error) bool) {
		resp, err := s.Chat(ctx, req)
		if err != nil {
			yield(core.Chunk{}, err)
			return
		}
		idx := 0
		for _, p := range resp.Message.Parts {
			switch v := p.(type) {
			case core.TextPart:
				half := len(v.Text) / 2
				if !yield(core.Chunk{Kind: core.ChunkText, Text: v.Text[:half]}, nil) || !yield(core.Chunk{Kind: core.ChunkText, Text: v.Text[half:]}, nil) {
					return
				}
			case core.ReasoningPart:
				if !yield(core.Chunk{Kind: core.ChunkReasoning, Text: v.Text}, nil) {
					return
				}
			case core.ToolCall:
				args := string(v.Arguments)
				half := len(args) / 2
				if !yield(core.Chunk{Kind: core.ChunkToolCall, ToolCall: &core.ToolCallDelta{Index: idx, ID: v.ID, Name: v.Name, Arguments: args[:half]}}, nil) ||
					!yield(core.Chunk{Kind: core.ChunkToolCall, ToolCall: &core.ToolCallDelta{Index: idx, Arguments: args[half:]}}, nil) {
					return
				}
				idx++
			}
		}
		u := resp.Usage
		yield(core.Chunk{Kind: core.ChunkFinish, FinishReason: resp.FinishReason, Usage: &u}, nil)
	}
}

type fakeEmbedder struct {
	vectors map[string][]float32
}

func (f fakeEmbedder) Embed(_ context.Context, req *core.EmbedRequest) (*core.EmbedResponse, error) {
	out := &core.EmbedResponse{}
	for _, in := range req.Inputs {
		v, ok := f.vectors[in]
		if !ok {
			v = []float32{0, 0, 1}
		}
		out.Embeddings = append(out.Embeddings, v)
	}
	return out, nil
}
