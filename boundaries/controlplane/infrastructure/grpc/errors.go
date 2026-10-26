package grpcadapter

import (
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/errorkind"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

const errorDomain = "nomnom.controlplane.v1"

// transport is the API answer of one error: gRPC code, ErrorInfo reason and,
// when the error text must not reach the caller, a fixed message.
type transport struct {
	code    codes.Code
	reason  string
	message string
}

// transportByKind is the API error table of the README by application.ErrorKind.
var transportByKind = map[application.ErrorKind]transport{
	application.KindInvalidInput:  {code: codes.InvalidArgument, reason: "INVALID_ARGUMENT"},
	application.KindAlreadyExists: {code: codes.AlreadyExists, reason: "ALREADY_EXISTS"},
	application.KindDomainRule:    {code: codes.FailedPrecondition, reason: "FAILED_PRECONDITION"},
	application.KindUnavailable:   {code: codes.Unavailable, reason: "DEPENDENCY_UNAVAILABLE", message: "dependency unavailable"},
	application.KindPermission:    {code: codes.PermissionDenied, reason: "OPERATION_NOT_ALLOWED"},
	application.KindInternal:      {code: codes.Internal, reason: "INTERNAL_ERROR", message: "internal error"},
}

// transportError maps an error to its API answer: authentication of the
// transport first, then the kind table with the finer reasons of this API.
func transportError(err error) error {
	if errors.Is(err, ErrUnauthenticated) {
		return withInfo(codes.Unauthenticated, "AUTHENTICATION_REQUIRED", err.Error())
	}
	answer, ok := transportByKind[errorkind.Classify(err)]
	if !ok {
		answer = transportByKind[application.KindInternal]
	}
	switch {
	case errors.Is(err, cell.ErrAlreadyRegistered):
		answer.reason = "CELL_ALREADY_REGISTERED"
	case errors.Is(err, market.ErrAlreadyCreated):
		answer.reason = "MARKET_ALREADY_EXISTS"
	case errors.Is(err, market.ErrUnknownCell):
		answer.reason = "CELL_NOT_FOUND"
	}
	message := answer.message
	if message == "" {
		message = err.Error()
	}
	return withInfo(answer.code, answer.reason, message)
}

func withInfo(code codes.Code, reason, message string) error {
	withDetails, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: errorDomain})
	if err != nil {
		return status.Error(code, message)
	}
	return withDetails.Err()
}
