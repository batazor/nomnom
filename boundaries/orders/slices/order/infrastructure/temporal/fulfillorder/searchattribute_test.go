package fulfillorder_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/grpc"

	"nomnom/orders/slices/order/infrastructure/temporal/fulfillorder"
)

// fakeOperator implements the two Operator API calls the worker uses.
type fakeOperator struct {
	operatorservice.OperatorServiceClient
	attributes map[string]enumspb.IndexedValueType
	addErr     error
	added      []*operatorservice.AddSearchAttributesRequest
}

func (f *fakeOperator) ListSearchAttributes(context.Context, *operatorservice.ListSearchAttributesRequest, ...grpc.CallOption) (*operatorservice.ListSearchAttributesResponse, error) {
	return &operatorservice.ListSearchAttributesResponse{CustomAttributes: f.attributes}, nil
}

func (f *fakeOperator) AddSearchAttributes(_ context.Context, request *operatorservice.AddSearchAttributesRequest, _ ...grpc.CallOption) (*operatorservice.AddSearchAttributesResponse, error) {
	f.added = append(f.added, request)
	return &operatorservice.AddSearchAttributesResponse{}, f.addErr
}

const stage = "OrderFulfillmentStage"

func TestEnsureStageSearchAttributeAcceptsRegisteredKeyword(t *testing.T) {
	operator := &fakeOperator{attributes: map[string]enumspb.IndexedValueType{stage: enumspb.INDEXED_VALUE_TYPE_KEYWORD}}

	require.NoError(t, fulfillorder.EnsureStageSearchAttribute(context.Background(), operator, "default", false))
	assert.Empty(t, operator.added)
}

// AC-449: a missing attribute is registered when allowed.
func TestEnsureStageSearchAttributeRegistersMissing(t *testing.T) {
	for name, addErr := range map[string]error{
		"created":        nil,
		"created racing": serviceerror.NewAlreadyExists("exists"),
	} {
		t.Run(name, func(t *testing.T) {
			operator := &fakeOperator{addErr: addErr}

			require.NoError(t, fulfillorder.EnsureStageSearchAttribute(context.Background(), operator, "orders-local", true))
			require.Len(t, operator.added, 1)
			assert.Equal(t, "orders-local", operator.added[0].GetNamespace())
			assert.Equal(t, map[string]enumspb.IndexedValueType{stage: enumspb.INDEXED_VALUE_TYPE_KEYWORD}, operator.added[0].GetSearchAttributes())
		})
	}
}

// AC-450: without permission to register, a missing attribute fails startup.
func TestEnsureStageSearchAttributeFailsWhenMissing(t *testing.T) {
	operator := &fakeOperator{}

	err := fulfillorder.EnsureStageSearchAttribute(context.Background(), operator, "default", false)

	require.ErrorIs(t, err, fulfillorder.ErrSearchAttributeMissing)
	assert.Contains(t, err.Error(), stage)
	assert.Contains(t, err.Error(), "TEMPORAL__REGISTER_SEARCH_ATTRIBUTES")
	assert.Empty(t, operator.added)
}

func TestEnsureStageSearchAttributeRejectsWrongType(t *testing.T) {
	operator := &fakeOperator{attributes: map[string]enumspb.IndexedValueType{stage: enumspb.INDEXED_VALUE_TYPE_TEXT}}

	require.Error(t, fulfillorder.EnsureStageSearchAttribute(context.Background(), operator, "default", true))
	assert.Empty(t, operator.added)
}

func TestEnsureStageSearchAttributeReturnsRegistrationFailure(t *testing.T) {
	operator := &fakeOperator{addErr: serviceerror.NewPermissionDenied("operator only", "")}

	require.Error(t, fulfillorder.EnsureStageSearchAttribute(context.Background(), operator, "default", true))
}
