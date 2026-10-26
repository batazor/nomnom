// Package market reads the Market of a client request (ADR 41). The public
// gateway routes /m/{market_id} to the Cell of the Market and sets the header
// X-Nomnom-Market from its route; the Cosmo Router forwards it to the plugin
// as incoming metadata. BFF serves one Cell: it does not pick a Cell, it only
// requires a valid Market before a context is called.
package market

import (
	"context"
	"regexp"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Metadata is the incoming gRPC metadata the Cosmo Router forwards the header
// X-Nomnom-Market as (headers.all.request propagate).
const Metadata = "x-nomnom-market"

// slug is the format of market_id (ADR 41).
var slug = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// Refusal is a request without a valid Market. It is deliberately not a gRPC
// status (no GRPCStatus method): slice adapters map status codes of the owner
// context to business errors, and a refusal must not pass for one, such as
// NOT_FOUND of a restaurant. The presenter turns it into the status for the
// client with Err.
type Refusal struct {
	Code    codes.Code
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// Err is the gRPC status of the refusal for the client.
func (r *Refusal) Err() error { return status.Error(r.Code, r.Message) }

// From returns the validated Market of the client request: exactly one
// x-nomnom-market in the incoming metadata, in the slug format. It reads the
// incoming metadata only, so the Market never travels on to contexts as
// outgoing metadata; list operations pass it in their request (market_id).
func From(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, Metadata)
	switch len(values) {
	case 0:
		return "", &Refusal{Code: codes.InvalidArgument, Message: "X-Nomnom-Market header is required"}
	case 1:
	default:
		return "", &Refusal{Code: codes.InvalidArgument, Message: "X-Nomnom-Market header must be sent once"}
	}
	if !slug.MatchString(values[0]) {
		return "", &Refusal{Code: codes.InvalidArgument, Message: "X-Nomnom-Market must match " + slug.String()}
	}
	return values[0], nil
}

// Require wraps the connection of slices to a context: a call without a valid
// Market is refused before it reaches the context.
func Require(conn grpc.ClientConnInterface) grpc.ClientConnInterface {
	return requiredConn{conn: conn}
}

type requiredConn struct {
	conn grpc.ClientConnInterface
}

func (c requiredConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	if _, err := From(ctx); err != nil {
		return err
	}
	return c.conn.Invoke(ctx, method, args, reply, opts...)
}

func (c requiredConn) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	if _, err := From(ctx); err != nil {
		return nil, err
	}
	return c.conn.NewStream(ctx, desc, method, opts...)
}
