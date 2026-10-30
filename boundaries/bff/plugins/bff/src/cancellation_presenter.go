package main

import (
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/ordertracking/applications"
)

var (
	cancellationOutcomes = map[applications.CancellationOutcome]service.OrderCancellationOutcome{
		applications.OutcomeRequested:        service.OrderCancellationOutcome_ORDER_CANCELLATION_OUTCOME_REQUESTED,
		applications.OutcomeAlreadyCancelled: service.OrderCancellationOutcome_ORDER_CANCELLATION_OUTCOME_ALREADY_CANCELLED,
	}
	cancellationProblems = map[applications.CancellationProblem]service.OrderCancellationProblemCode{
		applications.ProblemWindowNotOpen:     service.OrderCancellationProblemCode_ORDER_CANCELLATION_PROBLEM_CODE_CANCELLATION_WINDOW_NOT_OPEN,
		applications.ProblemWindowExpired:     service.OrderCancellationProblemCode_ORDER_CANCELLATION_PROBLEM_CODE_CANCELLATION_WINDOW_EXPIRED,
		applications.ProblemOrderAlreadyFinal: service.OrderCancellationProblemCode_ORDER_CANCELLATION_PROBLEM_CODE_ORDER_ALREADY_FINAL,
	}
)

// cancellationPayload carries either the problem or the outcome.
func cancellationPayload(result applications.CancellationResult) (*service.RequestOrderCancellationPayload, error) {
	payload := &service.RequestOrderCancellationPayload{}
	if result.Problem != "" {
		code, err := enumValue(cancellationProblems, "cancellation problem", result.Problem)
		if err != nil {
			return nil, err
		}
		payload.Problem = &service.OrderCancellationProblem{Code: code}
		return payload, nil
	}
	outcome, err := enumValue(cancellationOutcomes, "cancellation outcome", result.Outcome)
	if err != nil {
		return nil, err
	}
	payload.Outcome = outcome
	return payload, nil
}
