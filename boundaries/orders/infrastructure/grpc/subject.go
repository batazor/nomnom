package grpcadapter

import (
	"context"
	"errors"

	"nomnom/orders/pkg/authz"
)

var ErrUnauthenticated = errors.New("caller identity is not authenticated")

// SubjectResolver authenticates the transport caller and returns the verified
// application identity. Implementations, not RPC messages, own credentials.
type SubjectResolver interface {
	Resolve(ctx context.Context) (authz.Subject, error)
}
