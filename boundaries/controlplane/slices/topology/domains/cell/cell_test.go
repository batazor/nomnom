package cell_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
)

func TestRepeatWithTheSameValuesIsTheSameCell(t *testing.T) {
	router, err := vo.NewRouter("nomnom-bff", "bff", 3010)
	require.NoError(t, err)
	other, err := vo.NewRouter("nomnom-bff", "bff", 3011)
	require.NoError(t, err)
	registered := cell.New("cell-1", router)

	assert.NoError(t, cell.New("cell-1", router).Repeat(registered))
	assert.ErrorIs(t, cell.New("cell-1", other).Repeat(registered), cell.ErrAlreadyRegistered)
}
