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

func TestServiceAllowlistChecksTheCallingService(t *testing.T) {
	readers := application.ServiceAllowlist{"bff": true}
	assert.NoError(t, readers.Allow(application.Subject{Kind: application.SubjectService, ID: "bff", Service: "bff"}))
	// A user asserted by BFF reads as BFF: the allowlist names workloads, not users.
	assert.NoError(t, readers.Allow(application.Subject{Kind: application.SubjectUser, ID: "user-1", Service: "bff"}))
	assert.ErrorIs(t, readers.Allow(application.Subject{Kind: application.SubjectService, ID: "demo-seed", Service: "demo-seed"}), application.ErrPermissionDenied)
	assert.ErrorIs(t, readers.Allow(application.Subject{Kind: application.SubjectService, ID: "bff"}), application.ErrInvalidInput)
}
