package order

import (
	"context"
	"errors"
)

var (
	ErrOrderNotFound      = errors.New("order not found")
	ErrOrderAlreadyExists = errors.New("order already exists")
)

// Repository is the persistence contract of the Order aggregate. Add and Save
// participate in the active Unit of Work from ctx. Save succeeds only when the
// stored version equals the loaded Version(); after Add or Save the persisted
// version is Version()+1, which is also the aggregate_version of Outbox records.
type Repository interface {
	Get(ctx context.Context, id ID) (*Order, error)
	Add(ctx context.Context, order *Order) error
	Save(ctx context.Context, order *Order) error
}
