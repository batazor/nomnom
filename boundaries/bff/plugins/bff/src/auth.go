package main

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/identity"
)

// requireCustomer verifies the access token the Router forwards and puts the
// customer into the context; a missing or invalid token never reaches a
// context.
func (s *BffService) requireCustomer(ctx context.Context) (context.Context, error) {
	user, err := s.accessTokens.FromIncomingContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	return identity.WithUser(ctx, user), nil
}
