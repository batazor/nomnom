package grpcadapter

import (
	"errors"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

const errorDomain = "nomnom.orders.v1"

// transport is the API answer of one error: gRPC code, ErrorInfo reason and,
// when the error text must not reach the caller, a fixed message.
type transport struct {
	code    codes.Code
	reason  string
	message string
}

// transportByKind is the API error table of the README by application.ErrorKind.
// It covers every kind; errorkind.Classify decides the kind.
var transportByKind = map[application.ErrorKind]transport{
	application.KindInvalidInput:  {code: codes.InvalidArgument, reason: "INVALID_ARGUMENT"},
	application.KindNotFound:      {code: codes.NotFound, reason: "ORDER_NOT_FOUND"},
	application.KindAlreadyExists: {code: codes.AlreadyExists, reason: "ORDER_ALREADY_EXISTS"},
	application.KindConflict:      {code: codes.Aborted, reason: "CONCURRENCY_CONFLICT"},
	application.KindDomainRule:    {code: codes.FailedPrecondition, reason: "ORDER_STATE_CONFLICT"},
	application.KindUnavailable:   {code: codes.Unavailable, reason: "DEPENDENCY_UNAVAILABLE", message: "dependency unavailable"},
	application.KindPermission:    {code: codes.PermissionDenied, reason: "OPERATION_NOT_ALLOWED"},
	application.KindInternal:      {code: codes.Internal, reason: "INTERNAL_ERROR", message: "internal error"},
}

// transportError maps an error to its API answer: authentication of the
// transport first, then the kind table and the details of this API — finer
// reasons, the INVALID_ORDER code of an invalid composition and the metadata
// of ORDER_NOT_ORDERABLE.
func transportError(err error) error {
	if errors.Is(err, ErrUnauthenticated) {
		return withInfo(codes.Unauthenticated, "AUTHENTICATION_REQUIRED", err.Error(), nil)
	}
	answer, ok := transportByKind[errorkind.Classify(err)]
	if !ok {
		answer = transportByKind[application.KindInternal]
	}
	var metadata map[string]string
	var notOrderable order.NotOrderable
	var notCancellable order.CancellationNotAllowed
	switch {
	case errors.Is(err, placeorder.ErrUnknownRestaurant):
		answer.reason = "RESTAURANT_NOT_FOUND"
	case errors.Is(err, placeorder.ErrRestaurantNotAcceptingOrders):
		answer.reason = "RESTAURANT_NOT_ACCEPTING_ORDERS"
	case errors.Is(err, placeorder.ErrPremiumUnavailable):
		answer.reason = "PREMIUM_UNAVAILABLE"
	case errors.Is(err, application.ErrInvalidInput):
	case errors.As(err, &notOrderable):
		answer.reason = "ORDER_NOT_ORDERABLE"
		metadata = notOrderableMetadata(notOrderable)
	case errors.Is(err, vo.ErrInvalidValue), errors.Is(err, order.ErrOrderRequiresItems), errors.Is(err, order.ErrDuplicateMenuItem):
		answer.code, answer.reason = codes.InvalidArgument, "INVALID_ORDER"
	case errors.As(err, &notCancellable):
		answer.reason = cancellationReason(notCancellable)
	}
	message := answer.message
	if message == "" {
		message = err.Error()
	}
	return withInfo(answer.code, answer.reason, message, metadata)
}

func withInfo(code codes.Code, reason, message string, metadata map[string]string) error {
	withDetails, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{
		Reason:   reason,
		Domain:   errorDomain,
		Metadata: metadata,
	})
	if err != nil {
		return status.Error(code, message)
	}
	return withDetails.Err()
}

// notOrderableMetadata names the reasons of an unsatisfied OrderItemsOrderable
// policy (FR-498): `codes` lists each reason code once in upper case,
// `menu_item_ids` the Menu Items the reasons refer to, both comma-separated.
func notOrderableMetadata(err order.NotOrderable) map[string]string {
	var reasonCodes, menuItemIDs []string
	seen := make(map[string]struct{}, len(err.Reasons))
	for _, reason := range err.Reasons {
		reasonCode := strings.ToUpper(string(reason.Code))
		if _, duplicate := seen[reasonCode]; !duplicate {
			seen[reasonCode] = struct{}{}
			reasonCodes = append(reasonCodes, reasonCode)
		}
		if reason.MenuItemID != "" {
			menuItemIDs = append(menuItemIDs, string(reason.MenuItemID))
		}
	}
	return map[string]string{
		"codes":         strings.Join(reasonCodes, ","),
		"menu_item_ids": strings.Join(menuItemIDs, ","),
	}
}

// cancellationReason names the cancellation window rule that refused the
// request, for example CANCELLATION_WINDOW_EXPIRED, so that a client can tell it
// from a finished Order (FR-494).
func cancellationReason(err order.CancellationNotAllowed) string {
	if len(err.Reasons) == 0 {
		return "ORDER_STATE_CONFLICT"
	}
	return strings.ToUpper(string(err.Reasons[0].Code))
}
