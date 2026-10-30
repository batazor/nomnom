package messaging

import "errors"

// Class is how a consumer reacts to a handler error (ADR 28).
type Class string

const (
	// ClassTransient errors are nacked and retried with backoff up to a limit.
	ClassTransient Class = "transient"
	// ClassPermanent errors go to the poison topic and the message is acked.
	ClassPermanent Class = "permanent"
	// ClassBusiness errors are acked: the outcome is logged, not retried.
	ClassBusiness Class = "business"
	// ClassDeserialization marks records the GuardedUnmarshaler could not decode;
	// they go to the DLQ before any handler.
	ClassDeserialization Class = "deserialization"
)

var (
	errPermanent = errors.New("permanent")
	errBusiness  = errors.New("business")
)

type classified struct {
	class error
	err   error
}

func (c classified) Error() string   { return c.err.Error() }
func (c classified) Unwrap() []error { return []error{c.class, c.err} }

// Permanent marks an error that redelivery cannot fix: an undecodable message,
// an unknown event_type or an event the application rejects as invalid input.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return classified{class: errPermanent, err: err}
}

// Business marks an expected business refusal of the application handler.
func Business(err error) error {
	if err == nil {
		return nil
	}
	return classified{class: errBusiness, err: err}
}

// Classify returns the class of a handler error; unmarked errors are transient.
func Classify(err error) Class {
	switch {
	case errors.Is(err, errPermanent):
		return ClassPermanent
	case errors.Is(err, errBusiness):
		return ClassBusiness
	default:
		return ClassTransient
	}
}
