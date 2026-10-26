package cell_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
)

func TestRepeatWithTheSameValuesIsTheSameCell(t *testing.T) {
	registered := cell.New("cell-1", "gateway:8080")

	assert.NoError(t, cell.New("cell-1", "gateway:8080").Repeat(registered))
	assert.ErrorIs(t, cell.New("cell-1", vo.GatewayAddress("other:8080")).Repeat(registered), cell.ErrAlreadyRegistered)
}
