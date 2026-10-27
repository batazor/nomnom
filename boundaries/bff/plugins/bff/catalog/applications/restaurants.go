// Package applications holds the catalog scenarios of the customer UI: the
// list of restaurants and one restaurant with its menu. They read public data
// of restaurants, so no customer is involved; Protobuf and GraphQL types do not
// reach it (BFF ADR 2).
package applications

import "errors"

var (
	// ErrRestaurantNotFound is a restaurant unknown to restaurants.
	ErrRestaurantNotFound = errors.New("restaurant not found")
	// ErrMenuNotFound is a restaurant that has not published a menu.
	ErrMenuNotFound = errors.New("menu not found")
	// ErrInvalidPageSize is a page size outside 1..MaxPageSize.
	ErrInvalidPageSize = errors.New("page size must be between 1 and 100")
)

// MaxPageSize is the largest page restaurants serves.
const MaxPageSize = 100

// Money is an exact amount: a decimal string with a dot separator.
type Money struct {
	Amount   string
	Currency string
}

// Restaurant is the public restaurant data of restaurants.
type Restaurant struct {
	ID       string
	Name     string
	IsOpen   bool
	WaitTime WaitTime
}

// WaitTime is the wait for an order placed now (restaurants FR-645): an
// estimate, not a promise.
type WaitTime struct {
	// Elevated is a kitchen slower than usual for this restaurant.
	Elevated            bool
	ExpectedWaitMinutes int
}

type MenuItem struct {
	ID string
	// Description is empty when the item has none.
	Description string
	Name        string
	Price       Money
	Available   bool
}
