package applications

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrCookNotFound is a cook unknown to the restaurant.
	ErrCookNotFound = errors.New("cook not found")
	// ErrInvalidCookName is a name restaurants rejects: empty or longer than
	// 64 characters.
	ErrInvalidCookName = errors.New("cook name is invalid")
	// ErrCookBusy is a cook who holds an order and cannot leave the line.
	ErrCookBusy = errors.New("cook holds an order")
	// ErrCookOnLine is a cook on line who cannot leave the pool.
	ErrCookOnLine = errors.New("cook is on line")
	// ErrCookRetired is a cook who left the pool and cannot return.
	ErrCookRetired = errors.New("cook is retired")
	// ErrNoCookAvailable is a start of preparation without a free cook on line.
	ErrNoCookAvailable = errors.New("no cook on line is free")
)

// Cook is a cook of the Cook Pool of a restaurant.
type Cook struct {
	ID     string
	Name   string
	OnLine bool
	// CurrentKitchenOrderID is the order the cook holds; empty when free.
	CurrentKitchenOrderID string
}

// CookPool is the line and the load of the kitchen of one restaurant.
type CookPool struct {
	Size   int
	OnLine int
	Busy   int
	Queued int
	// Cooks are by name.
	Cooks []Cook
}

// CookPoolReader reads the Cook Pool of restaurants on behalf of the user in
// the context; ErrKitchenNotFound for a user who may not see the kitchen.
type CookPoolReader interface {
	CookPool(ctx context.Context, restaurantID string) (CookPool, error)
}

// CookCommands runs the Cook Pool commands of restaurants on behalf of the
// user in the context. Every method returns ErrNotPermitted and
// ErrCookNotFound for the refusals of restaurants; Hire also
// ErrInvalidCookName, SetOnLine ErrCookBusy (off line) and ErrCookRetired
// (on line), Retire ErrCookOnLine.
type CookCommands interface {
	Hire(ctx context.Context, restaurantID, name string) error
	SetOnLine(ctx context.Context, restaurantID, cookID string, onLine bool) error
	Retire(ctx context.Context, restaurantID, cookID string) error
}

// CookCommand is a Cook Pool command of the kitchen screen.
type CookCommand string

const (
	CookCommandHire        CookCommand = "hire"
	CookCommandPutOnLine   CookCommand = "put_on_line"
	CookCommandTakeOffLine CookCommand = "take_off_line"
	CookCommandRetire      CookCommand = "retire"
)

// CookPoolProblem is a business reason why a Cook Pool command cannot be
// applied.
type CookPoolProblem string

const (
	ProblemCookNameInvalid CookPoolProblem = "cook_name_invalid"
	ProblemCookBusy        CookPoolProblem = "cook_busy"
	ProblemCookOnLine      CookPoolProblem = "cook_on_line"
	ProblemCookRetired     CookPoolProblem = "cook_retired"
)

// CookPoolRequest is one Cook Pool command: Name for hire, CookID for the
// others.
type CookPoolRequest struct {
	RestaurantID string
	Command      CookCommand
	Name         string
	CookID       string
}

// CookPoolResult is the pool after the command and the problem, empty on
// success.
type CookPoolResult struct {
	Pool    CookPool
	Problem CookPoolProblem
}

var cookProblems = map[error]CookPoolProblem{
	ErrInvalidCookName: ProblemCookNameInvalid,
	ErrCookBusy:        ProblemCookBusy,
	ErrCookOnLine:      ProblemCookOnLine,
	ErrCookRetired:     ProblemCookRetired,
}

// ChangeCookPool runs a Cook Pool command and reads the pool back.
type ChangeCookPool struct {
	commands CookCommands
	pools    CookPoolReader
}

func NewChangeCookPool(commands CookCommands, pools CookPoolReader) ChangeCookPool {
	return ChangeCookPool{commands: commands, pools: pools}
}

// Handle answers the business refusals as problems; ErrNotPermitted and
// ErrCookNotFound stay errors.
func (h ChangeCookPool) Handle(ctx context.Context, request CookPoolRequest) (CookPoolResult, error) {
	var err error
	switch request.Command {
	case CookCommandHire:
		err = h.commands.Hire(ctx, request.RestaurantID, request.Name)
	case CookCommandPutOnLine:
		err = h.commands.SetOnLine(ctx, request.RestaurantID, request.CookID, true)
	case CookCommandTakeOffLine:
		err = h.commands.SetOnLine(ctx, request.RestaurantID, request.CookID, false)
	case CookCommandRetire:
		err = h.commands.Retire(ctx, request.RestaurantID, request.CookID)
	default:
		return CookPoolResult{}, fmt.Errorf("unknown cook command %q", request.Command)
	}
	var problem CookPoolProblem
	for cause, code := range cookProblems {
		if errors.Is(err, cause) {
			problem, err = code, nil
		}
	}
	if err != nil {
		return CookPoolResult{}, err
	}
	pool, err := h.pools.CookPool(ctx, request.RestaurantID)
	if err != nil {
		return CookPoolResult{}, fmt.Errorf("read the cook pool back: %w", err)
	}
	return CookPoolResult{Pool: pool, Problem: problem}, nil
}

// cooksByID indexes the pool for the cook of each kitchen order.
func cooksByID(pool CookPool) map[string]Cook {
	cooks := make(map[string]Cook, len(pool.Cooks))
	for _, cook := range pool.Cooks {
		cooks[cook.ID] = cook
	}
	return cooks
}
