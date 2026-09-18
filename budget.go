package agentkit

import "time"

// Budget bounds a run. Zero values take the documented defaults.
type Budget struct {
	MaxSteps     int           // model calls; default 10
	MaxTokens    int           // cumulative Usage.TotalTokens; 0 = unlimited
	MaxToolCalls int           // 0 = unlimited
	MaxHandoffs  int           // default 5
	Timeout      time.Duration // 0 = none
}

const (
	defaultMaxSteps    = 10
	defaultMaxHandoffs = 5
)

func (b Budget) maxSteps() int {
	if b.MaxSteps <= 0 {
		return defaultMaxSteps
	}
	return b.MaxSteps
}

func (b Budget) maxHandoffs() int {
	if b.MaxHandoffs <= 0 {
		return defaultMaxHandoffs
	}
	return b.MaxHandoffs
}
