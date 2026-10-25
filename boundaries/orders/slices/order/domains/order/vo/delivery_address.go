package vo

import (
	"fmt"
	"strings"
)

const maxDeliveryAddressLength = 512

var ErrInvalidDeliveryAddress = fmt.Errorf("%w: delivery address must be a non-empty string up to %d characters", ErrInvalidValue, maxDeliveryAddressLength)

// DeliveryAddress is a normalized free-form address; its detailed structure is not modelled yet.
type DeliveryAddress struct {
	value string
}

func NewDeliveryAddress(value string) (DeliveryAddress, error) {
	normalized := strings.Join(strings.Fields(value), " ")
	if normalized == "" || len([]rune(normalized)) > maxDeliveryAddressLength {
		return DeliveryAddress{}, ErrInvalidDeliveryAddress
	}
	return DeliveryAddress{value: normalized}, nil
}

func (a DeliveryAddress) String() string { return a.value }
