package authz

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
)

func TestSubjectValidate(t *testing.T) {
	tests := []struct {
		name    string
		subject Subject
		valid   bool
	}{
		{name: "user", subject: Subject{Kind: SubjectUser, ID: "u-1"}, valid: true},
		{name: "service", subject: Subject{Kind: SubjectService, ID: "orders-worker"}, valid: true},
		{name: "unknown kind", subject: Subject{Kind: "system", ID: "x"}},
		{name: "missing id", subject: Subject{Kind: SubjectUser}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.subject.Validate()
			if tt.valid {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, usecase.ErrInvalidInput)
		})
	}
}

func TestFailClosedDeniesOnAnyCheckFailure(t *testing.T) {
	require.NoError(t, FailClosed(nil))
	require.ErrorIs(t, FailClosed(ErrPermissionDenied), ErrPermissionDenied)

	cause := errors.New("spicedb unavailable")
	err := FailClosed(cause)
	require.ErrorIs(t, err, ErrPermissionDenied)
	require.ErrorIs(t, err, cause)
}
