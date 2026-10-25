package applications

import (
	"context"
	"errors"
)

// CancellationRefused is a business refusal of orders to accept a
// Cancellation Request. Reason is its ErrorInfo reason, for example
// CANCELLATION_WINDOW_EXPIRED or ORDER_STATE_CONFLICT.
type CancellationRefused struct {
	Reason string
}

func (e CancellationRefused) Error() string { return "cancellation refused: " + e.Reason }

// CancellationRequester is the cancellation command of orders on behalf of
// the customer in the request context. A business refusal is
// CancellationRefused; a missing or foreign Order is ErrOrderNotFound.
type CancellationRequester interface {
	RequestCancellation(ctx context.Context, request CancellationRequest) (CancellationOutcome, error)
}

type CancellationRequest struct {
	OrderID   string
	RequestID string
	Reason    string
}

// CancellationOutcome is the accepted result of a Cancellation Request.
type CancellationOutcome string

const (
	OutcomeRequested        CancellationOutcome = "REQUESTED"
	OutcomeAlreadyCancelled CancellationOutcome = "ALREADY_CANCELLED"
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
	orders CancellationRequester
}

func NewRequestCancellation(orders CancellationRequester) RequestCancellation {
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
