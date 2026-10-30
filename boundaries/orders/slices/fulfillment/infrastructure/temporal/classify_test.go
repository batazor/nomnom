package temporal

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/fulfillment/applications/orders"
)

func TestClassifyKeepsErrorTypes(t *testing.T) {
	tests := []struct {
		err      error
		wantType string
	}{
		{usecase.InvalidInput("step", "x"), ErrTypeInvalidInput},
		{fmt.Errorf("get order: %w", orders.ErrOrderNotFound), ErrTypeOrderNotFound},
		{fmt.Errorf("complete order: %w", orders.ErrStepRefused), ErrTypeInvalidTransition},
	}
	for _, tt := range tests {
		var applicationErr *temporal.ApplicationError
		require.True(t, errors.As(classify(tt.err), &applicationErr), tt.err)
		assert.Equal(t, tt.wantType, applicationErr.Type())
		assert.True(t, applicationErr.NonRetryable())
		assert.ErrorIs(t, classify(tt.err), tt.err)
	}

	for _, err := range []error{usecase.ErrConcurrencyConflict, usecase.ErrDependencyUnavailable, errors.New("reset")} {
		var applicationErr *temporal.ApplicationError
		assert.False(t, errors.As(classify(err), &applicationErr), err)
	}
	assert.NoError(t, classify(nil))
}
