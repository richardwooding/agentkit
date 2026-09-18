package agentkit

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/richardwooding/llmkit/core"
)

// Retriever returns the k texts most relevant to a query.
type Retriever interface {
	Retrieve(ctx context.Context, query string, k int) ([]string, error)
}

// VectorOption configures VectorMemory.
type VectorOption func(*VectorMemory)

// WithReranker reorders the top candidates with a cross-encoder.
func WithReranker(r core.Reranker) VectorOption { return func(m *VectorMemory) { m.rerank = r } }

// WithDimensions requests a fixed embedding size from the embedder.
func WithDimensions(n int) VectorOption { return func(m *VectorMemory) { m.dims = n } }

// VectorMemory is an in-memory store of texts searched by cosine similarity.
type VectorMemory struct {
	embed  core.Embedder
	rerank core.Reranker
	dims   int
	mu     sync.RWMutex
	texts  []string
	vecs   [][]float32
}

// NewVectorMemory builds an empty memory over the embedder.
func NewVectorMemory(e core.Embedder, opts ...VectorOption) *VectorMemory {
	m := &VectorMemory{embed: e}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Add embeds and stores texts.
func (m *VectorMemory) Add(ctx context.Context, texts ...string) error {
	if len(texts) == 0 {
		return nil
	}
	resp, err := m.embed.Embed(ctx, &core.EmbedRequest{Inputs: texts, Dimensions: m.dims, InputType: core.EmbedDocument})
	if err != nil {
		return fmt.Errorf("agentkit: embed: %w", err)
	}
	if len(resp.Embeddings) != len(texts) {
		return fmt.Errorf("agentkit: embedder returned %d vectors for %d texts", len(resp.Embeddings), len(texts))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.texts = append(m.texts, texts...)
	for _, v := range resp.Embeddings {
		m.vecs = append(m.vecs, normalize(v))
	}
	return nil
}

// Len returns the number of stored texts.
func (m *VectorMemory) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.texts)
}

// Retrieve returns up to k texts, best first.
func (m *VectorMemory) Retrieve(ctx context.Context, query string, k int) ([]string, error) {
	if k <= 0 {
		k = 5
	}
	resp, err := m.embed.Embed(ctx, &core.EmbedRequest{Inputs: []string{query}, Dimensions: m.dims, InputType: core.EmbedQuery})
	if err != nil {
		return nil, fmt.Errorf("agentkit: embed query: %w", err)
	}
	if len(resp.Embeddings) != 1 {
		return nil, fmt.Errorf("agentkit: embedder returned %d vectors for the query", len(resp.Embeddings))
	}
	q := normalize(resp.Embeddings[0])
	m.mu.RLock()
	type hit struct {
		i     int
		score float32
	}
	hits := make([]hit, len(m.vecs))
	for i, v := range m.vecs {
		hits[i] = hit{i, dot(q, v)}
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	candidates := min(len(hits), k*4)
	if m.rerank == nil {
		candidates = min(len(hits), k)
	}
	texts := make([]string, candidates)
	for i := range candidates {
		texts[i] = m.texts[hits[i].i]
	}
	m.mu.RUnlock()
	if m.rerank == nil || len(texts) == 0 {
		return texts, nil
	}
	rr, err := m.rerank.Rerank(ctx, &core.RerankRequest{Query: query, Documents: texts, TopN: k})
	if err != nil {
		return nil, fmt.Errorf("agentkit: rerank: %w", err)
	}
	out := make([]string, 0, min(k, len(rr.Results)))
	for _, r := range rr.Results {
		if r.Index >= 0 && r.Index < len(texts) && len(out) < k {
			out = append(out, texts[r.Index])
		}
	}
	return out, nil
}

func normalize(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(n))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

func dot(a, b []float32) float32 {
	var s float32
	for i := range min(len(a), len(b)) {
		s += a[i] * b[i]
	}
	return s
}

// RecallToolName is the name of the tool Recall builds.
const RecallToolName = "recall"

type recallArgs struct {
	Query string `json:"query" jsonschema:"what to look up in memory"`
}

// Recall builds a tool that lets the model search a Retriever.
func Recall(r Retriever, k int) Tool {
	return Func(RecallToolName, "Search long-term memory for information relevant to a query.",
		func(ctx context.Context, in recallArgs) (string, error) {
			hits, err := r.Retrieve(ctx, in.Query, k)
			if err != nil {
				return "", err
			}
			if len(hits) == 0 {
				return "No relevant memories found.", nil
			}
			return strings.Join(hits, "\n---\n"), nil
		})
}
