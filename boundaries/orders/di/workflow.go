package di

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/sdk/client"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/applications/fulfillment"
	orderdi "nomnom/orders/slices/order/di"
)

// NewTemporalClient — lazy Temporal client API: соединение открывается при
// первом вызове, поэтому старт и readiness API не зависят от Temporal. Пока
// Temporal недоступен, Request Order Cancellation отвечает UNAVAILABLE, а
// consumer откладывает старт и сигналы до повторной доставки.
// Tracing interceptor продолжает trace use case в start и signal workflow;
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
	return newFulfillmentPort(c, cfg)
}

var signalByKind = map[fulfillment.Kind]string{
	fulfillment.KindRestaurantAccepted: fulfillmenttemporal.SignalRestaurantOrderAccepted,
	fulfillment.KindRestaurantRejected: fulfillmenttemporal.SignalRestaurantOrderRejected,
	fulfillment.KindPreparationStarted: fulfillmenttemporal.SignalPreparationStarted,
	fulfillment.KindPreparationDelayed: fulfillmenttemporal.SignalPreparationDelayed,
	fulfillment.KindPreparationFailed:  fulfillmenttemporal.SignalPreparationFailed,
	fulfillment.KindReadyForPickup:     fulfillmenttemporal.SignalRestaurantOrderReady,
	fulfillment.KindCourierAssigned:    fulfillmenttemporal.SignalCourierAssignedToDelivery,
	fulfillment.KindPickedUp:           fulfillmenttemporal.SignalDeliveryPickedUp,
	fulfillment.KindDeliveryCompleted:  fulfillmenttemporal.SignalDeliveryCompleted,
	fulfillment.KindDeliveryFailed:     fulfillmenttemporal.SignalDeliveryFailed,
}

// fulfillmentPort реализует порт fulfillment слайса order на Temporal-клиенте
// слайса fulfillment: словарь порта (Kind, Notification) переводится здесь, а
// слайсы друг друга не импортируют.
type fulfillmentPort struct {
	client *fulfillmenttemporal.Client
}

var (
	_ fulfillment.Starter               = fulfillmentPort{}
	_ fulfillment.Notifier              = fulfillmentPort{}
	_ fulfillment.CancellationRequester = fulfillmentPort{}
)

func newFulfillmentPort(c client.Client, cfg config.FulfillmentConfig) fulfillmentPort {
	return fulfillmentPort{client: fulfillmenttemporal.NewClient(c, NewFulfillmentPolicy(cfg))}
}

func (p fulfillmentPort) Start(ctx context.Context, request fulfillment.StartRequest) error {
	start := fulfillmenttemporal.StartRequest{
		OrderID:      string(request.OrderID),
		RestaurantID: string(request.RestaurantID),
		EventID:      request.EventID,
		OccurredAt:   request.OccurredAt,
	}
	if !request.DeliveryWindow.IsZero() {
		start.DeliveryWindow = &fulfillmenttemporal.DeliveryWindow{Start: request.DeliveryWindow.Start(), End: request.DeliveryWindow.End()}
	}
	return p.client.Start(ctx, start)
}

func (p fulfillmentPort) Notify(ctx context.Context, notification fulfillment.Notification) error {
	signal, ok := signalByKind[notification.Kind]
	if !ok {
		return fmt.Errorf("no signal for fulfillment kind %q", notification.Kind)
	}
	return workflowError(p.client.Signal(ctx, string(notification.OrderID), signal, fulfillmenttemporal.SignalPayload{
		EventID:         notification.EventID,
		OccurredAt:      notification.OccurredAt,
		Reason:          notification.Reason,
		ExpectedReadyAt: notification.ExpectedReadyAt,
	}))
}

func (p fulfillmentPort) RequestCancellation(ctx context.Context, request fulfillment.CancellationRequest) error {
	return workflowError(p.client.RequestCancellation(ctx, string(request.OrderID), fulfillmenttemporal.CancellationRequest{
		RequestID:   request.RequestID,
		RequestedBy: request.RequestedBy.String(),
		RequestedAt: request.RequestedAt,
		Reason:      request.Reason,
	}))
}

// workflowError переводит ошибки Temporal-клиента в ошибки порта.
func workflowError(err error) error {
	switch {
	case errors.Is(err, fulfillmenttemporal.ErrWorkflowNotFound):
		return fmt.Errorf("%w: %w", fulfillment.ErrWorkflowNotFound, err)
	case errors.Is(err, fulfillmenttemporal.ErrCancellationRefused):
		return fmt.Errorf("%w: %w", fulfillment.ErrCancellationRefused, err)
	default:
		return err
	}
}
