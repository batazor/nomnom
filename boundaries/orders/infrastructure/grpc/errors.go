package grpcadapter

import (
	"errors"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/timestamppb"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/place"
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

// transportByKind is the API error table of the README by usecase.ErrorKind.
// It covers every kind; errorkind.Classify decides the kind.
var transportByKind = map[usecase.ErrorKind]transport{
	usecase.KindInvalidInput:  {code: codes.InvalidArgument, reason: "INVALID_ARGUMENT"},
	usecase.KindNotFound:      {code: codes.NotFound, reason: "ORDER_NOT_FOUND"},
	usecase.KindAlreadyExists: {code: codes.AlreadyExists, reason: "ORDER_ALREADY_EXISTS"},
	usecase.KindConflict:      {code: codes.Aborted, reason: "CONCURRENCY_CONFLICT"},
	usecase.KindDomainRule:    {code: codes.FailedPrecondition, reason: "ORDER_STATE_CONFLICT"},
	usecase.KindUnavailable:   {code: codes.Unavailable, reason: "DEPENDENCY_UNAVAILABLE", message: "dependency unavailable"},
	usecase.KindPermission:    {code: codes.PermissionDenied, reason: "OPERATION_NOT_ALLOWED"},
	usecase.KindInternal:      {code: codes.Internal, reason: "INTERNAL_ERROR", message: "internal error"},
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
		answer = transportByKind[usecase.KindInternal]
	}
	var metadata map[string]string
	var details []protoadapt.MessageV1
	var notOrderable order.NotOrderable
	var notCancellable order.CancellationNotAllowed
	var slotUnavailable place.SlotUnavailable
	switch {
	case errors.Is(err, place.ErrUnknownRestaurant):
		answer.reason = "RESTAURANT_NOT_FOUND"
	case errors.Is(err, place.ErrRestaurantNotAcceptingOrders):
		answer.reason = "RESTAURANT_NOT_ACCEPTING_ORDERS"
	case errors.Is(err, place.ErrPremiumUnavailable):
		answer.reason = "PREMIUM_UNAVAILABLE"
	case errors.As(err, &slotUnavailable):
		answer.reason = "SLOT_UNAVAILABLE"
		details = append(details, slotUnavailableDetail(slotUnavailable))
	case errors.Is(err, place.ErrScheduledOrdersUnavailable):
		answer.reason = "SCHEDULED_ORDERS_UNAVAILABLE"
	case errors.Is(err, usecase.ErrInvalidInput):
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
	return withInfo(answer.code, answer.reason, message, metadata, details...)
}

// withInfo answers with ErrorInfo first and the API details of the reason
// after it, such as SlotUnavailable of SLOT_UNAVAILABLE.
func withInfo(code codes.Code, reason, message string, metadata map[string]string, details ...protoadapt.MessageV1) error {
	info := &errdetails.ErrorInfo{
		Reason:   reason,
		Domain:   errorDomain,
		Metadata: metadata,
	}
	withDetails, err := status.New(code, message).WithDetails(append([]protoadapt.MessageV1{info}, details...)...)
	if err != nil {
		return status.Error(code, message)
	}
	return withDetails.Err()
}

// slotUnavailableDetail carries the nearest available windows of a full
// Delivery Window (FR-692).
func slotUnavailableDetail(err place.SlotUnavailable) *ordersv1.SlotUnavailable {
	detail := &ordersv1.SlotUnavailable{Alternatives: make([]*ordersv1.DeliveryWindow, 0, len(err.Alternatives))}
	for _, window := range err.Alternatives {
		detail.Alternatives = append(detail.Alternatives, &ordersv1.DeliveryWindow{
			Start: timestamppb.New(window.Start()),
			End:   timestamppb.New(window.End()),
		})
	}
	return detail
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
