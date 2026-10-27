package applications

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var receivedAt = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func at(minutes int) time.Time { return receivedAt.Add(time.Duration(minutes) * time.Minute) }

var menuNames = map[string]string{"menu-pho": "Фо бо"}

// kitchenCatalog expects one read of the restaurant and of its menu names.
func kitchenCatalog(t *testing.T, restaurantID string) *MockCatalog {
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, restaurantID).Return(Restaurant{ID: restaurantID, Name: "Кухня №8", IsOpen: true}, nil).Once()
	catalog.EXPECT().MenuItemNames(mock.Anything, restaurantID).Return(menuNames, nil).Once()
	return catalog
}

// kitchenBoard expects one read of the board of restaurant-1.
func kitchenBoard(t *testing.T, active []ActiveOrder, queue []string, pool CookPool) *MockKitchenBoard {
	board := NewMockKitchenBoard(t)
	board.EXPECT().ActiveOrders(mock.Anything, "restaurant-1").Return(active, nil).Once()
	board.EXPECT().QueueOrder(mock.Anything, "restaurant-1").Return(queue, nil).Once()
	board.EXPECT().CookPool(mock.Anything, "restaurant-1").Return(pool, nil).Once()
	return board
}

// applying expects one kitchen command answered with err.
func applying(t *testing.T, request ActionRequest, err error) *MockKitchenCommands {
	commands := NewMockKitchenCommands(t)
	commands.EXPECT().Apply(mock.Anything, request.RestaurantID, request.KitchenOrderID, request.Action).Return(err).Once()
	return commands
}

func active(id string, stage Stage, minutes int) ActiveOrder {
	order := ActiveOrder{
		ID: id, OrderID: "order-" + id, Stage: stage,
		Lines:      []ActiveOrderLine{{MenuItemID: "menu-pho", Quantity: 2}, {MenuItemID: "menu-gone", Quantity: 1}},
		ReceivedAt: at(minutes),
	}
	if stage != StageReceived {
		accepted, ready := at(minutes+1), at(minutes+16)
		order.AcceptedAt, order.ExpectedReadyAt = &accepted, &ready
	}
	return order
}

func ids(orders []OrderView) []string {
	result := make([]string, 0, len(orders))
	for _, order := range orders {
		result = append(result, order.ID)
	}
	return result
}

// RECEIVED by receipt, then the Kitchen Queue order, then the rest by receipt.
func TestKitchenOrdersFollowTheBoardOrder(t *testing.T) {
	board := kitchenBoard(t, []ActiveOrder{
		active("ready", StageReady, 0),
		active("queued-late", StageQueued, 1),
		active("received-1", StageReceived, 2),
		active("preparing", StagePreparing, 3),
		active("queued-first", StageQueued, 4),
		active("queued-missed", StageQueued, 5),
		active("received-2", StageReceived, 6),
	}, []string{"queued-first", "queued-late"}, CookPool{})

	view, err := NewGetKitchen(board, kitchenCatalog(t, "restaurant-1")).Handle(context.Background(), "restaurant-1")

	require.NoError(t, err)
	assert.Equal(t, []string{"received-1", "received-2", "queued-first", "queued-late", "queued-missed", "ready", "preparing"}, ids(view.Orders))
	assert.Equal(t, "Кухня №8", view.Restaurant.Name)
	assert.Equal(t, []ItemView{{Name: "Фо бо", Quantity: 2}, {Name: "", Quantity: 1}}, view.Orders[0].Items, "an item gone from the menu has no name")
	assert.Equal(t, at(2), view.Orders[0].ReceivedAt)
	assert.Equal(t, "order-received-1", view.Orders[0].OrderID)
	assert.Nil(t, view.Orders[0].AcceptedAt, "RECEIVED is not accepted yet")
	assert.Nil(t, view.Orders[0].ExpectedReadyAt)
	require.NotNil(t, view.Orders[2].AcceptedAt)
	require.NotNil(t, view.Orders[2].ExpectedReadyAt)
	assert.Equal(t, at(5), *view.Orders[2].AcceptedAt)
	assert.Equal(t, at(20), *view.Orders[2].ExpectedReadyAt)
}

func TestKitchenActionReadsTheOrderBack(t *testing.T) {
	request := ActionRequest{RestaurantID: "restaurant-1", KitchenOrderID: "ro-1", Action: ActionStartPreparation}
	board := NewMockKitchenBoard(t)
	board.EXPECT().ActiveOrders(mock.Anything, "restaurant-1").Return([]ActiveOrder{active("other", StageReceived, 0), active("ro-1", StagePreparing, 1)}, nil).Once()
	board.EXPECT().CookPool(mock.Anything, "restaurant-1").Return(CookPool{}, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().MenuItemNames(mock.Anything, "restaurant-1").Return(menuNames, nil).Once()
	h := NewApplyKitchenAction(applying(t, request, nil), board, catalog)

	result, err := h.Handle(context.Background(), request)

	require.NoError(t, err)
	require.NotNil(t, result.Order)
	assert.Equal(t, "ro-1", result.Order.ID)
	assert.Equal(t, StagePreparing, result.Order.Stage)
	assert.Empty(t, result.Problem)
}

func TestRejectedOrLeftOrderIsNull(t *testing.T) {
	commands := NewMockKitchenCommands(t)
	commands.EXPECT().Apply(mock.Anything, "r", "ro-1", ActionReject).Return(nil).Once()
	board := NewMockKitchenBoard(t)
	h := NewApplyKitchenAction(commands, board, NewMockCatalog(t))

	// A rejected order is not read back: the board expects no call yet.
	rejected, err := h.Handle(context.Background(), ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionReject})
	require.NoError(t, err)
	assert.Equal(t, ActionResult{}, rejected)

	commands.EXPECT().Apply(mock.Anything, "r", "ro-1", ActionMarkReady).Return(nil).Once()
	board.EXPECT().ActiveOrders(mock.Anything, "r").Return(nil, nil).Once()
	left, err := h.Handle(context.Background(), ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionMarkReady})
	require.NoError(t, err)
	assert.Nil(t, left.Order)
	assert.Empty(t, left.Problem)
}

func TestStageConflictIsAProblem(t *testing.T) {
	request := ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionAccept}
	// The board expects no call: the order is not read back.
	result, err := NewApplyKitchenAction(applying(t, request, ErrStageConflict), NewMockKitchenBoard(t), NewMockCatalog(t)).Handle(context.Background(), request)

	require.NoError(t, err)
	assert.Equal(t, ActionResult{Problem: ProblemOrderStageConflict}, result)
}

func TestOtherKitchenRefusalsStayErrors(t *testing.T) {
	request := ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionMarkAssembled}
	_, err := NewApplyKitchenAction(applying(t, request, ErrNotPermitted), NewMockKitchenBoard(t), NewMockCatalog(t)).Handle(context.Background(), request)
	assert.ErrorIs(t, err, ErrNotPermitted)

	// An unknown action reaches no command: the commands expect no call.
	_, err = NewApplyKitchenAction(NewMockKitchenCommands(t), NewMockKitchenBoard(t), NewMockCatalog(t)).Handle(context.Background(), ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: "cook"})
	assert.ErrorIs(t, err, ErrUnknownAction)
}

// AC-800: an order shows its cook from the Cook Pool; the pool is on the screen.
func TestKitchenShowsTheCookPoolAndTheCookOfAnOrder(t *testing.T) {
	preparing := active("ro-1", StagePreparing, 0)
	preparing.CookID = "cook-aigul"
	retired := active("ro-2", StageReady, 1)
	retired.CookID = "cook-gone"
	aigul := Cook{ID: "cook-aigul", Name: "Айгуль", OnLine: true, CurrentKitchenOrderID: "ro-1"}
	pool := CookPool{Size: 1, OnLine: 1, Busy: 1, Queued: 1, Cooks: []Cook{aigul}}
	board := kitchenBoard(t, []ActiveOrder{preparing, retired, active("ro-3", StageQueued, 2)}, nil, pool)

	view, err := NewGetKitchen(board, kitchenCatalog(t, "restaurant-1")).Handle(context.Background(), "restaurant-1")

	require.NoError(t, err)
	assert.Equal(t, pool, view.CookPool)
	byID := map[string]OrderView{}
	for _, order := range view.Orders {
		byID[order.ID] = order
	}
	assert.Equal(t, &aigul, byID["ro-1"].Cook)
	assert.Nil(t, byID["ro-2"].Cook, "a cook who left the pool is not named")
	assert.Nil(t, byID["ro-3"].Cook, "a queued order has no cook")
}

func TestStartWithoutAFreeCookIsAProblem(t *testing.T) {
	request := ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionStartPreparation}
	result, err := NewApplyKitchenAction(applying(t, request, ErrNoCookAvailable), NewMockKitchenBoard(t), NewMockCatalog(t)).Handle(context.Background(), request)

	require.NoError(t, err)
	assert.Equal(t, ActionResult{Problem: ProblemNoCookAvailable}, result)
}

// AC-793, AC-799: a Cook Pool command answers the pool read back; business
// refusals are problems, the other refusals stay errors.
func TestCookPoolCommandsAnswerThePool(t *testing.T) {
	pool := CookPool{Size: 1, OnLine: 1, Cooks: []Cook{{ID: "cook-1", Name: "Олег", OnLine: true}}}
	commands := NewMockCookCommands(t)
	commands.EXPECT().SetOnLine(mock.Anything, "r", "cook-1", true).Return(nil).Once()
	h := NewChangeCookPool(commands, poolReading(t, pool))

	result, err := h.Handle(context.Background(), CookPoolRequest{RestaurantID: "r", Command: CookCommandPutOnLine, CookID: "cook-1"})

	require.NoError(t, err)
	assert.Equal(t, CookPoolResult{Pool: pool}, result)

	for cause, problem := range map[error]CookPoolProblem{
		ErrCookBusy:        ProblemCookBusy,
		ErrCookOnLine:      ProblemCookOnLine,
		ErrCookRetired:     ProblemCookRetired,
		ErrInvalidCookName: ProblemCookNameInvalid,
	} {
		refused := NewMockCookCommands(t)
		refused.EXPECT().SetOnLine(mock.Anything, "r", "cook-1", false).Return(cause).Once()
		result, err := NewChangeCookPool(refused, poolReading(t, pool)).Handle(context.Background(), CookPoolRequest{RestaurantID: "r", Command: CookCommandTakeOffLine, CookID: "cook-1"})
		require.NoError(t, err)
		assert.Equal(t, CookPoolResult{Pool: pool, Problem: problem}, result)
	}

	denied := NewMockCookCommands(t)
	denied.EXPECT().Hire(mock.Anything, "r", "Марат").Return(ErrNotPermitted).Once()
	_, err = NewChangeCookPool(denied, NewMockCookPoolReader(t)).Handle(context.Background(), CookPoolRequest{RestaurantID: "r", Command: CookCommandHire, Name: "Марат"})
	assert.ErrorIs(t, err, ErrNotPermitted)
}

// poolReading expects one read of the Cook Pool of restaurant r.
func poolReading(t *testing.T, pool CookPool) *MockCookPoolReader {
	pools := NewMockCookPoolReader(t)
	pools.EXPECT().CookPool(mock.Anything, "r").Return(pool, nil).Once()
	return pools
}
