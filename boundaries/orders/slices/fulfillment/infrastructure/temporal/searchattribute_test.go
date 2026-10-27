package temporal_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"

	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
)

const stage = "OrderFulfillmentStage"

func TestEnsureStageSearchAttributeAcceptsRegisteredKeyword(t *testing.T) {
	operator := NewMockSearchAttributes(t)
	operator.EXPECT().ListSearchAttributes(mock.Anything, mock.Anything).
		Return(&operatorservice.ListSearchAttributesResponse{CustomAttributes: map[string]enumspb.IndexedValueType{stage: enumspb.INDEXED_VALUE_TYPE_KEYWORD}}, nil).Once()

	require.NoError(t, temporal.EnsureStageSearchAttribute(context.Background(), operator, "default", false))
}

// AC-449: a missing attribute is registered when allowed.
func TestEnsureStageSearchAttributeRegistersMissing(t *testing.T) {
	for name, addErr := range map[string]error{
		"created":        nil,
		"created racing": serviceerror.NewAlreadyExists("exists"),
	} {
		t.Run(name, func(t *testing.T) {
			operator := NewMockSearchAttributes(t)
			operator.EXPECT().ListSearchAttributes(mock.Anything, mock.Anything).
				Return(&operatorservice.ListSearchAttributesResponse{}, nil).Once()
			operator.EXPECT().AddSearchAttributes(mock.Anything, mock.MatchedBy(func(request *operatorservice.AddSearchAttributesRequest) bool {
				return request.GetNamespace() == "orders-local" &&
					assert.ObjectsAreEqual(map[string]enumspb.IndexedValueType{stage: enumspb.INDEXED_VALUE_TYPE_KEYWORD}, request.GetSearchAttributes())
			})).Return(&operatorservice.AddSearchAttributesResponse{}, addErr).Once()

			require.NoError(t, temporal.EnsureStageSearchAttribute(context.Background(), operator, "orders-local", true))
		})
	}
}

// AC-450: without permission to register, a missing attribute fails startup.
func TestEnsureStageSearchAttributeFailsWhenMissing(t *testing.T) {
	operator := NewMockSearchAttributes(t)
	operator.EXPECT().ListSearchAttributes(mock.Anything, mock.Anything).
		Return(&operatorservice.ListSearchAttributesResponse{}, nil).Once()

	err := temporal.EnsureStageSearchAttribute(context.Background(), operator, "default", false)

	require.ErrorIs(t, err, temporal.ErrSearchAttributeMissing)
	assert.Contains(t, err.Error(), stage)
	assert.Contains(t, err.Error(), "TEMPORAL__REGISTER_SEARCH_ATTRIBUTES")
}

func TestEnsureStageSearchAttributeRejectsWrongType(t *testing.T) {
	operator := NewMockSearchAttributes(t)
	operator.EXPECT().ListSearchAttributes(mock.Anything, mock.Anything).
		Return(&operatorservice.ListSearchAttributesResponse{CustomAttributes: map[string]enumspb.IndexedValueType{stage: enumspb.INDEXED_VALUE_TYPE_TEXT}}, nil).Once()

	require.Error(t, temporal.EnsureStageSearchAttribute(context.Background(), operator, "default", true))
}

func TestEnsureStageSearchAttributeReturnsRegistrationFailure(t *testing.T) {
	operator := NewMockSearchAttributes(t)
	operator.EXPECT().ListSearchAttributes(mock.Anything, mock.Anything).
		Return(&operatorservice.ListSearchAttributesResponse{}, nil).Once()
	operator.EXPECT().AddSearchAttributes(mock.Anything, mock.Anything).
		Return(&operatorservice.AddSearchAttributesResponse{}, serviceerror.NewPermissionDenied("operator only", "")).Once()

	require.Error(t, temporal.EnsureStageSearchAttribute(context.Background(), operator, "default", true))
}
