package applications

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var receivedAt = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func at(minutes int) time.Time { return receivedAt.Add(time.Duration(minutes) * time.Minute) }

type fakeBoard struct {
	active       []ActiveOrder
	queue        []string
	pool         CookPool
	err          error
	reads        int
	commands     []ActionRequest
	applyErr     error
	cookCommands []CookPoolRequest
	cookErr      error
}

func (f *fakeBoard) CookPool(context.Context, string) (CookPool, error) { return f.pool, f.err }

func (f *fakeBoard) Hire(_ context.Context, restaurantID, name string) error {
	f.cookCommands = append(f.cookCommands, CookPoolRequest{RestaurantID: restaurantID, Command: CookCommandHire, Name: name})
	return f.cookErr
}

func (f *fakeBoard) SetOnLine(_ context.Context, restaurantID, cookID string, onLine bool) error {
	command := CookCommandTakeOffLine
	if onLine {
		command = CookCommandPutOnLine
	}
	f.cookCommands = append(f.cookCommands, CookPoolRequest{RestaurantID: restaurantID, Command: command, CookID: cookID})
	return f.cookErr
}

func (f *fakeBoard) Retire(_ context.Context, restaurantID, cookID string) error {
	f.cookCommands = append(f.cookCommands, CookPoolRequest{RestaurantID: restaurantID, Command: CookCommandRetire, CookID: cookID})
	return f.cookErr
}

func (f *fakeBoard) ActiveOrders(context.Context, string) ([]ActiveOrder, error) {
	f.reads++
	return f.active, f.err
}

func (f *fakeBoard) QueueOrder(context.Context, string) ([]string, error) { return f.queue, f.err }

func (f *fakeBoard) Apply(_ context.Context, restaurantID, kitchenOrderID string, action Action) error {
	f.commands = append(f.commands, ActionRequest{RestaurantID: restaurantID, KitchenOrderID: kitchenOrderID, Action: action})
	return f.applyErr
}

type fakeCatalog struct{}

func (fakeCatalog) Restaurant(_ context.Context, id string) (Restaurant, error) {
	return Restaurant{ID: id, Name: "Кухня №8", IsOpen: true}, nil
}

func (fakeCatalog) MenuItemNames(context.Context, string) (map[string]string, error) {
	return map[string]string{"menu-pho": "Фо бо"}, nil
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
	board := &fakeBoard{
		active: []ActiveOrder{
			active("ready", StageReady, 0),
			active("queued-late", StageQueued, 1),
			active("received-1", StageReceived, 2),
			active("preparing", StagePreparing, 3),
			active("queued-first", StageQueued, 4),
			active("queued-missed", StageQueued, 5),
			active("received-2", StageReceived, 6),
		},
		queue: []string{"queued-first", "queued-late"},
	}

	view, err := NewGetKitchen(board, fakeCatalog{}).Handle(context.Background(), "restaurant-1")

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
	board := &fakeBoard{active: []ActiveOrder{active("other", StageReceived, 0), active("ro-1", StagePreparing, 1)}}
	h := NewApplyKitchenAction(board, board, fakeCatalog{})

	result, err := h.Handle(context.Background(), ActionRequest{RestaurantID: "restaurant-1", KitchenOrderID: "ro-1", Action: ActionStartPreparation})

	require.NoError(t, err)
	assert.Equal(t, []ActionRequest{{RestaurantID: "restaurant-1", KitchenOrderID: "ro-1", Action: ActionStartPreparation}}, board.commands)
	require.NotNil(t, result.Order)
	assert.Equal(t, "ro-1", result.Order.ID)
	assert.Equal(t, StagePreparing, result.Order.Stage)
	assert.Empty(t, result.Problem)
}

func TestRejectedOrLeftOrderIsNull(t *testing.T) {
	board := &fakeBoard{}
	h := NewApplyKitchenAction(board, board, fakeCatalog{})

	rejected, err := h.Handle(context.Background(), ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionReject})
	require.NoError(t, err)
	assert.Equal(t, ActionResult{}, rejected)
	assert.Zero(t, board.reads, "a rejected order is not read back")

	left, err := h.Handle(context.Background(), ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionMarkReady})
	require.NoError(t, err)
	assert.Nil(t, left.Order)
	assert.Empty(t, left.Problem)
}

func TestStageConflictIsAProblem(t *testing.T) {
	board := &fakeBoard{applyErr: ErrStageConflict}
	result, err := NewApplyKitchenAction(board, board, fakeCatalog{}).Handle(context.Background(), ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionAccept})

	require.NoError(t, err)
	assert.Equal(t, ActionResult{Problem: ProblemOrderStageConflict}, result)
	assert.Zero(t, board.reads)
}

func TestOtherKitchenRefusalsStayErrors(t *testing.T) {
	refused := &fakeBoard{applyErr: ErrNotPermitted}
	_, err := NewApplyKitchenAction(refused, refused, fakeCatalog{}).Handle(context.Background(), ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionMarkAssembled})
	assert.ErrorIs(t, err, ErrNotPermitted)

	board := &fakeBoard{}
	_, err = NewApplyKitchenAction(board, board, fakeCatalog{}).Handle(context.Background(), ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: "cook"})
	assert.ErrorIs(t, err, ErrUnknownAction)
	assert.Empty(t, board.commands)
}

// AC-800: an order shows its cook from the Cook Pool; the pool is on the screen.
func TestKitchenShowsTheCookPoolAndTheCookOfAnOrder(t *testing.T) {
	preparing := active("ro-1", StagePreparing, 0)
	preparing.CookID = "cook-aigul"
	retired := active("ro-2", StageReady, 1)
	retired.CookID = "cook-gone"
	aigul := Cook{ID: "cook-aigul", Name: "Айгуль", OnLine: true, CurrentKitchenOrderID: "ro-1"}
	board := &fakeBoard{
		active: []ActiveOrder{preparing, retired, active("ro-3", StageQueued, 2)},
		pool:   CookPool{Size: 1, OnLine: 1, Busy: 1, Queued: 1, Cooks: []Cook{aigul}},
	}

	view, err := NewGetKitchen(board, fakeCatalog{}).Handle(context.Background(), "restaurant-1")

	require.NoError(t, err)
	assert.Equal(t, board.pool, view.CookPool)
	byID := map[string]OrderView{}
	for _, order := range view.Orders {
		byID[order.ID] = order
	}
	assert.Equal(t, &aigul, byID["ro-1"].Cook)
	assert.Nil(t, byID["ro-2"].Cook, "a cook who left the pool is not named")
	assert.Nil(t, byID["ro-3"].Cook, "a queued order has no cook")
}

func TestStartWithoutAFreeCookIsAProblem(t *testing.T) {
	board := &fakeBoard{applyErr: ErrNoCookAvailable}
	result, err := NewApplyKitchenAction(board, board, fakeCatalog{}).Handle(context.Background(), ActionRequest{RestaurantID: "r", KitchenOrderID: "ro-1", Action: ActionStartPreparation})

	require.NoError(t, err)
	assert.Equal(t, ActionResult{Problem: ProblemNoCookAvailable}, result)
}

// AC-793, AC-799: a Cook Pool command answers the pool read back; business
// refusals are problems, the other refusals stay errors.
func TestCookPoolCommandsAnswerThePool(t *testing.T) {
	pool := CookPool{Size: 1, OnLine: 1, Cooks: []Cook{{ID: "cook-1", Name: "Олег", OnLine: true}}}
	board := &fakeBoard{pool: pool}
	h := NewChangeCookPool(board, board)

	result, err := h.Handle(context.Background(), CookPoolRequest{RestaurantID: "r", Command: CookCommandPutOnLine, CookID: "cook-1"})

	require.NoError(t, err)
	assert.Equal(t, CookPoolResult{Pool: pool}, result)
	assert.Equal(t, []CookPoolRequest{{RestaurantID: "r", Command: CookCommandPutOnLine, CookID: "cook-1"}}, board.cookCommands)

	for cause, problem := range map[error]CookPoolProblem{
		ErrCookBusy:        ProblemCookBusy,
		ErrCookOnLine:      ProblemCookOnLine,
		ErrCookRetired:     ProblemCookRetired,
		ErrInvalidCookName: ProblemCookNameInvalid,
	} {
		refused := &fakeBoard{pool: pool, cookErr: cause}
		result, err := NewChangeCookPool(refused, refused).Handle(context.Background(), CookPoolRequest{RestaurantID: "r", Command: CookCommandTakeOffLine, CookID: "cook-1"})
		require.NoError(t, err)
		assert.Equal(t, CookPoolResult{Pool: pool, Problem: problem}, result)
	}

	denied := &fakeBoard{cookErr: ErrNotPermitted}
	_, err = NewChangeCookPool(denied, denied).Handle(context.Background(), CookPoolRequest{RestaurantID: "r", Command: CookCommandHire, Name: "Марат"})
	assert.ErrorIs(t, err, ErrNotPermitted)
}
