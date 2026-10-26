package order

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type expectedOutcome struct {
	to      State
	invalid bool
	already bool
}

// TestTransitionMatrix is written independently of the table: every State x
// Trigger pair has an explicit outcome.
func TestTransitionMatrix(t *testing.T) {
	invalid := expectedOutcome{invalid: true}
	already := expectedOutcome{already: true}
	matrix := map[State]map[Trigger]expectedOutcome{
		StatePlaced: {
			TriggerConfirm:    {to: StateConfirmed},
			TriggerCancel:     {to: StateCancelled},
			TriggerComplete:   invalid,
			TriggerRecordFact: {to: StatePlaced},
		},
		StateConfirmed: {
			TriggerConfirm:    already,
			TriggerCancel:     {to: StateCancelled},
			TriggerComplete:   {to: StateCompleted},
			TriggerRecordFact: {to: StateConfirmed},
		},
		StateCancelled: {
			TriggerConfirm:    invalid,
			TriggerCancel:     already,
			TriggerComplete:   invalid,
			TriggerRecordFact: invalid,
		},
		StateCompleted: {
			TriggerConfirm:    invalid,
			TriggerCancel:     invalid,
			TriggerComplete:   already,
			TriggerRecordFact: invalid,
		},
	}
	input := TransitionInput{RequestedAt: testNow, OccurredAt: testNow}

	require.Len(t, matrix, len(allStates))
	for _, state := range allStates {
		require.Len(t, matrix[state], len(allTriggers), state)
		for _, trigger := range allTriggers {
			want := matrix[state][trigger]
			result, err := transition(state, trigger, input)
			switch {
			case want.invalid:
				assert.Equal(t, InvalidTransition{From: state, Trigger: trigger}, err, "%s x %s", state, trigger)
			case want.already:
				assert.Equal(t, AlreadyInState{State: state}, err, "%s x %s", state, trigger)
			default:
				require.NoError(t, err, "%s x %s", state, trigger)
				assert.Equal(t, TransitionResult{From: state, Trigger: trigger, To: want.to}, result)
			}
		}
	}
}

func TestHierarchyIsAcyclicAndReachesTopLevel(t *testing.T) {
	nodes := make([]node, 0, len(allStates)+len(allCompoundStates))
	for _, state := range allStates {
		nodes = append(nodes, node(state))
	}
	for _, compound := range allCompoundStates {
		nodes = append(nodes, node(compound))
	}
	require.Len(t, parent, len(nodes))
	for _, start := range nodes {
		seen := map[node]bool{}
		for current := start; current != topLevel; current = parent[current] {
			require.False(t, seen[current], "cycle through %s", start)
			seen[current] = true
			_, declared := parent[current]
			require.True(t, declared, "%s has no parent entry", current)
		}
	}
}

func TestCompoundStatesHaveChildrenAndStatesAreLeaves(t *testing.T) {
	children := map[node]int{}
	for _, up := range parent {
		children[up]++
	}
	for _, compound := range allCompoundStates {
		assert.Positive(t, children[node(compound)], compound)
	}
	for _, state := range allStates {
		assert.Zero(t, children[node(state)], state)
	}
}

func TestNodeTriggerDeclaredOnce(t *testing.T) {
	type key struct {
		source  node
		trigger Trigger
	}
	declared := map[key]bool{}
	for _, row := range transitions {
		k := key{row.source, row.trigger}
		assert.False(t, declared[k], "%s x %s declared twice", row.source, row.trigger)
		declared[k] = true
		_, known := parent[row.source]
		assert.True(t, known, "row on unknown node %s", row.source)
	}
	for _, repeat := range repeats {
		k := key{node(repeat.state), repeat.trigger}
		assert.False(t, declared[k], "%s x %s declared twice", repeat.state, repeat.trigger)
		declared[k] = true
	}
}

func TestRepeatsAreDeclaredOnLeaves(t *testing.T) {
	for _, repeat := range repeats {
		assert.Contains(t, allStates, repeat.state)
	}
}
