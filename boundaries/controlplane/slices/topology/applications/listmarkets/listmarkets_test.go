package listmarkets_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/listmarkets"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

// lister is a mock of the repository.
type lister []market.Market

func (l lister) List(context.Context) ([]market.Market, error) { return l, nil }

func TestOnlyAllowlistedServicesListMarkets(t *testing.T) {
	stored := lister{market.New("berlin", "Berlin", "EUR", "cell-1")}
	handler := listmarkets.New(application.ServiceAllowlist{"bff": true}, stored)

	got, err := handler.Handle(context.Background(), listmarkets.Query{Subject: application.Subject{Kind: application.SubjectService, ID: "bff", Service: "bff"}})
	require.NoError(t, err)
	assert.Equal(t, []market.Market(stored), got)

	_, err = handler.Handle(context.Background(), listmarkets.Query{Subject: application.Subject{Kind: application.SubjectService, ID: "grpc-caller", Service: "grpc-caller"}})
	assert.ErrorIs(t, err, application.ErrPermissionDenied)
}
