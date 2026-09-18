// Package slogx adapts agentkit.Hooks to log/slog.
package slogx

import (
	"context"
	"log/slog"

	"github.com/richardwooding/agentkit"
)

// Option configures Hooks.
type Option func(*config)

type config struct {
	level slog.Level
	args  bool
}

// WithLevel sets the level for routine events (run, step, tool). Retries and
// tool errors log one level higher; model request sizes log at Debug.
func WithLevel(l slog.Level) Option { return func(c *config) { c.level = l } }

// WithArguments includes tool arguments and results in log records. Off by
// default because they may carry personal data.
func WithArguments() Option { return func(c *config) { c.args = true } }

const (
	keyRun  = "run"
	keyStep = "step"
	keyTool = "tool"
	keyCall = "call"
)

// Hooks returns agentkit.Hooks that log to l.
func Hooks(l *slog.Logger, opts ...Option) agentkit.Hooks {
	cfg := config{level: slog.LevelInfo}
	for _, o := range opts {
		o(&cfg)
	}
	if l == nil {
		l = slog.Default()
	}
	warn := cfg.level + 4
	log := func(level slog.Level, msg string, attrs ...any) {
		l.Log(context.Background(), level, msg, attrs...)
	}
	return agentkit.Hooks{
		OnRunStart: func(i agentkit.RunInfo) {
			log(cfg.level, "agent run start", keyRun, i.RunID, "agent", i.Agent, "session", i.Session, "depth", i.Depth)
		},
		OnStep: func(i agentkit.StepInfo) {
			log(cfg.level, "agent step", keyRun, i.RunID, "agent", i.Agent, keyStep, i.Step)
		},
		OnModelCall: func(i agentkit.ModelCallInfo) {
			log(slog.LevelDebug, "model call", keyRun, i.RunID, keyStep, i.Step, "attempt", i.Attempt, "messages", len(i.Request.Messages), "tools", len(i.Request.Tools))
		},
		OnModelResponse: func(i agentkit.ModelResponseInfo) {
			if i.Err != nil {
				log(warn, "model error", keyRun, i.RunID, keyStep, i.Step, "duration", i.Duration, "err", i.Err)
				return
			}
			log(slog.LevelDebug, "model response", keyRun, i.RunID, keyStep, i.Step, "duration", i.Duration,
				"finish", string(i.Response.FinishReason), "tokens", i.Response.Usage.TotalTokens)
		},
		OnToolCall: func(i agentkit.ToolCallInfo) {
			attrs := []any{keyRun, i.Call.RunID, "agent", i.Call.Agent, keyTool, i.Call.Call.Name, keyCall, i.Call.Call.ID}
			if cfg.args {
				attrs = append(attrs, "args", string(i.Call.Call.Arguments))
			}
			log(cfg.level, "tool call", attrs...)
		},
		OnToolResult: func(i agentkit.ToolResultInfo) {
			attrs := []any{keyRun, i.Call.RunID, keyTool, i.Call.Call.Name, keyCall, i.Call.Call.ID, "duration", i.Duration}
			if cfg.args {
				attrs = append(attrs, "result", i.Output.Text())
			}
			if i.Err != nil {
				log(warn, "tool error", append(attrs, "err", i.Err)...)
				return
			}
			log(cfg.level, "tool result", attrs...)
		},
		OnRetry: func(i agentkit.RetryInfo) {
			log(warn, "model retry", keyRun, i.RunID, keyStep, i.Step, "attempt", i.Attempt, "delay", i.Delay, "err", i.Err)
		},
		OnCompact: func(i agentkit.CompactInfo) {
			log(cfg.level, "history compacted", keyRun, i.RunID, "reason", i.Reason, "before", i.Before, "after", i.After)
		},
		OnHandoff: func(i agentkit.HandoffInfo) {
			log(cfg.level, "handoff", keyRun, i.RunID, "from", i.From, "to", i.To)
		},
		OnRunEnd: func(r *agentkit.Result, err error) {
			attrs := []any{keyRun, r.RunID, "agent", r.Agent, "stop", string(r.StopReason), "steps", r.Steps, "tool_calls", r.ToolCalls, "tokens", r.Usage.TotalTokens, "duration", r.Duration}
			if err != nil {
				log(warn, "agent run failed", append(attrs, "err", err)...)
				return
			}
			log(cfg.level, "agent run end", attrs...)
		},
	}
}
