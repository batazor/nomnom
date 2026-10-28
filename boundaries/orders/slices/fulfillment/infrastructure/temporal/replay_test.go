package temporal_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
)

// Replay tests of recorded Event Histories (ADR 5, FR-682): the current code
// must issue the same commands as the code that wrote each history.
//
//   - asap_before_scheduled_route.json: an ASAP Order completed by the
//     workflow before the scheduled route existed (commit 5157bb93); its
//     history has no scheduled-route marker and must replay the ASAP route.
//   - scheduled_stage.json: a Scheduled Order accepted by the kitchen and
//     waiting in stage scheduled on its replan timer, recorded with the
//     scheduled-route patch.
//
// Both were recorded against the Temporal dev server of pkg/testfixture with
// stub Activities; a change of the workflow that breaks either is a new patch.
func TestWorkflowReplaysRecordedHistories(t *testing.T) {
	for _, file := range []string{
		"testdata/asap_before_scheduled_route.json",
		"testdata/scheduled_stage.json",
	} {
		t.Run(file, func(t *testing.T) {
			replayer := worker.NewWorkflowReplayer()
			replayer.RegisterWorkflowWithOptions(temporal.Workflow, workflow.RegisterOptions{Name: temporal.WorkflowName})

			require.NoError(t, replayer.ReplayWorkflowHistoryFromJSONFile(nil, file))
		})
	}
}
