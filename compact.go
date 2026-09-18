package agentkit

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/richardwooding/llmkit/core"
)

// Estimator approximates the prompt tokens a message list will cost.
type Estimator interface {
	Estimate(msgs []core.Message) int
}

// CharEstimator divides text length by CharsPerToken and charges flat costs
// per message and per binary part. Zero fields take the defaults 4, 4, 800.
type CharEstimator struct {
	CharsPerToken int
	PerMessage    int
	PerBinaryPart int
}

// Estimate implements Estimator.
func (e CharEstimator) Estimate(msgs []core.Message) int {
	cpt, perMsg, perBin := e.CharsPerToken, e.PerMessage, e.PerBinaryPart
	if cpt <= 0 {
		cpt = 4
	}
	if perMsg <= 0 {
		perMsg = 4
	}
	if perBin <= 0 {
		perBin = 800
	}
	total := 0
	for _, m := range msgs {
		total += perMsg
		for _, p := range m.Parts {
			total += estimatePart(p, cpt, perBin)
		}
	}
	return total
}

func estimatePart(p core.Part, cpt, perBin int) int {
	switch v := p.(type) {
	case core.TextPart:
		return len(v.Text)/cpt + 1
	case core.ReasoningPart:
		return len(v.Text)/cpt + 1
	case core.ToolCall:
		return (len(v.Name)+len(v.Arguments))/cpt + 4
	case core.ToolResult:
		n := 4
		for _, c := range v.Content {
			n += estimatePart(c, cpt, perBin)
		}
		return n
	default:
		return perBin
	}
}

// Compactor shrinks the history sent to the model to roughly budgetTokens.
// Implementations must keep tool calls with their results.
type Compactor interface {
	Compact(ctx context.Context, msgs []core.Message, budgetTokens int) ([]core.Message, error)
}

// CompactorFunc adapts a function to Compactor.
type CompactorFunc func(ctx context.Context, msgs []core.Message, budgetTokens int) ([]core.Message, error)

// Compact implements Compactor.
func (f CompactorFunc) Compact(ctx context.Context, msgs []core.Message, budget int) ([]core.Message, error) {
	return f(ctx, msgs, budget)
}

// Chain applies compactors in order.
func Chain(cs ...Compactor) Compactor {
	return CompactorFunc(func(ctx context.Context, msgs []core.Message, budget int) ([]core.Message, error) {
		var err error
		for _, c := range cs {
			if msgs, err = c.Compact(ctx, msgs, budget); err != nil {
				return nil, err
			}
		}
		return msgs, nil
	})
}

// splitTurns separates leading system messages from turns. A turn starts at a
// user message and runs to the next one, so tool calls stay with their results.
func splitTurns(msgs []core.Message) (system []core.Message, turns [][]core.Message) {
	i := 0
	for i < len(msgs) && msgs[i].Role == core.RoleSystem {
		i++
	}
	system = msgs[:i]
	for _, m := range msgs[i:] {
		if m.Role == core.RoleUser && !isToolResultOnly(m) || len(turns) == 0 {
			turns = append(turns, nil)
		}
		turns[len(turns)-1] = append(turns[len(turns)-1], m)
	}
	return system, turns
}

func isToolResultOnly(m core.Message) bool {
	if len(m.Parts) == 0 {
		return false
	}
	for _, p := range m.Parts {
		if _, ok := p.(core.ToolResult); !ok {
			return false
		}
	}
	return true
}

func joinTurns(system []core.Message, turns [][]core.Message) []core.Message {
	out := append([]core.Message(nil), system...)
	for _, t := range turns {
		out = append(out, t...)
	}
	return out
}

// Window keeps the system messages and the last keepTurns turns, dropping
// further whole turns while the estimate exceeds the budget.
func Window(keepTurns int) Compactor {
	return &window{keep: keepTurns, est: CharEstimator{}}
}

type window struct {
	keep int
	est  Estimator
}

// Compact implements Compactor.
func (w *window) Compact(_ context.Context, msgs []core.Message, budget int) ([]core.Message, error) {
	system, turns := splitTurns(msgs)
	if w.keep > 0 && len(turns) > w.keep {
		turns = turns[len(turns)-w.keep:]
	}
	for budget > 0 && len(turns) > 1 && w.est.Estimate(joinTurns(system, turns)) > budget {
		turns = turns[1:]
	}
	return joinTurns(system, turns), nil
}

// SummarizeOption configures Summarize.
type SummarizeOption func(*summarizer)

// WithSummaryPrompt replaces the instruction sent to the summarizing model.
func WithSummaryPrompt(prompt string) SummarizeOption {
	return func(s *summarizer) { s.prompt = prompt }
}

// WithSummaryEstimator replaces the estimator used to decide how much to drop.
func WithSummaryEstimator(e Estimator) SummarizeOption {
	return func(s *summarizer) { s.est = e }
}

const defaultSummaryPrompt = "Summarize the conversation so far for your own future reference. " +
	"Keep every fact, decision, open question and tool result the assistant may still need. Be dense and neutral."

// Summarize replaces turns older than keepTurns with one model-written summary,
// inserted as a user message after the system prompt. Summaries are memoised
// per dropped prefix so repeated runs in one process do not pay twice.
func Summarize(c core.Chatter, keepTurns int, opts ...SummarizeOption) Compactor {
	s := &summarizer{chat: c, keep: max(keepTurns, 1), prompt: defaultSummaryPrompt, est: CharEstimator{}, memo: map[string]string{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

type summarizer struct {
	chat   core.Chatter
	keep   int
	prompt string
	est    Estimator
	mu     sync.Mutex
	memo   map[string]string
}

// Compact implements Compactor.
func (s *summarizer) Compact(ctx context.Context, msgs []core.Message, budget int) ([]core.Message, error) {
	system, turns := splitTurns(msgs)
	if len(turns) <= s.keep {
		return msgs, nil
	}
	cut := len(turns) - s.keep
	for budget > 0 && cut < len(turns)-1 && s.est.Estimate(joinTurns(system, turns[cut:])) > budget {
		cut++
	}
	dropped := joinTurns(nil, turns[:cut])
	summary, err := s.summary(ctx, dropped)
	if err != nil {
		return nil, err
	}
	note := core.UserText("Summary of the earlier conversation:\n" + summary)
	kept := turns[cut:]
	out := append([]core.Message(nil), system...)
	out = append(out, note)
	return joinTurns(out, kept), nil
}

func (s *summarizer) summary(ctx context.Context, dropped []core.Message) (string, error) {
	key := memoKey(dropped)
	s.mu.Lock()
	if v, ok := s.memo[key]; ok {
		s.mu.Unlock()
		return v, nil
	}
	s.mu.Unlock()
	req := &core.Request{Messages: append(append([]core.Message(nil), dropped...), core.UserText(s.prompt))}
	resp, err := s.chat.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("agentkit: summarize history: %w", err)
	}
	text := strings.TrimSpace(resp.Text())
	s.mu.Lock()
	s.memo[key] = text
	s.mu.Unlock()
	return text, nil
}

func memoKey(msgs []core.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(string(m.Role))
		b.WriteByte(':')
		b.WriteString(m.Text())
		for _, tc := range m.ToolCalls() {
			b.WriteString(tc.ID)
		}
		b.WriteByte('\n')
	}
	return fmt.Sprintf("%d:%s", len(msgs), b.String())
}
