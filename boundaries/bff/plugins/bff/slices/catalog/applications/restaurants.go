// Package applications holds the catalog scenarios of the customer UI: the
// list of restaurants and one restaurant with its menu. They read public data
// of restaurants, so no customer is involved; Protobuf and GraphQL types do not
// reach it (BFF ADR 2).
package applications

import (
	"errors"
	"time"
)

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
	ID   string
	Name string
	// AcceptingOrders is opened by the owner and inside the opening hours at
	// the time of the read (restaurants FR-699).
	AcceptingOrders bool
	WaitTime        WaitTime
	// PremiumFee is nil when the restaurant offers no Premium Priority.
	PremiumFee *Money
	// TimeZone is the IANA time zone of the restaurant.
	TimeZone string
	// OpeningHours is empty when no schedule is set.
	OpeningHours []OpeningInterval
	// NextOpeningAt is set only for a restaurant opened by the owner and
	// outside its opening hours.
	NextOpeningAt *time.Time
}

// OpeningInterval is one interval of the weekly opening hours on the wall
// clock of the restaurant: Day is ISO (1 Monday … 7 Sunday), Opens and Closes
// are "HH:MM"; Closes not after Opens closes on the next day.
type OpeningInterval struct {
	Day    int
	Opens  string
	Closes string
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
