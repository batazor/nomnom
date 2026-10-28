package vo_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

func TestMarketIDIsASlug(t *testing.T) {
	_, err := vo.NewMarketID("berlin")
	require.NoError(t, err)
	for _, invalid := range []string{"", "b", "Berlin", "9berlin", "new_york"} {
		_, err := vo.NewMarketID(invalid)
		assert.ErrorIs(t, err, vo.ErrInvalidValue, invalid)
	}
}

func TestNameIsTrimmedAndBounded(t *testing.T) {
	name, err := vo.NewName("  Berlin \n")
	require.NoError(t, err)
	assert.Equal(t, vo.Name("Berlin"), name)

	_, err = vo.NewName(strings.Repeat("я", vo.MaxNameLength))
	require.NoError(t, err, "length counts characters, not bytes")
	for _, invalid := range []string{"", "   ", strings.Repeat("a", vo.MaxNameLength+1)} {
		_, err := vo.NewName(invalid)
		assert.ErrorIs(t, err, vo.ErrInvalidValue)
	}
}

func TestCurrencyIsAnISO4217Code(t *testing.T) {
	_, err := vo.NewCurrency("EUR")
	require.NoError(t, err)
	for _, invalid := range []string{"", "eur", "EURO", "E1R"} {
		_, err := vo.NewCurrency(invalid)
		assert.ErrorIs(t, err, vo.ErrInvalidValue, invalid)
	}
}

func TestTimeZoneIsAnIANAName(t *testing.T) {
	timeZone, err := vo.NewTimeZone("Europe/Berlin")
	require.NoError(t, err)
	assert.Equal(t, vo.TimeZone("Europe/Berlin"), timeZone)
	for _, invalid := range []string{"", "Local", "Mars/Olympus", "Europe/Berlin "} {
		_, err := vo.NewTimeZone(invalid)
		assert.ErrorIs(t, err, vo.ErrInvalidValue, invalid)
	}
}
