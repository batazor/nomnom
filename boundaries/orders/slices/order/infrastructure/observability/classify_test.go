package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nomnom/orders/pkg/usecase"
)

// AC-635: the telemetry table reports every usecase.ErrorKind.
func TestFailureTableCoversEveryKind(t *testing.T) {
	for _, kind := range usecase.ErrorKinds() {
		failure, ok := failureByKind[kind]
		assert.True(t, ok, kind)
		assert.NotEmpty(t, failure.Outcome, kind)
		assert.NotEmpty(t, failure.ErrorType, kind)
	}
}
