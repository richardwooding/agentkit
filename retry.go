package agentkit

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"time"

	"github.com/richardwooding/llmkit/core"
)

// RetryPolicy governs retries of model calls. Tools are never retried.
type RetryPolicy struct {
	MaxAttempts int           // total attempts; default 3, 1 disables retries
	BaseDelay   time.Duration // default 500ms
	MaxDelay    time.Duration // default 30s
	Jitter      float64       // fraction of the delay, default 0.2
}

// DefaultRetry is the policy agents use unless WithRetry overrides it.
var DefaultRetry = RetryPolicy{MaxAttempts: 3, BaseDelay: 500 * time.Millisecond, MaxDelay: 30 * time.Second, Jitter: 0.2}

func (p RetryPolicy) withDefaults() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = DefaultRetry.MaxAttempts
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = DefaultRetry.BaseDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = DefaultRetry.MaxDelay
	}
	if p.Jitter <= 0 {
		p.Jitter = DefaultRetry.Jitter
	}
	return p
}

// Retryable reports whether err is transient: rate limits, 5xx and transport
// failures. Context errors, other 4xx and ErrContextLength are not.
func (p RetryPolicy) Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, core.ErrContextLength) || errors.Is(err, core.ErrUnsupported) {
		return false
	}
	if errors.Is(err, core.ErrRateLimited) {
		return true
	}
	if apiErr, ok := errors.AsType[*core.APIError](err); ok {
		return apiErr.Status >= 500 || apiErr.Status == 0
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF)
}

// Delay returns how long to wait before the given 1-based retry attempt,
// honoring a Retry-After hint when the error carries one.
func (p RetryPolicy) Delay(attempt int, err error) time.Duration {
	p = p.withDefaults()
	d := min(p.BaseDelay<<uint(max(attempt-1, 0)), p.MaxDelay)
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		d = apiErr.RetryAfter
	}
	jitter := time.Duration(float64(d) * p.Jitter * (rand.Float64()*2 - 1)) //nolint:gosec // jitter, not security
	return max(d+jitter, 0)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
