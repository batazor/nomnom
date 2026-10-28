package applications

import (
	"context"
	"fmt"
	"time"
)

// Slot is a delivery window of scheduled orders of a restaurant: [Start, End),
// Available is false when the window is full.
type Slot struct {
	Start     time.Time
	End       time.Time
	Available bool
}

// SlotReader reads the delivery windows of scheduled orders of restaurants.
type SlotReader interface {
	// ScheduledSlots lists the windows by start; empty for a restaurant that
	// takes no scheduled orders or does not accept orders now.
	ScheduledSlots(ctx context.Context, restaurantID string) ([]Slot, error)
}

// ListScheduledSlots reads the delivery windows of the restaurants a
// GraphQL response asks Restaurant.scheduledSlots for. restaurants has no
// batch read of windows, so each restaurant is one call; the catalog list does
// not ask for windows, the restaurant screen asks for one restaurant.
type ListScheduledSlots struct {
	slots SlotReader
}

func NewListScheduledSlots(slots SlotReader) ListScheduledSlots {
	return ListScheduledSlots{slots: slots}
}

// Handle returns the windows of each restaurant in the order of restaurantIDs.
func (h ListScheduledSlots) Handle(ctx context.Context, restaurantIDs []string) ([][]Slot, error) {
	result := make([][]Slot, 0, len(restaurantIDs))
	for _, restaurantID := range restaurantIDs {
		slots, err := h.slots.ScheduledSlots(ctx, restaurantID)
		if err != nil {
			return nil, fmt.Errorf("read scheduled slots of %s: %w", restaurantID, err)
		}
		result = append(result, slots)
	}
	return result, nil
}
