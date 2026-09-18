# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`agentkit` is an agent framework built on `github.com/richardwooding/llmkit`
(`module github.com/richardwooding/agentkit`, Go 1.27, pure Go, no cgo). The root module
depends on the standard library, llmkit and `github.com/google/jsonschema-go` only. MCP
support is a **nested module** in `mcp/` with its own `go.mod` so the MCP SDK stays out of
the root module graph.

## Commands

```sh
go build ./...
go test -race ./...                              # httptest / scripted fakes only, no network
go test -race -run TestRunToolLoop .             # single test
go run ./examples/tools                          # examples run offline against a fake provider

gofumpt -w . && goimports -local github.com/richardwooding/agentkit -w .
golangci-lint run ./...                          # strict v2 config, the bar is 0 issues

(cd mcp && go test -race ./... && golangci-lint run --config ../.golangci.yml ./...)
```

Tools may not be on PATH; `go install mvdan.cc/gofumpt@latest golang.org/x/tools/cmd/goimports@latest`
and add `$(go env GOPATH)/bin`. Use `GOWORK=off go mod tidy` at the root (the workspace
`go.work` exists for local development of `mcp/`).

## Architecture

```
tool.go        Tool interface, Output, Call/CallFrom, Raw, Toolset, Rename, Merge
schema.go      Func/NewFunc (jsonschema-go), FuncOption, SchemaFor, argument validation
middleware.go  Middleware, Wrap, Timeout, Approve, Recover, Serial, Observe
agent.go       Agent + options; New (via llmkit) / NewFromClient (fakes)
run.go         Run/RunMessages/Stream/StreamMessages, Result, StopReason, RunOption
loop.go        run struct: prepare → step (model call → tools) → finish
event.go       Event/EventKind for Stream
typed.go       Run[T]: OutputSchema vs OutputTool (final_answer), nudge, fence stripping
budget.go retry.go hooks.go state.go
memory.go      Store, MemoryStore, FileStore (atomic JSON per session)
compact.go     Estimator, Compactor, Window, Summarize, Chain, turn splitting
retrieve.go    Retriever, VectorMemory, Recall tool
multi.go       AsTool, Handoff, Map
internal/pool  bounded ordered worker pool; internal/atomicfile
slogx/         Hooks adapter for log/slog
mcp/           nested module: Connect, Server.Tools → agentkit.Toolset
examples/      tools, typed, multiagent (+ internal/fake provider)
docs/          gloam Pages site
```

### Things that are non-obvious and easy to break

- **Tool call/result pairs must never be orphaned.** Every assistant message with tool calls
  is followed by a `RoleTool` message with one result per call: budget stops append
  "not executed" error results, cancelled parallel calls get error results, `final_answer`
  gets a synthetic "recorded" result, and `Window`/`Summarize` cut on turn boundaries only
  (a turn = user message through the next user message; `splitTurns` treats tool-result-only
  messages as part of the current turn). Anthropic rejects unmatched results and Gemini
  addresses results by name, so `ToolResult.Name` is always set.
- **The `run` struct owns all mutable state; `Agent` is immutable.** After a handoff `r.agent`
  is the target (tools, model, template, parallel, retry) but budget, store, compactor,
  estimator, window and hooks stay with `r.origin`.
- **Usage accounting across sub-agents** goes through `usageSink` in the context
  (`state.go`). A child captures the parent's sink in `prepare` before installing its own,
  then adds its total in `finish`. `Result.Usage` = own model usage + children.
- **Run uses Chat; Stream uses Stream when available.** A stream that has emitted text is
  never retried (`oneModelCall` reports `emitted`). Setup/HTTP errors arrive before any chunk
  and are retried like Chat errors.
- **Retry classification** lives in `RetryPolicy.Retryable`: `ErrRateLimited`, 5xx,
  status 0 (transport), net timeouts. Never 4xx, `ErrContextLength` (handled by compaction,
  once per step) or `ErrUnsupported` (handled by the typed-output fallback).
- **Typed output mode**: tool-less agents use `Format json_schema`; agents with tools get the
  `final_answer` tool because Chat Completions and Gemini reject response formats alongside
  tools. `ErrUnsupported` in schema mode flips to tool mode before any retry.
- **jsonschema-go**: `omitempty`/`omitzero` fields are optional, everything else required;
  structs get `additionalProperties:false` (so OpenAI `strict` only works with all-required
  structs → `WithStrict` is opt-in); enums only via `TypeSchemas` (`WithEnum`); maps with
  non-string keys, funcs and channels fail (`Func` panics, `NewFunc` errors). Validation
  decodes args to `any`, validates, then decodes to `In`.
- **Hooks fire from pool goroutines** when `WithParallel(n > 1)`; user hooks must be safe.
- **Package name clash**: `agentkit/mcp` vs the SDK's `mcp`; the SDK is imported as `sdk`
  inside the package and users alias ours (`agentmcp`).
- **Nested-module tagging**: tag root `vX.Y.Z` first, bump the require in `mcp/go.mod`, then
  tag `mcp/vX.Y.Z`. Never commit a `replace`; `go.work` covers local development.

## Conventions

- Tests are black-box (`package agentkit_test`), table-driven, using the `scripted` Chatter
  and `scriptedStream` fakes in `fakes_test.go` (they record every request in `seen`).
  No golden files, no network. MCP tests use `sdk.NewInMemoryTransports()`.
- No comments explaining *what*; short *why* comments for non-obvious behaviour. Exported
  identifiers and methods of unexported types carry one-line doc comments (revive).
- Repeated strings become constants (goconst 2/3); keep loop methods under gocyclo 15 /
  gocognit 20 by splitting per phase. US spelling in comments and strings (misspell).
- Commit prefixes: `feat:`, `fix:`, `docs:`, `ci:`, `test:`, `chore:`.

## Releasing

`v0.x` — breaking changes allowed but recorded under `[Unreleased]` in `CHANGELOG.md`.
Tag-push only; see the nested-module tagging order above.
