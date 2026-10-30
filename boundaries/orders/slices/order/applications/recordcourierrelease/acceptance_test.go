package recordcourierrelease_test

import (
	"context"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/recordcourierrelease"
	"nomnom/orders/slices/order/domains/order"
)

// TestAcceptance runs acceptance/recordcourierrelease.feature (ADR 19). A
// scenario is a subtest; its mocks report to it through godog.T. The store and
// the cleanup of the mocks belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				w      world
				result recordcourierrelease.Result
			)
			handle := func(ctx context.Context, in recordcourierrelease.Input) {
				var err error
				result, err = w.release.Handle(ctx, in)
				require.NoError(godog.T(ctx), err)
			}

			sc.Given(`^a courier is assigned to the delivery of an order$`, func(ctx context.Context) {
				workflow := NewMockNotifier(t)
				workflow.Test(godog.T(ctx))
				workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
				w = newWorld(t, workflow)
				w.changes.Test(godog.T(ctx))
			})

			sc.When(`^delivery removes the courier without a replacement$`, func(ctx context.Context) {
				handle(ctx, unassigned("event-1", "delivery-1", "courier-1", 3))
			})
			sc.When(`^delivery cancels the delivery$`, func(ctx context.Context) {
				w.changes.EXPECT().RelationshipsChanged(mock.Anything, order.ID("order-1")).RunAndReturn(w.store.RelationshipsChanged).Once()
				handle(ctx, cancelled("event-1", "delivery-1", 3))
			})
			sc.When(`^delivery reports the removal of the courier from another delivery$`, func(ctx context.Context) {
				handle(ctx, unassigned("event-1", "delivery-2", "courier-1", 3))
			})
			sc.When(`^delivery reports a removal older than the assignment$`, func(ctx context.Context) {
				older := unassigned("event-1", "delivery-1", "courier-1", assignedVersion-1)
				// occurred_at is later than the assignment; the version still orders it before.
				older.OccurredAt = assignedAt.Add(time.Hour)
				handle(ctx, older)
			})
			sc.When(`^delivery reports the cancellation of another delivery$`, func(ctx context.Context) {
				handle(ctx, cancelled("event-1", "delivery-2", 3))
			})

			sc.Then(`^the order has no courier$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), recordcourierrelease.OutcomeReleased, result.Outcome)
				assert.Empty(godog.T(ctx), w.stored(t).CourierID)
			})
			sc.Then(`^the order keeps its delivery$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), "delivery-1", w.linkedDelivery())
				assert.Len(godog.T(ctx), w.store.RelationshipChanges(), 1, "no RelationshipsChanged")
				w.changes.AssertNotCalled(godog.T(ctx), "RelationshipsChanged", mock.Anything, mock.Anything)
				assert.Equal(godog.T(ctx), order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: releasedAt, DeliveryVersion: 3}, w.stored(t))
			})
			sc.Then(`^order fulfillment is not notified of the release$`, func(ctx context.Context) {
				w.workflow.AssertNumberOfCalls(godog.T(ctx), "Notify", 1)
			})
			sc.Then(`^the order has no delivery$`, func(ctx context.Context) {
				assert.Empty(godog.T(ctx), w.linkedDelivery())
				assert.Len(godog.T(ctx), w.store.RelationshipChanges(), 2)
				assert.Equal(godog.T(ctx), order.CourierAssignment{AssignedAt: releasedAt, DeliveryVersion: 3}, w.stored(t))
			})
			sc.Then(`^the order keeps its courier and its delivery$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), recordcourierrelease.OutcomeIgnoredStale, result.Outcome)
				assert.Equal(godog.T(ctx), "delivery-1", w.linkedDelivery())
				assert.Len(godog.T(ctx), w.store.RelationshipChanges(), 1)
				assert.Equal(godog.T(ctx), order.CourierAssignment{
					DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt, DeliveryVersion: assignedVersion,
				}, w.stored(t))
				assert.Equal(godog.T(ctx), 2, w.store.InboxSize(), "the event is recorded in the Inbox")
			})
		},
		Options: &godog.Options{
			Format:   "progress",
			Paths:    []string{"acceptance"},
			Strict:   true,
			TestingT: t,
		},
	}

	require.Zero(t, suite.Run(), "acceptance scenarios failed")
}
