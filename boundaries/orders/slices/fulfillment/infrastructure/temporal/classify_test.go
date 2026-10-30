package temporal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"nomnom/orders/pkg/usecase"
)

// AC-635: the Activity table decides retryability for every usecase.ErrorKind.
func TestRetryabilityTableCoversEveryKind(t *testing.T) {
	for _, kind := range usecase.ErrorKinds() {
		r, ok := retryabilityByKind[kind]
		assert.True(t, ok, kind)
		assert.Equal(t, r.nonRetryable, r.errType != "", kind)
	}
}

func TestClassifyKeepsErrorTypes(t *testing.T) {
	tests := []struct {
		err      error
		wantType string
	}{
		{usecase.InvalidInput("step", "x"), ErrTypeInvalidInput},
		{usecase.WithKind(usecase.KindNotFound, errors.New("order not found")), ErrTypeOrderNotFound},
		{usecase.WithKind(usecase.KindDomainRule, errors.New("invalid transition")), ErrTypeInvalidTransition},
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
