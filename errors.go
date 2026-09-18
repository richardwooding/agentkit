package agentkit

import (
	"errors"
	"fmt"
)

// Sentinel errors. Match them with errors.Is; a partial *Result accompanies
// every run error.
var (
	ErrBudgetExceeded = errors.New("agentkit: budget exceeded")
	ErrNoFinalAnswer  = errors.New("agentkit: model finished without final answer")
	ErrToolNotFound   = errors.New("agentkit: tool not found")
	ErrApprovalDenied = errors.New("agentkit: tool call not approved")
	ErrHandoffLoop    = errors.New("agentkit: handoff limit reached")
	ErrDuplicateTool  = errors.New("agentkit: duplicate tool name")
	ErrInvalidArgs    = errors.New("agentkit: tool arguments failed schema validation")
)

// BudgetError reports which Budget limit stopped a run.
type BudgetError struct {
	Limit string
	Used  int
	Max   int
}

// Error formats the exhausted limit.
func (e *BudgetError) Error() string {
	return fmt.Sprintf("agentkit: %s budget exceeded (%d of %d)", e.Limit, e.Used, e.Max)
}

// Unwrap lets errors.Is match ErrBudgetExceeded.
func (e *BudgetError) Unwrap() error { return ErrBudgetExceeded }
