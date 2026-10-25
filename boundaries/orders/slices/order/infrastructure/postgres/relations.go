package postgres

import (
	"context"
	"fmt"
)

// Relation is an Order relation in SpiceDB whose subject is a column of the
// Order row: the business association the reconciliation compares with.
type Relation string

const (
	RelationCustomer   Relation = "customer"
	RelationRestaurant Relation = "restaurant"
	RelationCourier    Relation = "courier"
	RelationDelivery   Relation = "delivery"
)

// relationColumns is the closed set of columns a relation may read; it keeps
// query text out of reach of the caller.
var relationColumns = map[Relation]string{
	RelationCustomer:   "customer_id",
	RelationRestaurant: "restaurant_id",
	RelationCourier:    "courier_id",
	RelationDelivery:   "delivery_id",
}

func (r Relation) column() (string, error) {
	column, ok := relationColumns[r]
	if !ok {
		return "", fmt.Errorf("unknown order relation %q", r)
	}
	return column, nil
}

// RelationSubject is one Order and the subject id of its relation.
type RelationSubject struct {
	OrderID   string
	SubjectID string
}

// SubjectsOf returns the subject id of relation for each Order among ids that
// has one; an Order without the subject (no courier yet) is absent.
func (s *Store) SubjectsOf(ctx context.Context, relation Relation, ids []string) (map[string]string, error) {
	column, err := relation.column()
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).Query(ctx,
		`SELECT id, `+column+` FROM orders WHERE id = ANY($1) AND `+column+` IS NOT NULL`, ids)
	if err != nil {
		return nil, fmt.Errorf("read order %s subjects: %w", relation, err)
	}
	defer rows.Close()
	subjects := make(map[string]string, len(ids))
	for rows.Next() {
		var id, subject string
		if err := rows.Scan(&id, &subject); err != nil {
			return nil, fmt.Errorf("scan order %s subject: %w", relation, err)
		}
		subjects[id] = subject
	}
	return subjects, rows.Err()
}

// RelationPage pages Orders with a subject of relation by id after afterID.
func (s *Store) RelationPage(ctx context.Context, relation Relation, afterID string, limit int) ([]RelationSubject, error) {
	column, err := relation.column()
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).Query(ctx,
		`SELECT id, `+column+` FROM orders WHERE id > $1 AND `+column+` IS NOT NULL ORDER BY id LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("page order %s subjects: %w", relation, err)
	}
	defer rows.Close()
	var page []RelationSubject
	for rows.Next() {
		var row RelationSubject
		if err := rows.Scan(&row.OrderID, &row.SubjectID); err != nil {
			return nil, fmt.Errorf("scan order %s subject: %w", relation, err)
		}
		page = append(page, row)
	}
	return page, rows.Err()
}
