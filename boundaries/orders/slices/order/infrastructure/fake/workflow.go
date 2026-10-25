package fake

import (
	"context"
	"sync"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Workflow records every call to the fulfillment port. Orders marked closed
// behave like a finished workflow.
type Workflow struct {
	mu            sync.Mutex
	starts        []fulfillment.StartRequest
	notifications []fulfillment.Notification
	cancellations []fulfillment.CancellationRequest
	closed        map[order.ID]struct{}
	startError    error
	notifyError   error
	cancelError   error
}

func NewWorkflow() *Workflow {
	return &Workflow{closed: make(map[order.ID]struct{})}
}

func (w *Workflow) Close(orderID order.ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed[orderID] = struct{}{}
}

func (w *Workflow) FailStart(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startError = err
}

func (w *Workflow) FailNotify(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.notifyError = err
}

func (w *Workflow) FailCancellation(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancelError = err
}

func (w *Workflow) Start(_ context.Context, request fulfillment.StartRequest) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.startError != nil {
		return w.startError
	}
	w.starts = append(w.starts, request)
	return nil
}

func (w *Workflow) Notify(_ context.Context, notification fulfillment.Notification) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.notifyError != nil {
		return w.notifyError
	}
	if _, closed := w.closed[notification.OrderID]; closed {
		return fulfillment.ErrWorkflowNotFound
	}
	w.notifications = append(w.notifications, notification)
	return nil
}

func (w *Workflow) RequestCancellation(_ context.Context, request fulfillment.CancellationRequest) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancelError != nil {
		return w.cancelError
	}
	if _, closed := w.closed[request.OrderID]; closed {
		return fulfillment.ErrWorkflowNotFound
	}
	w.cancellations = append(w.cancellations, request)
	return nil
}

func (w *Workflow) Starts() []fulfillment.StartRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]fulfillment.StartRequest(nil), w.starts...)
}

func (w *Workflow) Notifications() []fulfillment.Notification {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]fulfillment.Notification(nil), w.notifications...)
}

func (w *Workflow) Cancellations() []fulfillment.CancellationRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]fulfillment.CancellationRequest(nil), w.cancellations...)
}
