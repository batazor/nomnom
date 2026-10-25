package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nomnom/orders/pkg/application"
)

// AC-635: the telemetry table reports every application.ErrorKind.
func TestFailureTableCoversEveryKind(t *testing.T) {
	for _, kind := range application.ErrorKinds() {
		failure, ok := failureByKind[kind]
		assert.True(t, ok, kind)
		assert.NotEmpty(t, failure.Outcome, kind)
		assert.NotEmpty(t, failure.ErrorType, kind)
	}
}
