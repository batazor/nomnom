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

func TestRouterIsAServiceOfANamespace(t *testing.T) {
	router, err := vo.NewRouter("nomnom-bff", "bff", 3010)
	require.NoError(t, err)
	assert.Equal(t, "nomnom-bff", router.Namespace())
	assert.Equal(t, "bff", router.Name())
	assert.Equal(t, int32(3010), router.Port())

	for _, invalid := range []struct {
		namespace, name string
		port            int32
	}{
		{"", "bff", 3010},
		{"nomnom-bff", "", 3010},
		{"nomnom-bff", "Bff_1", 3010},
		{"nomnom.bff", "bff", 3010},
		{"nomnom-bff", "-bff", 3010},
		{"nomnom-bff", "bff", 0},
		{"nomnom-bff", "bff", 65536},
	} {
		_, err := vo.NewRouter(invalid.namespace, invalid.name, invalid.port)
		assert.ErrorIs(t, err, vo.ErrInvalidValue, invalid)
	}
}
