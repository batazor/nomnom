package grpcadapter

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/usecase"
)

// The error table covers every kind of the application layer.
func TestTransportTableCoversEveryKind(t *testing.T) {
	for _, kind := range usecase.ErrorKinds() {
		_, ok := transportByKind[kind]
		assert.True(t, ok, kind)
	}
}
