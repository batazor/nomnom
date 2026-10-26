package application_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
)

func TestFailClosedTurnsAFailedCheckIntoDenial(t *testing.T) {
	assert.NoError(t, application.FailClosed(nil))
	assert.ErrorIs(t, application.FailClosed(application.ErrPermissionDenied), application.ErrPermissionDenied)
	cause := errors.New("spicedb timeout")
	err := application.FailClosed(cause)
	assert.ErrorIs(t, err, application.ErrPermissionDenied)
	assert.ErrorIs(t, err, cause)
}
