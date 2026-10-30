package authz_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/authz"
)

func TestFailClosedTurnsAFailedCheckIntoDenial(t *testing.T) {
	assert.NoError(t, authz.FailClosed(nil))
	assert.ErrorIs(t, authz.FailClosed(authz.ErrPermissionDenied), authz.ErrPermissionDenied)
	cause := errors.New("spicedb timeout")
	err := authz.FailClosed(cause)
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
	assert.ErrorIs(t, err, cause)
}
