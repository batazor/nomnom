// Package cellrouter routes the calls of BFF slices to the Cell that serves the
// Market of the client request (ADR 41): the Market comes from the incoming
// metadata x-nomnom-market, the Cell gateway from the Placement Map of the
// control plane. There is no default Cell: a request without a routable Market
// is refused.
package cellrouter

import (
	"context"
	"regexp"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// MarketMetadata is the incoming gRPC metadata the Cosmo Router forwards the
// client header X-Nomnom-Market as (headers.all.request propagate).
const MarketMetadata = "x-nomnom-market"

// marketSlug is the format of market_id (ADR 41).
var marketSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// Refusal is a request the router does not route. It is deliberately not a
// gRPC status (no GRPCStatus method): slice adapters map status codes of the
// owner context to business errors, and a routing refusal must not pass for
// one, such as NOT_FOUND of a restaurant. The presenter turns it into the
// status for the client with Err.
type Refusal struct {
	Code    codes.Code
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// Err is the gRPC status of the refusal for the client.
func (r *Refusal) Err() error { return status.Error(r.Code, r.Message) }

// MarketFrom returns the validated Market of the client request: exactly one
// x-nomnom-market in the incoming metadata, in the slug format. It reads the
// incoming metadata only, so the Market never travels on to contexts as
// outgoing metadata; list operations pass it in their request (market_id).
func MarketFrom(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, MarketMetadata)
	switch len(values) {
	case 0:
		return "", &Refusal{Code: codes.InvalidArgument, Message: "X-Nomnom-Market header is required"}
	case 1:
	default:
		return "", &Refusal{Code: codes.InvalidArgument, Message: "X-Nomnom-Market header must be sent once"}
	}
	if !marketSlug.MatchString(values[0]) {
		return "", &Refusal{Code: codes.InvalidArgument, Message: "X-Nomnom-Market must match " + marketSlug.String()}
	}
	return values[0], nil
}
