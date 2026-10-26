package applications

import (
	"context"
	"errors"
	"fmt"
)

// Command is a Courier Pool command of the flow board.
type Command string

const (
	CommandAdd         Command = "add"
	CommandPutOnLine   Command = "put_on_line"
	CommandTakeOffLine Command = "take_off_line"
	CommandRetire      Command = "retire"
)

// Problem is a business reason why a Courier Pool command cannot be applied.
type Problem string

const (
	ProblemCourierNameInvalid    Problem = "courier_name_invalid"
	ProblemCourierStatusConflict Problem = "courier_status_conflict"
	ProblemCourierOnLine         Problem = "courier_on_line"
)

// Request is one Courier Pool command: Name for add, CourierID for the
// others.
type Request struct {
	Command   Command
	Name      string
	CourierID string
}

// Result is the pool after the command and the problem, empty on success.
type Result struct {
	Pool    Pool
	Problem Problem
}

var problems = map[error]Problem{
	ErrInvalidCourierName: ProblemCourierNameInvalid,
	ErrStatusConflict:     ProblemCourierStatusConflict,
	ErrCourierOnLine:      ProblemCourierOnLine,
}

// ChangePool runs a Courier Pool command and reads the pool back. A new
// courier is registered under a fresh id — no user stands behind it, the
// Simulator acts for it — and placed at the start point of new couriers, so
// it is on line and gets offers.
type ChangePool struct {
	dispatch Dispatch
	newID    func() (string, error)
	start    Location
}

func NewChangePool(dispatch Dispatch, newID func() (string, error), start Location) ChangePool {
	return ChangePool{dispatch: dispatch, newID: newID, start: start}
}

// Handle answers the business refusals as problems; ErrNotPermitted and
// ErrCourierNotFound stay errors.
func (h ChangePool) Handle(ctx context.Context, request Request) (Result, error) {
	var err error
	switch request.Command {
	case CommandAdd:
		err = h.add(ctx, request.Name)
	case CommandPutOnLine:
		err = h.dispatch.SetOnLine(ctx, request.CourierID, true)
	case CommandTakeOffLine:
		err = h.dispatch.SetOnLine(ctx, request.CourierID, false)
	case CommandRetire:
		err = h.dispatch.Retire(ctx, request.CourierID)
	default:
		return Result{}, fmt.Errorf("unknown courier pool command %q", request.Command)
	}
	var problem Problem
	for cause, code := range problems {
		if errors.Is(err, cause) {
			problem, err = code, nil
		}
	}
	if err != nil {
		return Result{}, err
	}
	pool, err := h.dispatch.Pool(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("read the courier pool back: %w", err)
	}
	return Result{Pool: pool, Problem: problem}, nil
}

func (h ChangePool) add(ctx context.Context, name string) error {
	courierID, err := h.newID()
	if err != nil {
		return fmt.Errorf("courier id: %w", err)
	}
	if err := h.dispatch.Register(ctx, courierID, name); err != nil {
		return err
	}
	return h.dispatch.Place(ctx, courierID, h.start)
}
