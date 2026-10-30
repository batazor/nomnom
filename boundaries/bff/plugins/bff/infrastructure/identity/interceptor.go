package identity

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type userKey struct{}

// WithUser puts the verified customer into the request context.
func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// WithoutUser drops the customer from the context, for calls of public APIs
// that must not carry a user assertion.
func WithoutUser(ctx context.Context) context.Context {
	return context.WithValue(ctx, userKey{}, nil)
}

func UserFrom(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userKey{}).(User)
	return user, ok
}

// UnaryClientInterceptor authenticates BFF calls to one context: the service
// token of BFF always, and a user assertion for that audience when the request
// context carries a verified customer.
func UnaryClientInterceptor(audience string, tokens TokenSource, signer Signer) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		token, err := tokens.Token(audience)
		if err != nil {
			return err
		}
		pairs := []string{"authorization", "Bearer " + token}
		if user, ok := UserFrom(ctx); ok {
			assertion, err := signer.Sign(user, audience)
			if err != nil {
				return fmt.Errorf("sign user assertion: %w", err)
			}
			pairs = append(pairs, AssertionMetadata, assertion)
		}
		return invoker(metadata.AppendToOutgoingContext(ctx, pairs...), method, req, reply, cc, opts...)
	}
}
