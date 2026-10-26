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

var operator = application.Subject{Kind: application.SubjectUser, ID: "kind-operator", Service: "demo-seed"}

// lister is a mock of the repository.
type lister []market.Market

func (l lister) List(context.Context) ([]market.Market, error) { return l, nil }

// granted is a mock of the SpiceDB check granting manage_markets to the operator only.
type granted struct{}

func (granted) Check(_ context.Context, check application.AccessCheck) error {
	if check.Subject.ID != operator.ID || check.Permission != "manage_markets" {
		return application.ErrPermissionDenied
	}
	return nil
}

func TestOnlyTheOperatorListsMarkets(t *testing.T) {
	stored := lister{market.New("berlin", "Berlin", "EUR", "cell-1")}
	handler := listmarkets.New(granted{}, stored)

	got, err := handler.Handle(context.Background(), listmarkets.Query{Subject: operator})
	require.NoError(t, err)
	assert.Equal(t, []market.Market(stored), got)

	_, err = handler.Handle(context.Background(), listmarkets.Query{Subject: application.Subject{Kind: application.SubjectService, ID: "bff", Service: "bff"}})
	assert.ErrorIs(t, err, application.ErrPermissionDenied)
}
