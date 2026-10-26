// Package order contains the canonical Order aggregate and state machine for the
// order slice. The state machine is a hierarchy of States and CompoundStates;
// see docs/fsm.md#order and ADR 21.
package order

import (
	"fmt"
	"time"
)

type State string

const (
	StatePlaced    State = "placed"
	StateConfirmed State = "confirmed"
	StateCancelled State = "cancelled"
	StateCompleted State = "completed"
)

var allStates = [...]State{StatePlaced, StateConfirmed, StateCancelled, StateCompleted}

// CompoundState is a named group of States in the Ubiquitous Language. It is
// never persisted or published: an Order is always in exactly one State.
type CompoundState string

const (
	// CompoundOpen is an Open Order: Fulfillment is not finished, the Order
	// can be cancelled and records Fulfillment Facts.
	CompoundOpen CompoundState = "open"
	// CompoundClosed is a Closed Order: a final outcome of the lifecycle.
	CompoundClosed CompoundState = "closed"
)

var allCompoundStates = [...]CompoundState{CompoundOpen, CompoundClosed}

// node is a State or a CompoundState of the hierarchy.
type node string

// topLevel marks a node without a parent.
const topLevel node = ""

// parent of every node of the hierarchy.
var parent = map[node]node{
	node(CompoundOpen):   topLevel,
	node(CompoundClosed): topLevel,
	node(StatePlaced):    node(CompoundOpen),
	node(StateConfirmed): node(CompoundOpen),
	node(StateCancelled): node(CompoundClosed),
	node(StateCompleted): node(CompoundClosed),
}

// lineage is the state followed by its ancestors up to the top level.
func lineage(state State) []node {
	var nodes []node
	for current := node(state); current != topLevel; current = parent[current] {
		nodes = append(nodes, current)
	}
	return nodes
}

// isIn reports whether the state lies inside the compound state.
func isIn(state State, compound CompoundState) bool {
	for _, candidate := range lineage(state) {
		if candidate == node(compound) {
			return true
		}
	}
	return false
}

// IsFinal reports whether the state is a terminal outcome of the Order lifecycle.
func (s State) IsFinal() bool { return isIn(s, CompoundClosed) }

// AcceptsCancellation reports whether an Order in this State can be cancelled:
// TriggerCancel is declared on CompoundOpen, CheckCancellation resolves it.
func (s State) AcceptsCancellation() bool { return isIn(s, CompoundOpen) }

type Trigger string

const (
	TriggerConfirm  Trigger = "confirm"
	TriggerCancel   Trigger = "cancel"
	TriggerComplete Trigger = "complete"
	// TriggerRecordFact records a fulfillment fact reported by a neighbouring
	// context; its target state equals the source state.
	TriggerRecordFact Trigger = "record_fact"
)

var allTriggers = [...]Trigger{TriggerConfirm, TriggerCancel, TriggerComplete, TriggerRecordFact}

type TransitionInput struct {
	RequestedAt time.Time
	OccurredAt  time.Time
}

type TransitionResult struct {
	From    State
	Trigger Trigger
	To      State
}

type InvalidTransition struct {
	From    State
	Trigger Trigger
}

func (e InvalidTransition) Error() string {
	return fmt.Sprintf("invalid order transition from %q with trigger %q", e.From, e.Trigger)
}

type GuardFailed struct {
	Reason string
}

func (e GuardFailed) Error() string { return "order transition guard failed: " + e.Reason }

type AlreadyInState struct {
	State State
}

func (e AlreadyInState) Error() string {
	return fmt.Sprintf("order is already in state %q", e.State)
}

type guard func(TransitionInput) error

// target is a State or stay: a targetless transition keeps the current State.
type target struct {
	state State
	stay  bool
}

func to(state State) target { return target{state: state} }

var stay = target{stay: true}

type transitionRow struct {
	source  node
	trigger Trigger
	target  target
	guard   guard
}

type repeatRow struct {
	state   State
	trigger Trigger
}

func requestedAtIsPresent(input TransitionInput) error {
	if input.RequestedAt.IsZero() {
		return GuardFailed{Reason: "requested_at_required"}
	}
	return nil
}

func occurredAtIsPresent(input TransitionInput) error {
	if input.OccurredAt.IsZero() {
		return GuardFailed{Reason: "occurred_at_required"}
	}
	return nil
}

// transitions is the single source of truth for allowed transitions; a row
// on a CompoundState applies to every State inside it.
var transitions = []transitionRow{
	{source: node(StatePlaced), trigger: TriggerConfirm, target: to(StateConfirmed)},
	{source: node(StateConfirmed), trigger: TriggerComplete, target: to(StateCompleted), guard: occurredAtIsPresent},
	{source: node(CompoundOpen), trigger: TriggerCancel, target: to(StateCancelled), guard: requestedAtIsPresent},
	{source: node(CompoundOpen), trigger: TriggerRecordFact, target: stay, guard: occurredAtIsPresent},
}

// repeats are strict repeats of an applied transition with an explicit
// AlreadyInState outcome; they are declared on States only.
var repeats = []repeatRow{
	{state: StateConfirmed, trigger: TriggerConfirm},
	{state: StateCancelled, trigger: TriggerCancel},
	{state: StateCompleted, trigger: TriggerComplete},
}

// resolve finds the row for the trigger innermost-first without evaluating its
// guard: the closest declaration on the state or one of its ancestors wins;
// none is an InvalidTransition.
func resolve(from State, trigger Trigger) (transitionRow, error) {
	for _, current := range lineage(from) {
		for _, row := range transitions {
			if row.source == current && row.trigger == trigger {
				return row, nil
			}
		}
		for _, repeat := range repeats {
			if node(repeat.state) == current && repeat.trigger == trigger {
				return transitionRow{}, AlreadyInState{State: from}
			}
		}
	}
	return transitionRow{}, InvalidTransition{From: from, Trigger: trigger}
}

// targetOf is the State a resolved row leads to from the given State.
func (row transitionRow) targetOf(from State) State {
	if row.target.stay {
		return from
	}
	return row.target.state
}

// TriggerOf names the allowed transition that takes an Order from one State to
// another. Every allowed transition of a State has its own target, so the
// pair identifies the trigger; false — no allowed transition joins them.
func TriggerOf(from, to State) (Trigger, bool) {
	for _, trigger := range allTriggers {
		row, err := resolve(from, trigger)
		if err == nil && row.targetOf(from) == to {
			return trigger, true
		}
	}
	return "", false
}

func transition(from State, trigger Trigger, input TransitionInput) (TransitionResult, error) {
	row, err := resolve(from, trigger)
	if err != nil {
		return TransitionResult{}, err
	}
	if row.guard != nil {
		if err := row.guard(input); err != nil {
			return TransitionResult{}, err
		}
	}
	return TransitionResult{From: from, Trigger: trigger, To: row.targetOf(from)}, nil
}
