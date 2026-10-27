package order

import (
	"errors"
	"fmt"

	"nomnom/orders/slices/order/domains/order/vo"
)

var (
	ErrUnknownPriority   = errors.New("order priority must be STANDARD or PREMIUM")
	ErrInvalidPremiumFee = fmt.Errorf("%w: a premium order requires a positive premium fee, a standard one none", vo.ErrInvalidValue)
)

// Priority is the Premium Priority chosen at Place Order (FR-650): the kitchen
// starts a premium Order first.
type Priority string

const (
	PriorityStandard Priority = "STANDARD"
	PriorityPremium  Priority = "PREMIUM"
)

// ParsePriority accepts only the two known priorities.
func ParsePriority(value string) (Priority, error) {
	switch priority := Priority(value); priority {
	case PriorityStandard, PriorityPremium:
		return priority, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownPriority, value)
	}
}

// Service is the priority of an Order with the fee paid for it, fixed at Place
// Order: a premium Order carries a positive fee in the currency of its items,
// a standard Order none.
type Service struct {
	priority   Priority
	premiumFee vo.Money
	hasFee     bool
}

// StandardService is an Order without Premium Priority.
func StandardService() Service {
	return Service{priority: PriorityStandard}
}

// PremiumService is Premium Priority for a positive fee.
func PremiumService(fee vo.Money) (Service, error) {
	if !fee.Amount().IsPositive() {
		return Service{}, ErrInvalidPremiumFee
	}
	return Service{priority: PriorityPremium, premiumFee: fee, hasFee: true}, nil
}

// RestoreService rebuilds a stored Service: fee is nil exactly for a standard
// Order.
func RestoreService(priority Priority, fee *vo.Money) (Service, error) {
	switch priority {
	case PriorityStandard:
		if fee != nil {
			return Service{}, ErrInvalidPremiumFee
		}
		return StandardService(), nil
	case PriorityPremium:
		if fee == nil {
			return Service{}, ErrInvalidPremiumFee
		}
		return PremiumService(*fee)
	default:
		return Service{}, fmt.Errorf("%w: %q", ErrUnknownPriority, priority)
	}
}

func (s Service) Priority() Priority { return s.priority }

// PremiumFee is the fee of a premium Order; false for a standard one.
func (s Service) PremiumFee() (vo.Money, bool) { return s.premiumFee, s.hasFee }

func (s Service) validate() error {
	switch s.priority {
	case PriorityStandard:
		if s.hasFee {
			return ErrInvalidPremiumFee
		}
	case PriorityPremium:
		if !s.hasFee || !s.premiumFee.Amount().IsPositive() {
			return ErrInvalidPremiumFee
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnknownPriority, s.priority)
	}
	return nil
}
