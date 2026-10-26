package market_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

func TestRepeatWithTheSameValuesIsTheSameMarket(t *testing.T) {
	existing := market.New("berlin", "Berlin", "EUR", "cell-1")

	assert.NoError(t, market.New("berlin", "Berlin", "EUR", "cell-1").Repeat(existing))
	for _, other := range []market.Market{
		market.New("berlin", "Berlin Mitte", "EUR", "cell-1"),
		market.New("berlin", "Berlin", "USD", "cell-1"),
		market.New("berlin", "Berlin", "EUR", "cell-2"),
	} {
		assert.ErrorIs(t, other.Repeat(existing), market.ErrAlreadyCreated)
	}
}
