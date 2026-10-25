package grpcadapter

import (
	"context"
	"errors"

	"nomnom/orders/pkg/application"
)

var ErrUnauthenticated = errors.New("caller identity is not authenticated")

// SubjectResolver authenticates the transport caller and returns the verified
// application identity. Implementations, not RPC messages, own credentials.
type SubjectResolver interface {
	Resolve(ctx context.Context) (application.Subject, error)
}

// RejectingSubjectResolver is the secure default until the internal identity
// verification adapter is configured. It deliberately does not trust arbitrary
// gRPC metadata or the fact that a request passed through the gateway.
type RejectingSubjectResolver struct{}

func NewRejectingSubjectResolver() SubjectResolver { return RejectingSubjectResolver{} }

func (RejectingSubjectResolver) Resolve(context.Context) (application.Subject, error) {
	return application.Subject{}, ErrUnauthenticated
}
