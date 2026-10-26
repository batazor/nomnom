// Package vo holds the value objects of a Market (ADR 41): id, name and
// currency.
package vo

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ErrInvalidValue marks a value that breaks the rules of its value object.
var ErrInvalidValue = errors.New("invalid value")

var (
	marketIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
)

// MaxNameLength bounds the name of a Market in characters, after trim.
const MaxNameLength = 64

// MarketID identifies a Market: slug ^[a-z][a-z0-9-]{1,31}$, immutable.
type MarketID string

func NewMarketID(raw string) (MarketID, error) {
	if !marketIDPattern.MatchString(raw) {
		return "", fmt.Errorf("%w: market_id %q must match %s", ErrInvalidValue, raw, marketIDPattern)
	}
	return MarketID(raw), nil
}

// Name is the display name of a Market: 1..64 characters after trim; it is
// stored trimmed.
type Name string

func NewName(raw string) (Name, error) {
	name := strings.TrimSpace(raw)
	if length := utf8.RuneCountInString(name); length < 1 || length > MaxNameLength {
		return "", fmt.Errorf("%w: name must have 1..%d characters after trim, got %d", ErrInvalidValue, MaxNameLength, length)
	}
	return Name(name), nil
}

// Currency is the ISO 4217 code of the money of a Market: three upper-case letters.
type Currency string

func NewCurrency(raw string) (Currency, error) {
	if !currencyPattern.MatchString(raw) {
		return "", fmt.Errorf("%w: currency %q must be an ISO 4217 code of three upper-case letters", ErrInvalidValue, raw)
	}
	return Currency(raw), nil
}
