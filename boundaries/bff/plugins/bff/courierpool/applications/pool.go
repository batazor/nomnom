// Package applications holds the scenarios of the Courier Pool on the flow
// board: the line and load of the couriers of the platform and the commands
// of a dispatcher over them. delivery authorizes platform#manage_couriers;
// BFF only forwards the verified user. Protobuf and GraphQL types do not
// reach it (BFF ADR 2).
package applications

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// PoolID is the key of the Courier Pool entity: the platform singleton.
const PoolID = "nomnom"

var (
	// ErrNotPermitted is a user who may not manage the Courier Pool.
	ErrNotPermitted = errors.New("courier pool is not permitted")
	// ErrCourierNotFound is a courier unknown to delivery.
	ErrCourierNotFound = errors.New("courier not found")
	// ErrInvalidCourierName is a name delivery rejects: empty or longer than
	// 64 characters.
	ErrInvalidCourierName = errors.New("courier name is invalid")
	// ErrStatusConflict is a line change the courier status forbids: a busy
	// courier stays on line, a retired one off line.
	ErrStatusConflict = errors.New("courier status forbids the line change")
	// ErrCourierOnLine is a courier on line who cannot leave the pool.
	ErrCourierOnLine = errors.New("courier is on line")
)

// Availability of a courier for new deliveries.
type Availability string

const (
	AvailabilityAvailable   Availability = "available"
	AvailabilityBusy        Availability = "busy"
	AvailabilityUnavailable Availability = "unavailable"
)

// Courier is a courier of the pool. OfferedOrderID and CurrentOrderID are
// empty without a pending offer or an active delivery.
type Courier struct {
	ID             string
	Name           string
	Availability   Availability
	OfferedOrderID string
	CurrentOrderID string
}

// Pool is the line and the load of the couriers of the platform.
type Pool struct {
	Size    int
	OnLine  int
	Busy    int
	Waiting int
	// Couriers are by name.
	Couriers []Courier
}

// Location is a point couriers stand at.
type Location struct {
	Latitude  float64
	Longitude float64
}

// NewLocation validates a point: finite, latitude within [-90, 90] and
// longitude within [-180, 180], the ranges delivery accepts.
func NewLocation(latitude, longitude float64) (Location, error) {
	if math.IsNaN(latitude) || math.Abs(latitude) > 90 {
		return Location{}, fmt.Errorf("latitude %v is outside [-90, 90]", latitude)
	}
	if math.IsNaN(longitude) || math.Abs(longitude) > 180 {
		return Location{}, fmt.Errorf("longitude %v is outside [-180, 180]", longitude)
	}
	return Location{Latitude: latitude, Longitude: longitude}, nil
}

// Dispatch runs the Courier Pool RPCs of delivery on behalf of the user in
// the context. Every method returns ErrNotPermitted for a user without
// platform#manage_couriers and ErrCourierNotFound for an unknown courier.
type Dispatch interface {
	Pool(ctx context.Context) (Pool, error)
	// Register also returns ErrInvalidCourierName.
	Register(ctx context.Context, courierID, name string) error
	Place(ctx context.Context, courierID string, location Location) error
	// SetOnLine also returns ErrStatusConflict.
	SetOnLine(ctx context.Context, courierID string, onLine bool) error
	// Retire also returns ErrCourierOnLine.
	Retire(ctx context.Context, courierID string) error
}

// GetPool reads the Courier Pool.
type GetPool struct {
	dispatch Dispatch
}

func NewGetPool(dispatch Dispatch) GetPool {
	return GetPool{dispatch: dispatch}
}

// Handle returns ErrNotPermitted for a user who may not manage couriers.
func (h GetPool) Handle(ctx context.Context) (Pool, error) {
	return h.dispatch.Pool(ctx)
}
