package agentkit

import (
	"sync"

	"github.com/richardwooding/llmkit/core"
)

// Inbox queues user messages for a run that is already in progress. The loop
// drains it at step boundaries: before every model call and, when the model
// stops with plain text, instead of finishing, so a message posted while tools
// run lands after their results. Drained messages are appended to the
// transcript and the session store like any user turn. Safe for concurrent
// use; one Inbox may serve several runs but each message goes to one of them.
type Inbox struct {
	mu   sync.Mutex
	msgs []core.Message
}

// NewInbox returns an empty Inbox.
func NewInbox() *Inbox { return &Inbox{} }

// Post queues a user message built from parts; no parts is a no-op.
func (b *Inbox) Post(parts ...core.Part) {
	if len(parts) == 0 {
		return
	}
	b.PostMessage(core.User(parts...))
}

// PostMessage queues a caller-built message.
func (b *Inbox) PostMessage(m core.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = append(b.msgs, m)
}

// Len reports how many messages are waiting.
func (b *Inbox) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.msgs)
}

func (b *Inbox) drain() []core.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.msgs
	b.msgs = nil
	return out
}

// WithInbox lets messages be posted into the run while it executes.
func WithInbox(b *Inbox) RunOption { return func(c *runConfig) { c.inbox = b } }
