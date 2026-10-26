package vo_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
)

func TestCellIDIsASlug(t *testing.T) {
	for _, valid := range []string{"cell-1", "eu", "a234567890123456789012345678901b"} {
		id, err := vo.NewCellID(valid)
		require.NoError(t, err, valid)
		assert.Equal(t, vo.CellID(valid), id)
	}
	for _, invalid := range []string{"", "c", "1cell", "Cell-1", "cell_1", "cell 1", "a2345678901234567890123456789012c"} {
		_, err := vo.NewCellID(invalid)
		assert.ErrorIs(t, err, vo.ErrInvalidValue, invalid)
	}
}

func TestGatewayAddressIsHostAndPort(t *testing.T) {
	for _, valid := range []string{"internal-gateway.envoy-gateway-system.svc.cluster.local:8080", "10.0.0.1:443", "[::1]:8080"} {
		_, err := vo.NewGatewayAddress(valid)
		require.NoError(t, err, valid)
	}
	for _, invalid := range []string{"", "gateway", ":8080", "gateway:", "gateway:0", "gateway:65536", "gateway:http", "http://gateway:8080"} {
		_, err := vo.NewGatewayAddress(invalid)
		assert.ErrorIs(t, err, vo.ErrInvalidValue, invalid)
	}
}
