package agentkit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/richardwooding/llmkit"
	"github.com/richardwooding/llmkit/core"
)

// Agent is an immutable configuration: a model, instructions, tools and
// limits. One Agent serves any number of concurrent runs.
type Agent struct {
	name         string
	instructions string
	extra        []string
	chat         core.Chatter
	stream       core.Streamer
	tools        Toolset
	mws          []Middleware
	budget       Budget
	retry        RetryPolicy
	parallel     int
	hooks        Hooks
	store        Store
	compactor    Compactor
	estimator    Estimator
	window       int
	retriever    Retriever
	retrieveK    int
	template     core.Request

	registry   *llmkit.Registry
	clientOpts []core.Option
}

// Option configures an Agent.
type Option func(*Agent) error

// New resolves model through llmkit and builds an Agent.
func New(model string, opts ...Option) (*Agent, error) {
	a := &Agent{name: model}
	if err := a.apply(opts); err != nil {
		return nil, err
	}
	reg := a.registry
	if reg == nil {
		reg = llmkit.Default
	}
	c, err := reg.New(model, a.clientOpts...)
	if err != nil {
		return nil, err
	}
	chat, ok := c.(core.Chatter)
	if !ok {
		return nil, fmt.Errorf("agentkit: %s/%s cannot chat: %w", c.Provider(), c.Model(), core.ErrUnsupported)
	}
	a.chat = chat
	a.stream, _ = c.(core.Streamer)
	return a.finish()
}

// NewFromClient builds an Agent around an existing client (or a fake).
func NewFromClient(c core.Chatter, opts ...Option) (*Agent, error) {
	if c == nil {
		return nil, errors.New("agentkit: nil client")
	}
	a := &Agent{name: "agent", chat: c}
	a.stream, _ = c.(core.Streamer)
	if err := a.apply(opts); err != nil {
		return nil, err
	}
	return a.finish()
}

func (a *Agent) apply(opts []Option) error {
	for _, o := range opts {
		if o == nil {
			continue
		}
		if err := o(a); err != nil {
			return err
		}
	}
	return nil
}

func (a *Agent) finish() (*Agent, error) {
	a.instructions = joinSections(a.instructions, a.extra)
	if a.retriever != nil {
		a.tools = append(a.tools, Recall(a.retriever, a.retrieveK))
	}
	merged, err := Merge(a.tools)
	if err != nil {
		return nil, err
	}
	for i, t := range merged {
		merged[i] = Wrap(t, a.mws...)
	}
	a.tools = merged
	if a.parallel < 1 {
		a.parallel = 1
	}
	if a.estimator == nil {
		a.estimator = CharEstimator{}
	}
	return a, nil
}

// Name returns the agent's name (the model name unless WithName was used).
func (a *Agent) Name() string { return a.name }

// Tools returns the agent's tools after middleware has been applied.
func (a *Agent) Tools() Toolset { return append(Toolset(nil), a.tools...) }

// WithName sets the agent's name, used in events, hooks and handoffs.
func WithName(name string) Option { return func(a *Agent) error { a.name = name; return nil } }

// WithInstructions sets the system prompt.
func WithInstructions(s string) Option {
	return func(a *Agent) error { a.instructions = s; return nil }
}

// WithAdditionalInstructions appends sections to the system prompt after the
// text set by WithInstructions, whatever the option order. Empty sections are
// dropped; sections are separated by a blank line.
func WithAdditionalInstructions(sections ...string) Option {
	return func(a *Agent) error { a.extra = append(a.extra, sections...); return nil }
}

// WithTools adds tools; names must be unique across the agent.
func WithTools(tools ...Tool) Option {
	return func(a *Agent) error { a.tools = append(a.tools, tools...); return nil }
}

// WithMiddleware wraps every tool of the agent, including Recall and sub-agents.
func WithMiddleware(mws ...Middleware) Option {
	return func(a *Agent) error { a.mws = append(a.mws, mws...); return nil }
}

// WithBudget bounds each run.
func WithBudget(b Budget) Option { return func(a *Agent) error { a.budget = b; return nil } }

// WithRetry sets the model-call retry policy.
func WithRetry(p RetryPolicy) Option { return func(a *Agent) error { a.retry = p; return nil } }

// WithParallel allows up to n tool calls of one step to run concurrently.
func WithParallel(n int) Option { return func(a *Agent) error { a.parallel = n; return nil } }

// WithHooks adds observers; multiple calls are joined.
func WithHooks(h Hooks) Option { return func(a *Agent) error { a.hooks = a.hooks.Join(h); return nil } }

// WithStore persists conversations for runs that pass WithSession.
func WithStore(s Store) Option { return func(a *Agent) error { a.store = s; return nil } }

// WithCompactor shapes the history sent to the model when it grows too large.
func WithCompactor(c Compactor) Option { return func(a *Agent) error { a.compactor = c; return nil } }

// WithEstimator replaces the token estimator used for proactive compaction.
func WithEstimator(e Estimator) Option { return func(a *Agent) error { a.estimator = e; return nil } }

// WithContextWindow enables proactive compaction when the estimated prompt
// exceeds 90% of tokens.
func WithContextWindow(tokens int) Option {
	return func(a *Agent) error { a.window = tokens; return nil }
}

// WithRetriever adds a "recall" tool returning the k best matches.
func WithRetriever(r Retriever, k int) Option {
	return func(a *Agent) error { a.retriever, a.retrieveK = r, k; return nil }
}

// WithToolChoice sets the request tool choice.
func WithToolChoice(tc core.ToolChoice) Option {
	return func(a *Agent) error { a.template.ToolChoice = tc; return nil }
}

// WithMaxTokens caps output tokens per model call.
func WithMaxTokens(n int) Option {
	return func(a *Agent) error { a.template.MaxTokens = n; return nil }
}

// WithTemperature sets the sampling temperature.
func WithTemperature(t float64) Option {
	return func(a *Agent) error { a.template.Temperature = new(t); return nil }
}

// WithReasoning enables extended thinking where supported.
func WithReasoning(r core.ReasoningConfig) Option {
	return func(a *Agent) error { a.template.Reasoning = &r; return nil }
}

// WithRequestExtra merges raw fields into every request body.
func WithRequestExtra(extra map[string]any) Option {
	return func(a *Agent) error { a.template.Extra = extra; return nil }
}

// WithProviderOptions merges provider-keyed raw fields into every request.
func WithProviderOptions(po map[string]map[string]any) Option {
	return func(a *Agent) error { a.template.ProviderOptions = po; return nil }
}

// WithRegistry resolves the model against a specific llmkit registry (New only).
func WithRegistry(r *llmkit.Registry) Option {
	return func(a *Agent) error { a.registry = r; return nil }
}

// WithClientOptions passes options to the llmkit client (New only).
func WithClientOptions(opts ...core.Option) Option {
	return func(a *Agent) error { a.clientOpts = append(a.clientOpts, opts...); return nil }
}

func joinSections(base string, extra []string) string {
	parts := make([]string, 0, len(extra)+1)
	if base != "" {
		parts = append(parts, base)
	}
	for _, e := range extra {
		if e != "" {
			parts = append(parts, e)
		}
	}
	return strings.Join(parts, "\n\n")
}
