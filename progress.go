package agentkit

import (
	"context"
	"io"
)

// Progress reports interim output from inside a tool call as an
// EventToolProgress carrying the current ToolCall. It never touches the
// transcript and is a no-op outside a streaming run.
func Progress(ctx context.Context, text string) {
	if send := toolSender(ctx); send != nil {
		send(Event{Kind: EventToolProgress, Text: text})
	}
}

// ProgressWriter returns a Writer whose every Write becomes one Progress call,
// for streaming command output; it discards outside a streaming run.
func ProgressWriter(ctx context.Context) io.Writer {
	if toolSender(ctx) == nil {
		return io.Discard
	}
	return progressWriter{ctx: ctx}
}

type progressWriter struct{ ctx context.Context }

// Write implements io.Writer.
func (w progressWriter) Write(p []byte) (int, error) {
	Progress(w.ctx, string(p))
	return len(p), nil
}
