package repository

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
)

// Relation is an Order relation in SpiceDB whose subject is a column of the
// Order row: the business association the reconciliation compares with.
type Relation string

const (
	RelationCustomer   Relation = "customer"
	RelationRestaurant Relation = "restaurant"
	RelationDelivery   Relation = "delivery"
)

// relationColumns is the closed set of columns a relation may read; it keeps
// query text out of reach of the caller.
var relationColumns = map[Relation]string{
	RelationCustomer:   "customer_id",
	RelationRestaurant: "restaurant_id",
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
// has one; an Order without the subject (no Delivery yet) is absent.
func (s *Store) SubjectsOf(ctx context.Context, relation Relation, ids []string) (map[string]string, error) {
	column, err := relation.column()
	if err != nil {
		return nil, err
	}
	rows, err := s.query(ctx, psql.Select("id", column).From("orders").
		Where("id = ANY(?)", ids).Where(sq.NotEq{column: nil}))
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
	rows, err := s.query(ctx, psql.Select("id", column).From("orders").
		Where(sq.Gt{"id": afterID}).Where(sq.NotEq{column: nil}).OrderBy("id").Suffix("LIMIT ?", limit))
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

// RestoreRelation re-reads the subject of relation under a FOR SHARE row lock
// and, only if the committed Order still names subject, runs restore and
// stores its revision in the same transaction. Record Courier Assignment and
// Record Courier Release hold FOR UPDATE on the row from before their SpiceDB
// write until commit, so the re-read waits for a writer in flight and a later
// writer waits for the restore: a relationship such a writer just deleted is
// never restored for the subject it is replacing. false — the Order is gone or
// names another subject; nothing is written.
func (s *Store) RestoreRelation(
	ctx context.Context, relation Relation, orderID, subject string,
	restore func(context.Context) (application.AuthorizationRevision, error),
) (bool, error) {
	column, err := relation.column()
	if err != nil {
		return false, err
	}
	restored := false
	err = s.Do(ctx, func(ctx context.Context) error {
		var current *string
		err := s.queryRow(ctx, psql.Select(column).From("orders").Where(sq.Eq{"id": orderID}).Suffix("FOR SHARE"), &current)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (current == nil || *current != subject)) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("re-read order %s %s: %w", orderID, relation, err)
		}
		revision, err := restore(ctx)
		if err != nil {
			return err
		}
		if err := s.RememberRevision(ctx, order.ID(orderID), revision); err != nil {
			return err
		}
		restored = true
		return nil
	})
	return restored, err
}
