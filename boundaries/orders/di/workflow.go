package di

import (
	"fmt"

	"go.temporal.io/sdk/client"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
	orderdi "nomnom/orders/slices/order/di"
)

// NewTemporalClient — lazy Temporal client API: соединение открывается при
// первом вызове, поэтому старт и readiness API не зависят от Temporal. Пока
// Temporal недоступен, Place Order сохраняет заказ с fulfillment_started=false,
// а Request Order Cancellation отвечает UNAVAILABLE.
// Tracing interceptor продолжает trace Place Order в start и signal workflow;
// logger — JSON logger процесса.
func NewTemporalClient(cfg config.TemporalConfig) (client.Client, func(), error) {
	options, err := telemetry.TemporalClientOptions(client.Options{HostPort: cfg.HostPort, Namespace: cfg.Namespace})
	if err != nil {
		return nil, nil, err
	}
	c, err := client.NewLazyClient(options)
	if err != nil {
		return nil, nil, fmt.Errorf("temporal client %s: %w", cfg.HostPort, err)
	}
	return c, c.Close, nil
}

// NewFulfillmentWorkflow — порт Order Fulfillment Workflow command use cases.
func NewFulfillmentWorkflow(c client.Client, cfg config.FulfillmentConfig) orderdi.Workflow {
	return fulfillmenttemporal.NewClient(c, NewFulfillmentPolicy(cfg))
}
