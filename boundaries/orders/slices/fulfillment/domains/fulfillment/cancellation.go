package fulfillment

import (
	"errors"
	"fmt"
)

var (
	// ErrRequestIDRequired: a Cancellation Request without id cannot be deduplicated.
	ErrRequestIDRequired = errors.New("request_id is required")
	// ErrCancellationRefused: the outcome is chosen or the courier picked up the Order.
	ErrCancellationRefused = errors.New("cancellation refused")
)

// CancellationRequest is a Cancellation Request of the Order.
type CancellationRequest struct {
	RequestID   string
	RequestedBy string
	Reason      string
}

// AdmitCancellation checks a Cancellation Request without changing the
// process. A repeated accepted RequestID is admitted so retries succeed.
func (p *Process) AdmitCancellation(request CancellationRequest) error {
	switch {
	case request.RequestID == "":
		return ErrRequestIDRequired
	case p.cancellations[request.RequestID]:
		return nil
	case p.closing() || p.pickedUp:
		return fmt.Errorf("%w in stage %s", ErrCancellationRefused, p.Stage())
	}
	return nil
}

// AcceptCancellation cancels the Order for an admitted request and reports
// whether the request is new; a repeated RequestID changes nothing.
func (p *Process) AcceptCancellation(request CancellationRequest) bool {
	if p.cancellations[request.RequestID] {
		return false
	}
	p.cancellations[request.RequestID] = true
	p.fail(OutcomeCancellation, ReasonCustomerRequested)
	return true
}

// RequestCancellation admits and accepts a Cancellation Request.
func (p *Process) RequestCancellation(request CancellationRequest) error {
	if err := p.AdmitCancellation(request); err != nil {
		return err
	}
	p.AcceptCancellation(request)
	return nil
}
