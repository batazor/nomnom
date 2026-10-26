package repository

import (
	"context"
	"fmt"
)

// Accept records (consumer, event_id) in the active transaction; false means
// the event was already processed (ADR 11).
func (s *Store) Accept(ctx context.Context, consumer, eventID string) (bool, error) {
	tag, err := s.exec(ctx, psql.Insert("inbox").
		Columns("consumer", "event_id").Values(consumer, eventID).Suffix("ON CONFLICT DO NOTHING"))
	if err != nil {
		return false, fmt.Errorf("accept inbox %s/%s: %w", consumer, eventID, err)
	}
	return tag.RowsAffected() == 1, nil
}
