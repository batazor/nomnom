package vo

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/shopspring/decimal"
)

// ErrInvalidValue is the common cause of every value object validation error.
var ErrInvalidValue = errors.New("invalid value")

var (
	ErrNegativeAmount   = fmt.Errorf("%w: money amount must not be negative", ErrInvalidValue)
	ErrInvalidCurrency  = fmt.Errorf("%w: currency must be an ISO 4217 code", ErrInvalidValue)
	ErrCurrencyMismatch = fmt.Errorf("%w: money arithmetic requires one currency", ErrInvalidValue)
)

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// Money is an exact non-negative decimal amount in one currency.
type Money struct {
	amount   decimal.Decimal
	currency string
}

func NewMoney(amount decimal.Decimal, currency string) (Money, error) {
	if !currencyPattern.MatchString(currency) {
		return Money{}, ErrInvalidCurrency
	}
	if amount.IsNegative() {
		return Money{}, ErrNegativeAmount
	}
	return Money{amount: amount, currency: currency}, nil
}

// ParseMoney creates Money from an exact decimal string such as "12.50".
func ParseMoney(amount, currency string) (Money, error) {
	value, err := decimal.NewFromString(amount)
	if err != nil {
		return Money{}, fmt.Errorf("%w: money amount %q is not a decimal", ErrInvalidValue, amount)
	}
	return NewMoney(value, currency)
}

func ZeroMoney(currency string) (Money, error) {
	return NewMoney(decimal.Zero, currency)
}

func (m Money) Amount() decimal.Decimal { return m.amount }

func (m Money) Currency() string { return m.currency }

func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	return Money{amount: m.amount.Add(other.amount), currency: m.currency}, nil
}

func (m Money) Multiply(quantity Quantity) Money {
	return Money{amount: m.amount.Mul(decimal.NewFromInt(int64(quantity.Value()))), currency: m.currency}
}

func (m Money) Equal(other Money) bool {
	return m.currency == other.currency && m.amount.Equal(other.amount)
}

func (m Money) String() string {
	return m.amount.String() + " " + m.currency
}
