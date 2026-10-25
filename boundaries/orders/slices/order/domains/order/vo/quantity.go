package vo

import "fmt"

var ErrInvalidQuantity = fmt.Errorf("%w: quantity must be a positive integer", ErrInvalidValue)

// Quantity is a positive number of ordered units.
type Quantity struct {
	value int
}

func NewQuantity(value int) (Quantity, error) {
	if value < 1 {
		return Quantity{}, ErrInvalidQuantity
	}
	return Quantity{value: value}, nil
}

func (q Quantity) Value() int { return q.value }
