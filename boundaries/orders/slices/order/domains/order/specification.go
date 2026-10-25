package order

import (
	"time"

	"nomnom/orders/slices/order/domains/order/vo"
)

type PolicyReasonCode string

const (
	CancellationWindowNotOpen PolicyReasonCode = "cancellation_window_not_open"
	CancellationWindowExpired PolicyReasonCode = "cancellation_window_expired"
	OrderItemUnavailable      PolicyReasonCode = "order_item_unavailable"
	OrderTotalBelowMinimum    PolicyReasonCode = "order_total_below_minimum"
	OrderTotalAboveMaximum    PolicyReasonCode = "order_total_above_maximum"
)

type PolicyReason struct {
	Code       PolicyReasonCode
	MenuItemID MenuItemID
}

type SpecificationResult struct {
	Reasons []PolicyReason
}

func (r SpecificationResult) Satisfied() bool { return len(r.Reasons) == 0 }

type CancellationWindowContext struct {
	Now      time.Time
	OpensAt  time.Time
	ClosesAt time.Time
}

func WithinCancellationWindow(context CancellationWindowContext) SpecificationResult {
	if context.Now.Before(context.OpensAt) {
		return SpecificationResult{Reasons: []PolicyReason{{Code: CancellationWindowNotOpen}}}
	}
	if context.Now.After(context.ClosesAt) {
		return SpecificationResult{Reasons: []PolicyReason{{Code: CancellationWindowExpired}}}
	}
	return SpecificationResult{}
}

type ItemAvailability map[MenuItemID]bool

type OrderabilityContext struct {
	Availability ItemAvailability
	MinimumTotal vo.Money
	MaximumTotal vo.Money
}

func OrderItemsOrderable(order *Order, context OrderabilityContext) SpecificationResult {
	reasons := make([]PolicyReason, 0)
	for _, item := range order.Items() {
		if !context.Availability[item.MenuItemID()] {
			reasons = append(reasons, PolicyReason{
				Code:       OrderItemUnavailable,
				MenuItemID: item.MenuItemID(),
			})
		}
	}

	total := order.Total().Amount()
	if total.LessThan(context.MinimumTotal.Amount()) {
		reasons = append(reasons, PolicyReason{Code: OrderTotalBelowMinimum})
	}
	if total.GreaterThan(context.MaximumTotal.Amount()) {
		reasons = append(reasons, PolicyReason{Code: OrderTotalAboveMaximum})
	}
	return SpecificationResult{Reasons: reasons}
}
