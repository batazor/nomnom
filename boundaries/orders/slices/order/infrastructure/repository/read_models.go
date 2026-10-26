package repository

import (
	"context"
	"fmt"

	"nomnom/orders/slices/order/domains/order"
)

// CountByState — число Orders в каждом состоянии для gauge nomnom.orders.in_state.
func (s *Store) CountByState(ctx context.Context) (map[order.State]int64, error) {
	rows, err := s.query(ctx, psql.Select("state", "count(*)").From("orders").GroupBy("state"))
	if err != nil {
		return nil, fmt.Errorf("count orders by state: %w", err)
	}
	defer rows.Close()
	counts := map[order.State]int64{}
	for rows.Next() {
		var state string
		var n int64
		if err := rows.Scan(&state, &n); err != nil {
			return nil, fmt.Errorf("count orders by state: %w", err)
		}
		counts[order.State(state)] = n
	}
	return counts, rows.Err()
}
