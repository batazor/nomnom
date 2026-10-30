package applications

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var start = Location{Latitude: 52.525, Longitude: 13.402}

func fixedID() (string, error) { return "courier-new", nil }

// AC-794: a new courier is registered under a fresh id, placed at the start
// point and the pool is read back.
func TestAddCourierRegistersAndPlacesIt(t *testing.T) {
	pool := Pool{Size: 1, OnLine: 1, Couriers: []Courier{{ID: "courier-new", Name: "Анна", Availability: AvailabilityAvailable}}}
	dispatch := NewMockDispatch(t)
	mock.InOrder(
		dispatch.EXPECT().Register(mock.Anything, "courier-new", "Анна").Return(nil).Once(),
		dispatch.EXPECT().Place(mock.Anything, "courier-new", start).Return(nil).Once(),
		dispatch.EXPECT().Pool(mock.Anything).Return(pool, nil).Once(),
	)

	result, err := NewChangePool(dispatch, fixedID, start).Handle(context.Background(), Request{Command: CommandAdd, Name: "Анна"})

	require.NoError(t, err)
	assert.Equal(t, Result{Pool: pool}, result)
}

func TestRefusedAddPlacesNobody(t *testing.T) {
	// Place is not expected: nobody is placed.
	dispatch := NewMockDispatch(t)
	dispatch.EXPECT().Register(mock.Anything, "courier-new", " ").Return(ErrInvalidCourierName).Once()
	dispatch.EXPECT().Pool(mock.Anything).Return(Pool{}, nil).Once()

	result, err := NewChangePool(dispatch, fixedID, start).Handle(context.Background(), Request{Command: CommandAdd, Name: " "})

	require.NoError(t, err)
	assert.Equal(t, ProblemCourierNameInvalid, result.Problem)
}

func TestLineCommandsAnswerProblemsAndErrors(t *testing.T) {
	for cause, problem := range map[error]Problem{
		ErrStatusConflict: ProblemCourierStatusConflict,
		ErrCourierOnLine:  ProblemCourierOnLine,
	} {
		dispatch := NewMockDispatch(t)
		dispatch.EXPECT().SetOnLine(mock.Anything, "courier-1", false).Return(cause).Once()
		dispatch.EXPECT().Pool(mock.Anything).Return(Pool{}, nil).Once()
		result, err := NewChangePool(dispatch, fixedID, start).Handle(context.Background(), Request{Command: CommandTakeOffLine, CourierID: "courier-1"})
		require.NoError(t, err)
		assert.Equal(t, problem, result.Problem)
	}

	denied := NewMockDispatch(t)
	denied.EXPECT().Retire(mock.Anything, "courier-1").Return(ErrNotPermitted).Once()
	_, err := NewChangePool(denied, fixedID, start).Handle(context.Background(), Request{Command: CommandRetire, CourierID: "courier-1"})
	assert.ErrorIs(t, err, ErrNotPermitted)

	broken := errors.New("id source down")
	_, err = NewChangePool(NewMockDispatch(t), func() (string, error) { return "", broken }, start).Handle(context.Background(), Request{Command: CommandAdd, Name: "Анна"})
	assert.ErrorIs(t, err, broken)
}

// A dispatcher offers an order to the courier they chose; a refusal of
// delivery is the problem of the answer.
func TestOfferGoesToTheChosenCourier(t *testing.T) {
	offered := Pool{Size: 1, OnLine: 1, Couriers: []Courier{{ID: "courier-1", Name: "Анна", Availability: AvailabilityAvailable, OfferedOrderID: "order-1"}}}
	dispatch := NewMockDispatch(t)
	mock.InOrder(
		dispatch.EXPECT().Offer(mock.Anything, "order-1", "courier-1").Return(nil).Once(),
		dispatch.EXPECT().Pool(mock.Anything).Return(offered, nil).Once(),
		dispatch.EXPECT().Offer(mock.Anything, "order-2", "courier-1").Return(ErrOfferRefused).Once(),
		dispatch.EXPECT().Pool(mock.Anything).Return(offered, nil).Once(),
	)
	handler := NewChangePool(dispatch, fixedID, start)

	result, err := handler.Handle(context.Background(), Request{Command: CommandOffer, OrderID: "order-1", CourierID: "courier-1"})
	require.NoError(t, err)
	assert.Equal(t, Result{Pool: offered}, result)

	result, err = handler.Handle(context.Background(), Request{Command: CommandOffer, OrderID: "order-2", CourierID: "courier-1"})
	require.NoError(t, err)
	assert.Equal(t, ProblemOfferRefused, result.Problem)
}

func TestStartPointIsValidated(t *testing.T) {
	_, err := NewLocation(52.5, 13.4)
	require.NoError(t, err)
	for _, point := range [][2]float64{{91, 0}, {-91, 0}, {0, 181}, {0, -181}} {
		_, err := NewLocation(point[0], point[1])
		assert.Error(t, err, "%v", point)
	}
}
