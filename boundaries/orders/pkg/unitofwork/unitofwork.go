// Package unitofwork contains the local transaction ports of the orders boundary
// and the Inbox protocol that runs inside them. It holds no business rules.
package unitofwork

import (
	"context"
	"fmt"
)

// UnitOfWork is the local transaction boundary of a use case. A nested Do joins
// the active transaction; only the outermost scope commits or rolls back.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Inbox records a processed integration event inside the active transaction.
// Accept returns false when the consumer has already processed eventID.
type Inbox interface {
	Accept(ctx context.Context, consumer, eventID string) (bool, error)
}

// AcceptOnce runs the insert-first Inbox protocol of an integration event:
//
//	UoW { Inbox.Accept → fn } → commit
//
// fn runs in the transaction only for an event the consumer has not processed
// yet; its error rolls the Inbox record back, so redelivery repeats the event.
// accepted is false for a duplicate, which changes nothing.
func AcceptOnce(ctx context.Context, uow UnitOfWork, inbox Inbox, consumer, eventID string, fn func(ctx context.Context) error) (accepted bool, err error) {
	err = uow.Do(ctx, func(ctx context.Context) error {
		ok, err := inbox.Accept(ctx, consumer, eventID)
		if err != nil {
			return fmt.Errorf("accept inbox event: %w", err)
		}
		if !ok {
			return nil
		}
		if err := fn(ctx); err != nil {
			return err
		}
		accepted = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return accepted, nil
}
