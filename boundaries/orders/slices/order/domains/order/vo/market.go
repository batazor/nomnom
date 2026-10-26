package vo

import (
	"fmt"
	"regexp"
)

var ErrInvalidMarketID = fmt.Errorf("%w: market_id must match ^[a-z][a-z0-9-]{1,31}$", ErrInvalidValue)

var marketIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// MarketID identifies the Market (city) an Order belongs to (ADR 41). It is
// taken from the Restaurant at Place Order and never changes.
type MarketID struct {
	value string
}

func NewMarketID(value string) (MarketID, error) {
	if !marketIDPattern.MatchString(value) {
		return MarketID{}, fmt.Errorf("%w: %q", ErrInvalidMarketID, value)
	}
	return MarketID{value: value}, nil
}

func (m MarketID) String() string { return m.value }

// IsZero reports a MarketID that was never set.
func (m MarketID) IsZero() bool { return m.value == "" }
