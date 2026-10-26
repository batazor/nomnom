package applications

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDispatch struct {
	pool  Pool
	calls []string
	err   error
}

func (f *fakeDispatch) Pool(context.Context) (Pool, error) { return f.pool, nil }

func (f *fakeDispatch) Register(_ context.Context, courierID, name string) error {
	f.calls = append(f.calls, "register "+courierID+" "+name)
	return f.err
}

func (f *fakeDispatch) Place(_ context.Context, courierID string, location Location) error {
	f.calls = append(f.calls, "place "+courierID)
	return nil
}

func (f *fakeDispatch) SetOnLine(_ context.Context, courierID string, onLine bool) error {
	if onLine {
		f.calls = append(f.calls, "on line "+courierID)
	} else {
		f.calls = append(f.calls, "off line "+courierID)
	}
	return f.err
}

func (f *fakeDispatch) Retire(_ context.Context, courierID string) error {
	f.calls = append(f.calls, "retire "+courierID)
	return f.err
}

var start = Location{Latitude: 52.525, Longitude: 13.402}

func fixedID() (string, error) { return "courier-new", nil }

// AC-794: a new courier is registered under a fresh id, placed at the start
// point and the pool is read back.
func TestAddCourierRegistersAndPlacesIt(t *testing.T) {
	pool := Pool{Size: 1, OnLine: 1, Couriers: []Courier{{ID: "courier-new", Name: "Анна", Availability: AvailabilityAvailable}}}
	dispatch := &fakeDispatch{pool: pool}

	result, err := NewChangePool(dispatch, fixedID, start).Handle(context.Background(), Request{Command: CommandAdd, Name: "Анна"})

	require.NoError(t, err)
	assert.Equal(t, Result{Pool: pool}, result)
	assert.Equal(t, []string{"register courier-new Анна", "place courier-new"}, dispatch.calls)
}

func TestRefusedAddPlacesNobody(t *testing.T) {
	dispatch := &fakeDispatch{err: ErrInvalidCourierName}

	result, err := NewChangePool(dispatch, fixedID, start).Handle(context.Background(), Request{Command: CommandAdd, Name: " "})

	require.NoError(t, err)
	assert.Equal(t, ProblemCourierNameInvalid, result.Problem)
	assert.Equal(t, []string{"register courier-new  "}, dispatch.calls)
}

func TestLineCommandsAnswerProblemsAndErrors(t *testing.T) {
	for cause, problem := range map[error]Problem{
		ErrStatusConflict: ProblemCourierStatusConflict,
		ErrCourierOnLine:  ProblemCourierOnLine,
	} {
		dispatch := &fakeDispatch{err: cause}
		result, err := NewChangePool(dispatch, fixedID, start).Handle(context.Background(), Request{Command: CommandTakeOffLine, CourierID: "courier-1"})
		require.NoError(t, err)
		assert.Equal(t, problem, result.Problem)
	}

	denied := &fakeDispatch{err: ErrNotPermitted}
	_, err := NewChangePool(denied, fixedID, start).Handle(context.Background(), Request{Command: CommandRetire, CourierID: "courier-1"})
	assert.ErrorIs(t, err, ErrNotPermitted)

	broken := errors.New("id source down")
	_, err = NewChangePool(&fakeDispatch{}, func() (string, error) { return "", broken }, start).Handle(context.Background(), Request{Command: CommandAdd, Name: "Анна"})
	assert.ErrorIs(t, err, broken)
}

func TestStartPointIsValidated(t *testing.T) {
	_, err := NewLocation(52.5, 13.4)
	require.NoError(t, err)
	for _, point := range [][2]float64{{91, 0}, {-91, 0}, {0, 181}, {0, -181}} {
		_, err := NewLocation(point[0], point[1])
		assert.Error(t, err, "%v", point)
	}
}
