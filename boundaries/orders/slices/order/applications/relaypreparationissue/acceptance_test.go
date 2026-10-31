package relaypreparationissue_test

import (
	"context"
	"testing"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/relaypreparationissue"
)

// TestAcceptance runs acceptance/relaypreparationissue.feature (ADR 19). The use
// case keeps no state, so no store takes part. A scenario is a subtest; its mock
// reports to it through godog.T, and the cleanup of the mock belongs to t.
func TestAcceptance(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				workflow *MockNotifier
				result   relaypreparationissue.Result
			)
			issue := fulfillment.Notification{
				OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationFailed, OccurredAt: occurredAt, Reason: "oven_broken",
			}

			sc.Given(`^order fulfillment is running$`, func(ctx context.Context) {
				workflow = NewMockNotifier(t)
				workflow.Test(godog.T(ctx))
				workflow.EXPECT().Notify(mock.Anything, issue).Return(nil).Once()
			})
			sc.Given(`^order fulfillment is closed$`, func(ctx context.Context) {
				workflow = NewMockNotifier(t)
				workflow.Test(godog.T(ctx))
				workflow.EXPECT().Notify(mock.Anything, issue).Return(fulfillment.ErrWorkflowNotFound).Once()
			})

			sc.When(`^the restaurant reports a preparation issue$`, func(ctx context.Context) {
				var err error
				result, err = relaypreparationissue.NewHandler(workflow).Handle(ctx, relaypreparationissue.Input{
					EventID: "event-1", OrderID: "order-1", OccurredAt: occurredAt, Reason: "oven_broken",
				})
				require.NoError(godog.T(ctx), err)
			})

			sc.Then(`^order fulfillment is told of the issue and its reason$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
			})
			sc.Then(`^nobody is told of the issue$`, func(ctx context.Context) {
				assert.False(godog.T(ctx), result.WorkflowNotified)
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
