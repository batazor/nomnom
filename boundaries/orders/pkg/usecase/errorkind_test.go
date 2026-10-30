package usecase_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
)

func TestKindOf(t *testing.T) {
	cause := errors.New("cause")
	tests := []struct {
		name string
		err  error
		want usecase.ErrorKind
	}{
		{"assigned kind", usecase.WithKind(usecase.KindNotFound, cause), usecase.KindNotFound},
		{"wrapped assigned kind", fmt.Errorf("call: %w", usecase.WithKind(usecase.KindDomainRule, cause)), usecase.KindDomainRule},
		{"invalid input", usecase.InvalidInput("step", "is required"), usecase.KindInvalidInput},
		{"conflict", usecase.ErrConcurrencyConflict, usecase.KindConflict},
		{"unavailable", usecase.ErrDependencyUnavailable, usecase.KindUnavailable},
		{"unknown", cause, usecase.KindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, usecase.KindOf(tt.err))
		})
	}
}

func TestWithKindKeepsTheCause(t *testing.T) {
	cause := errors.New("cause")

	require.ErrorIs(t, usecase.WithKind(usecase.KindNotFound, cause), cause)
	require.NoError(t, usecase.WithKind(usecase.KindNotFound, nil))
}
