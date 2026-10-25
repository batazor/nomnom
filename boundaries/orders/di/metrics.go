package di

import (
	"nomnom/orders/slices/fulfillment/domains/fulfillment"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// NewReasonCodes передаёт метрикам order закрытый набор reason codes процесса
// fulfillment: composition root знает оба слайса, слайс order — нет.
func NewReasonCodes() observability.ReasonCodes {
	return observability.ReasonCodes(fulfillment.ReasonCodes())
}
