package applications

import (
	"context"
	"errors"
)

// CancellationProblem is a business reason why cancellation cannot be
// requested (OrderCancellationProblemCode).
type CancellationProblem string

const (
	ProblemWindowNotOpen     CancellationProblem = "CANCELLATION_WINDOW_NOT_OPEN"
	ProblemWindowExpired     CancellationProblem = "CANCELLATION_WINDOW_EXPIRED"
	ProblemOrderAlreadyFinal CancellationProblem = "ORDER_ALREADY_FINAL"
)

var problemOfReason = map[string]CancellationProblem{
	"CANCELLATION_WINDOW_NOT_OPEN": ProblemWindowNotOpen,
	"CANCELLATION_WINDOW_EXPIRED":  ProblemWindowExpired,
	"ORDER_STATE_CONFLICT":         ProblemOrderAlreadyFinal,
}

// CancellationResult carries either an Outcome or a Problem.
type CancellationResult struct {
	Outcome CancellationOutcome
	Problem CancellationProblem
}

// RequestCancellation hands the customer's Cancellation Request to orders and
// turns its business refusals into problems; other errors stay errors.
type RequestCancellation struct {
	orders Orders
}

func NewRequestCancellation(orders Orders) RequestCancellation {
	return RequestCancellation{orders: orders}
}

func (h RequestCancellation) Handle(ctx context.Context, request CancellationRequest) (CancellationResult, error) {
	outcome, err := h.orders.RequestCancellation(ctx, request)
	var refused CancellationRefused
	if errors.As(err, &refused) {
		if problem, ok := problemOfReason[refused.Reason]; ok {
			return CancellationResult{Problem: problem}, nil
		}
	}
	if err != nil {
		return CancellationResult{}, err
	}
	return CancellationResult{Outcome: outcome}, nil
}
