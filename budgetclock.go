package agentkit

import (
	"context"
	"sync"
	"time"
)

// blockedClock accumulates the time a run spends waiting on a human.
//
// A run's Timeout is meant to bound the *agent's* work — a loop that will not
// converge, a tool that hangs — but it was enforced as a plain context
// deadline set when the run began, so it also bounded the person answering an
// approval prompt. Step away from a prompt and the budget burns; a wright user
// lost two of three runs in one session to a deadline reached entirely while
// the agent sat idle waiting to be told whether it could proceed.
//
// The clock is shared down a run tree: a sub-agent inherits its parent's, so a
// human pondering a child's prompt pauses every budget above it too. Anything
// else would let a parent expire while its child waited on the same person.
type blockedClock struct {
	mu      sync.Mutex
	total   time.Duration
	waiting int       // how many prompts are open right now
	since   time.Time // when the first of them opened
}

// enter records that a wait has begun, and leave that it has ended.
//
// They count the *union* of the waits, not their sum: tools run in parallel,
// so several prompts can be open at once, and one person answering three
// prompts over a minute has cost the run a minute, not three. A nil clock
// ignores both, so a caller outside a run needs no special case.
func (b *blockedClock) enter() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiting == 0 {
		b.since = time.Now()
	}
	b.waiting++
}

func (b *blockedClock) leave() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.waiting--
	if b.waiting == 0 {
		b.total += time.Since(b.since)
	}
}

// elapsed includes a wait that is still open, which is the whole point: the
// budget has to be paused *while* someone is deciding, not credited back
// afterwards. Accounting only on leave let the watchdog fire mid-prompt and
// kill the very run the person was about to allow.
func (b *blockedClock) elapsed() time.Duration {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiting > 0 {
		return b.total + time.Since(b.since)
	}
	return b.total
}

// blockedFrom returns the clock of the run executing under ctx, or nil.
func blockedFrom(ctx context.Context) *blockedClock {
	if s, ok := ctx.Value(runStateKey{}).(runState); ok {
		return s.blocked
	}
	return nil
}

// watchBudget cancels the run once it has spent Timeout *working*, which is
// wall-clock minus whatever a human spent deciding.
//
// It replaces context.WithTimeout rather than supplementing it: a context
// deadline is fixed at creation and cannot be extended, so there is no way to
// give the run back the time it spent waiting. Canceling with an explicit
// cause keeps the run's stop reason "deadline" — stopReasonFor reads
// context.Cause, so a budget stop is still distinguishable from a user's
// interrupt, which matters because one is a failure to report and the other is
// not.
//
// It wakes at most a few times: each timer fire recomputes the remaining
// budget from the clock, which may have grown while a prompt was open.
func (r *run) watchBudget(ctx context.Context, cancel context.CancelCauseFunc, timeout time.Duration) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if remaining := timeout - r.worked(); remaining > 0 {
				t.Reset(remaining)
				continue
			}
			cancel(context.DeadlineExceeded)
			return
		}
	}
}

// worked is how long the run has been the one doing the work.
func (r *run) worked() time.Duration {
	return time.Since(r.start) - r.blocked.elapsed()
}
