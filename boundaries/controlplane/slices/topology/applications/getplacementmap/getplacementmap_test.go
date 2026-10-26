package getplacementmap_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/getplacementmap"
)

// placements is a mock of the read model.
type placements []getplacementmap.Placement

func (p placements) Placements(context.Context) ([]getplacementmap.Placement, error) { return p, nil }

func TestOnlyAllowlistedServicesReadThePlacementMap(t *testing.T) {
	stored := placements{{MarketID: "berlin", CellID: "cell-1", GatewayAddress: "gateway:8080"}}
	handler := getplacementmap.New(application.ServiceAllowlist{"bff": true}, stored)

	got, err := handler.Handle(context.Background(), getplacementmap.Query{Subject: application.Subject{Kind: application.SubjectService, ID: "bff", Service: "bff"}})
	require.NoError(t, err)
	assert.Equal(t, []getplacementmap.Placement(stored), got)

	_, err = handler.Handle(context.Background(), getplacementmap.Query{Subject: application.Subject{Kind: application.SubjectUser, ID: "kind-operator", Service: "demo-seed"}})
	assert.ErrorIs(t, err, application.ErrPermissionDenied)
}
