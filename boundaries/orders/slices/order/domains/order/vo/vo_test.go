package vo

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMoneyRejectsNegativeAmountAndBadCurrency(t *testing.T) {
	_, err := ParseMoney("-0.01", "EUR")
	require.ErrorIs(t, err, ErrNegativeAmount)

	_, err = ParseMoney("1.00", "eur")
	require.ErrorIs(t, err, ErrInvalidCurrency)

	_, err = ParseMoney("1,00", "EUR")
	require.ErrorIs(t, err, ErrInvalidValue)
}

func TestMoneyArithmeticIsExactAndSingleCurrency(t *testing.T) {
	price, err := ParseMoney("0.10", "EUR")
	require.NoError(t, err)
	three, err := NewQuantity(3)
	require.NoError(t, err)

	total := price.Multiply(three)
	assert.True(t, total.Amount().Equal(decimal.RequireFromString("0.30")))

	sum, err := total.Add(price)
	require.NoError(t, err)
	assert.Equal(t, "0.4 EUR", sum.String())

	usd, err := ParseMoney("1", "USD")
	require.NoError(t, err)
	_, err = sum.Add(usd)
	require.ErrorIs(t, err, ErrCurrencyMismatch)
}

func TestQuantityMustBePositive(t *testing.T) {
	_, err := NewQuantity(0)
	require.ErrorIs(t, err, ErrInvalidQuantity)
}

func TestMarketIDIsASlug(t *testing.T) {
	market, err := NewMarketID("berlin")
	require.NoError(t, err)
	assert.Equal(t, "berlin", market.String())
	assert.False(t, market.IsZero())

	_, err = NewMarketID("new-york-2")
	require.NoError(t, err)

	for _, invalid := range []string{"", "b", "Berlin", "1berlin", "-berlin", "berlin_city", "a" + strings.Repeat("b", 32)} {
		_, err := NewMarketID(invalid)
		require.ErrorIs(t, err, ErrInvalidMarketID, invalid)
		require.ErrorIs(t, err, ErrInvalidValue, invalid)
	}
	assert.True(t, MarketID{}.IsZero())
}
