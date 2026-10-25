// Package system provides the process implementations of application.Clock and
// placeorder.IDGenerator.
package system

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"nomnom/orders/slices/order/domains/order"
)

// Clock reads the wall clock in UTC.
type Clock struct{}

func (Clock) Now() time.Time { return time.Now().UTC() }

// OrderIDs generates UUIDv7 order identifiers: time-ordered, so new Orders
// stay close in the primary key index, and valid SpiceDB object ids.
type OrderIDs struct{}

func (OrderIDs) NewOrderID(context.Context) (order.ID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate uuidv7: %w", err)
	}
	return order.ID(id.String()), nil
}
