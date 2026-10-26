package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/cellrouter"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/markets/applications"
)

type fakeListMarkets struct {
	markets []applications.Market
	err     error
}

func (f *fakeListMarkets) Handle(context.Context) ([]applications.Market, error) {
	return f.markets, f.err
}

// markets is public: no access token and no Market header.
func TestQueryMarketsMapsTheList(t *testing.T) {
	bff := &BffService{listMarkets: &fakeListMarkets{markets: []applications.Market{
		{ID: "berlin", Name: "Berlin", Currency: "EUR"},
	}}}

	response, err := bff.QueryMarkets(context.Background(), &service.QueryMarketsRequest{})

	require.NoError(t, err)
	require.Len(t, response.GetMarkets(), 1)
	assert.Equal(t, "berlin", response.GetMarkets()[0].GetId())
	assert.Equal(t, "Berlin", response.GetMarkets()[0].GetName())
	assert.Equal(t, "EUR", response.GetMarkets()[0].GetCurrency())
}

func TestQueryMarketsFailureIsUnavailable(t *testing.T) {
	bff := &BffService{listMarkets: &fakeListMarkets{err: errors.New("control plane is down")}}
	_, err := bff.QueryMarkets(context.Background(), &service.QueryMarketsRequest{})
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// ADR 41: a refusal of the cell router reaches the client with its own code,
// however deep the slice wrapped it; other failures stay UNAVAILABLE.
func TestInternalErrorKeepsRouterRefusals(t *testing.T) {
	for _, code := range []codes.Code{codes.InvalidArgument, codes.NotFound, codes.Unavailable} {
		refusal := &cellrouter.Refusal{Code: code, Message: "refused"}
		err := internalError("restaurants", fmt.Errorf("list restaurants: %w", refusal))
		assert.Equal(t, code, status.Code(err))
		assert.Equal(t, "refused", status.Convert(err).Message())
	}
	err := internalError("restaurants", status.Error(codes.NotFound, "restaurant-1"))
	assert.Equal(t, codes.Unavailable, status.Code(err), "a status of a context is not a refusal")
}
