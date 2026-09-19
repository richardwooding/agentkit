# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the module uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.2.0] - 2026-09-19

### Added

- `skills` package: load [Agent Skills](https://agentskills.io) from any `fs.FS`, render the
  catalog for the system prompt, and expose the `skill` and `skill_file` tools via `skills.Use`.
- `WithAdditionalInstructions` appends sections to the system prompt independent of option order.
- `Pinned` tools: results are re-sent in the system prompt once compaction drops their turn.

## [0.1.0] - 2026-09-18

### Added

- Typed tools: `Func[In, Out]` derives JSON Schema from Go structs (jsonschema-go), validates
  arguments before decoding; `Raw`, `Toolset`, `Merge`, `Rename`.
- Middleware: `Timeout`, `Approve`, `Recover`, `Serial`, `Observe`.
- `Agent` with a budgeted tool loop (`Run`, `RunMessages`, `Stream`), parallel tool execution,
  retries with `Retry-After`, hooks, and the `slogx` adapter.
- `Run[T]` typed output via JSON-schema response format or a `final_answer` tool.
- Memory: `Store` (`MemoryStore`, `FileStore`), `Window` and `Summarize` compactors with
  proactive and context-length-triggered compaction, `VectorMemory` and the `recall` tool.
- Multi-agent: `AsTool`, `Handoff`, `Map`.
- Nested module `agentkit/mcp` exposing MCP server tools as agent tools.
