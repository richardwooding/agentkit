# agentkit

[![Go Reference](https://pkg.go.dev/badge/github.com/richardwooding/agentkit.svg)](https://pkg.go.dev/github.com/richardwooding/agentkit)
[![CI](https://github.com/richardwooding/agentkit/actions/workflows/ci.yml/badge.svg)](https://github.com/richardwooding/agentkit/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Website:** https://richardwooding.github.io/agentkit/

An agent framework for Go on top of [llmkit](https://github.com/richardwooding/llmkit):
tools typed as Go structs, a budgeted tool-calling loop that streams events, conversation
memory, and multi-agent composition. Any model llmkit can reach, by name.

```go
type weatherArgs struct {
	City string `json:"city" jsonschema:"city name"`
}

weather := agentkit.Func("weather", "Current weather for a city",
	func(ctx context.Context, in weatherArgs) (string, error) {
		return lookup(in.City), nil
	})

agent, err := agentkit.New("claude-sonnet-4-5",
	agentkit.WithInstructions("You are a concise assistant."),
	agentkit.WithTools(weather),
	agentkit.WithBudget(agentkit.Budget{MaxSteps: 8, Timeout: time.Minute}),
)
res, err := agent.Run(ctx, "What's the weather in Cape Town?")
fmt.Println(res.Output, res.Usage.TotalTokens)
```

Pure Go 1.27, no cgo. The root module depends on llmkit and
`github.com/google/jsonschema-go` only. MCP support lives in a nested module so the
MCP SDK stays opt-in.

## Why

- **Tools are Go functions.** `Func` turns `func(ctx, In) (Out, error)` into a tool: the
  schema comes from `In` (`json` and `jsonschema:"description"` tags, `omitempty` means
  optional), arguments are validated against it before your code runs, and `Out` is
  marshalled for the model.
- **The loop is bounded and observable.** Steps, tokens, tool calls, handoffs and wall time
  are budgets; every model call, tool call, retry and compaction fires a hook or a stream
  event. Errors always come with the partial `Result`.
- **Capabilities are honest.** Tool call/result pairs are never orphaned, even when a budget
  or cancellation stops a step half way, so transcripts stay valid for every provider.
- **Composition over framework.** Agents are immutable and concurrency-safe. Sub-agents are
  tools, handoffs swap the active agent mid-transcript, `Map` fans work out.

## Install

```sh
go get github.com/richardwooding/agentkit
go get github.com/richardwooding/agentkit/mcp   # optional, MCP servers as tools
```

## Usage

### Streaming

```go
for e, err := range agent.Stream(ctx, "Plan my week") {
	if err != nil {
		return err // e.Result holds the partial transcript
	}
	switch e.Kind {
	case agentkit.EventText:
		fmt.Print(e.Text)
	case agentkit.EventToolCall:
		fmt.Printf("\n→ %s %s\n", e.ToolCall.Name, e.ToolCall.Arguments)
	case agentkit.EventFinish:
		fmt.Printf("\n%s in %d steps\n", e.Result.StopReason, e.Result.Steps)
	}
}
```

The model is streamed when the provider supports it; otherwise one text event carries the
whole reply. Breaking out of the loop cancels the run. Events always arrive on the goroutine
that ranges over the stream, even with `WithParallel`. `EventUsage` follows every model call
with that call's own token usage (a context meter needs `Usage.InputTokens`); tools can
report interim output with `Progress(ctx, text)` or `ProgressWriter(ctx)`, which surface as
`EventToolProgress` without touching the transcript.

### Typed output

```go
type Forecast struct {
	City  string `json:"city"`
	TempC int    `json:"temp_c"`
}
forecast, res, err := agentkit.Run[Forecast](ctx, agent, "Forecast for Cape Town?")
```

Agents without tools get a JSON-schema response format. Agents with tools get a synthetic
`final_answer` tool instead, because most providers reject a response format alongside
tool definitions. If the model stops without answering it is nudged once, then
`ErrNoFinalAnswer` is returned.

### Middleware and approval

```go
agent, _ := agentkit.New("gpt-5",
	agentkit.WithTools(deleteFile, sendMail),
	agentkit.WithMiddleware(
		agentkit.Recover(),
		agentkit.Timeout(30*time.Second),
		agentkit.ApproveWith(agentkit.ApproverFunc(func(ctx context.Context, c agentkit.Call) (agentkit.Decision, error) {
			switch c.Call.Name {
			case "delete_file":
				return askUser(ctx, c) // Allow(), AllowWith(rewrittenArgs) or Deny(reason)
			default:
				return agentkit.Allow(), nil
			}
		})),
	),
	agentkit.WithParallel(4),
)
```

`ApproveWith` may block for as long as a person takes to answer: in a streaming run an
`EventApprovalRequest` precedes the `Approver` and an `EventApprovalResult` carries its
`Decision`, so a UI can show the pending call and answer it asynchronously. A denial is fed
back to the model as an error result wrapping `ErrApprovalDenied` and the run continues;
canceling ctx while approval is pending stops the run as `StopCancelled`. Rewritten
arguments reach the tool but the transcript keeps what the model wrote. `Approve(fn)` is the
error-only shorthand. Wrap a tool in `Serial()` to keep it out of parallel batches.

### Steering a running agent

```go
inbox := agentkit.NewInbox()
go func() { inbox.Post(core.Text("also run the tests")) }()
res, err := agent.Run(ctx, "fix the bug", agentkit.WithInbox(inbox))
```

Messages posted while the run executes are appended at the next step boundary (after the
current tool results, before the next model call); if the model has already answered, the
run continues with them instead of finishing. Cancel ctx to interrupt instead.

### Switching models on a session

```go
fast, err := agent.With(agentkit.WithName("fast")) // same options, replayed; middleware wraps once
client := agent.Client()                            // the underlying llmkit Chatter
```

### Memory

```go
store := agentkit.NewFileStore("./sessions")
agent, _ := agentkit.New("gemini-2.5-pro",
	agentkit.WithStore(store),
	agentkit.WithCompactor(agentkit.Summarize(summariser, 6)),
	agentkit.WithContextWindow(120_000),
	agentkit.WithRetriever(memory, 4), // adds a "recall" tool
)
res, _ := agent.Run(ctx, "Where did we leave off?", agentkit.WithSession("richard"))
```

Stores are lossless. `FileStore` is append-only JSON Lines (one `{"t":…,"m":…}` per message,
fsync'd, torn tails tolerated; older single-array files are migrated on first write), and
both stores implement `Lister` (`List` → `SessionInfo{ID, Created, Updated, Messages,
Title}`) for a session picker. Compaction only shapes what the model sees, whole turns at a
time, and runs proactively near the window or reactively when a provider reports the
context is too long; `Summarize(c, 0)` replays nothing but the summary and
`StripReasoning()` drops reasoning blocks before replaying a transcript to another model.
A tool that implements `Pinned` keeps its results visible: once compaction drops the turn
holding one, the content is re-sent in the system prompt of the outgoing request, fenced as
data. `VectorMemory` builds on any llmkit `Embedder` (and optionally a `Reranker`).

### Multi-agent

```go
researcher, _ := agentkit.New("gpt-5", agentkit.WithName("researcher"), ...)
billing, _ := agentkit.New("claude-sonnet-4-5", agentkit.WithName("billing"), ...)

front, _ := agentkit.New("gpt-5-mini",
	agentkit.WithTools(
		agentkit.AsTool(researcher, "research", "Research a topic in depth"),
		agentkit.Handoff(billing),
	),
)
```

`AsTool` runs the child with its own budget and returns only its final text; its usage is
added to the parent. With `agentkit.WithForwardEvents()` the child's events appear in the
parent's stream too, carrying the child's `RunID`, `Depth` and `Parent` (the enclosing run
ID). `Handoff` switches instructions, tools and model while carrying the transcript. `Map`
runs any function over inputs with bounded concurrency.

### MCP servers as tools

```go
import agentmcp "github.com/richardwooding/agentkit/mcp"

fs, err := agentmcp.Connect(ctx, agentmcp.Command("npx", "-y", "@modelcontextprotocol/server-filesystem", "."),
	agentmcp.WithPrefix("fs_"))
defer fs.Close()
tools, err := fs.Tools(ctx)
agent, _ := agentkit.New("gpt-5", agentkit.WithTools(tools...))
```

Each tool implements `agentmcp.Annotated`, exposing the server's `readOnlyHint`,
`destructiveHint` and friends for a consent UI. They are the server's own claims; never use
them to skip approval.

### Skills

```go
import "github.com/richardwooding/agentkit/skills"

set, err := skills.LoadAll(os.DirFS(".agents/skills")) // any fs.FS: os.DirFS, embed.FS, ...
agent, _ := agentkit.New("claude-sonnet-4-5",
	agentkit.WithInstructions("You are a helpful assistant."),
	skills.Use(set), // catalog in the system prompt + "skill" and "skill_file" tools
)
```

[Agent Skills](https://agentskills.io) are folders with a `SKILL.md`. `Use` lists their names
and descriptions in the system prompt; the model activates one by calling `skill` and gets the
full body (pinned, so it survives compaction), then reads bundled files with `skill_file`.
`Merge` gives project skills precedence over user skills; `Set.Problems` reports what was
skipped or loaded with reservations. Running a skill's scripts is left to your own tools.

### Observability

```go
agentkit.WithHooks(slogx.Hooks(slog.Default(), slogx.WithLevel(slog.LevelDebug)))
```

`Hooks` is a struct of optional functions; `slogx` is one implementation. Tool hooks may
fire from worker goroutines.

### Errors

`ErrBudgetExceeded` (with `*BudgetError` naming the limit), `ErrNoFinalAnswer`,
`ErrToolNotFound`, `ErrApprovalDenied`, `ErrHandoffLoop`, `ErrDuplicateTool`,
`ErrInvalidArgs`. Provider errors come through unchanged from llmkit; rate limits and
5xx responses are retried with jitter, honouring `Retry-After`.

## Examples

`examples/tools`, `examples/typed` and `examples/multiagent` run offline against an
in-process fake provider: `go run ./examples/tools`.

## What this is not

- Not a prompt library or a planner. Instructions are a string; planning is the model's job
  or yours.
- Not a workflow engine. Runs are single processes with a `context.Context`; persist the
  `Result` yourself if you need durability across restarts.
- Not tied to one vendor. Everything that talks to a model goes through llmkit's
  `Chatter`/`Streamer` interfaces, so fakes drop in for tests.

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## License

MIT © 2026 Richard Wooding
