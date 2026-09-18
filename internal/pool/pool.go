// Package pool runs functions over a slice with bounded concurrency,
// preserving input order in the results.
package pool

import (
	"context"
	"sync"
)

// Map applies fn to every item with at most parallel goroutines. Results are
// indexed like items. When ctx is canceled, items not yet started are skipped
// and reported through the returned started slice (false = never ran).
func Map[In, Out any](ctx context.Context, parallel int, items []In, fn func(context.Context, int, In) Out) (results []Out, started []bool) {
	results = make([]Out, len(items))
	started = make([]bool, len(items))
	if len(items) == 0 {
		return results, started
	}
	if parallel < 1 {
		parallel = 1
	}
	parallel = min(parallel, len(items))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range parallel {
		wg.Go(func() {
			for i := range jobs {
				results[i] = fn(ctx, i, items[i])
			}
		})
	}
	for i := range items {
		if ctx.Err() != nil {
			break
		}
		started[i] = true
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results, started
}
