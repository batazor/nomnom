package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
)

// requireCustomer verifies the access token the Router forwards and puts the
// customer into the context; a missing or invalid token never reaches a
// context. It only authenticates: whether the slice of the operation is
// connected is requireSlice.
func (s *BffService) requireCustomer(ctx context.Context) (context.Context, error) {
	if s.accessTokens == nil {
		return nil, status.Error(codes.Unauthenticated, "authentication is not configured")
	}
	user, err := s.accessTokens.FromIncomingContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	return identity.WithUser(ctx, user), nil
}

// requireSlice answers that the operations of the named slice are not
// available while it is not connected.
func requireSlice(name string, connected bool) error {
	if !connected {
		return status.Error(codes.Unavailable, fmt.Sprintf("%s operations are not available", name))
	}
	return nil
}
