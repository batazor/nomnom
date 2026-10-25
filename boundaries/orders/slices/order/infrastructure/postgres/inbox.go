package postgres

import (
	"context"
	"fmt"
)

// Accept records (consumer, event_id) in the active transaction; false means
// the event was already processed (ADR 11).
func (s *Store) Accept(ctx context.Context, consumer, eventID string) (bool, error) {
	tag, err := s.q(ctx).Exec(ctx, `
		INSERT INTO inbox (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, consumer, eventID)
	if err != nil {
		return false, fmt.Errorf("accept inbox %s/%s: %w", consumer, eventID, err)
	}
	return tag.RowsAffected() == 1, nil
}
