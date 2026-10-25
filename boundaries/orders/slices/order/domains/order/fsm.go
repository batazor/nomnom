// Package order contains the canonical Order aggregate and state machine for the order slice.
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

// IsFinal reports whether the state is a terminal outcome of the Order lifecycle.
func (s State) IsFinal() bool { return s == StateCancelled || s == StateCompleted }

// AcceptsCancellation reports whether the transition table lets an Order in
// this State be cancelled; CheckCancellation reads the same row.
func (s State) AcceptsCancellation() bool {
	definition, ok := transitionTable[transitionKey{from: s, trigger: TriggerCancel}]
	return ok && definition.outcome == transitionAllowed
}

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

type transitionKey struct {
	from    State
	trigger Trigger
}

type transitionOutcome uint8

const (
	transitionAllowed transitionOutcome = iota
	transitionInvalid
	transitionAlreadyApplied
)

type guard func(TransitionInput) error

type transitionDefinition struct {
	outcome transitionOutcome
	to      State
	guard   guard
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

// transitionTable is the single source of truth for every State x Trigger pair.
var transitionTable = map[transitionKey]transitionDefinition{
	{StatePlaced, TriggerConfirm}:       {outcome: transitionAllowed, to: StateConfirmed},
	{StatePlaced, TriggerCancel}:        {outcome: transitionAllowed, to: StateCancelled, guard: requestedAtIsPresent},
	{StatePlaced, TriggerComplete}:      {outcome: transitionInvalid},
	{StatePlaced, TriggerRecordFact}:    {outcome: transitionAllowed, to: StatePlaced, guard: occurredAtIsPresent},
	{StateConfirmed, TriggerConfirm}:    {outcome: transitionAlreadyApplied, to: StateConfirmed},
	{StateConfirmed, TriggerCancel}:     {outcome: transitionAllowed, to: StateCancelled, guard: requestedAtIsPresent},
	{StateConfirmed, TriggerComplete}:   {outcome: transitionAllowed, to: StateCompleted, guard: occurredAtIsPresent},
	{StateConfirmed, TriggerRecordFact}: {outcome: transitionAllowed, to: StateConfirmed, guard: occurredAtIsPresent},
	{StateCancelled, TriggerConfirm}:    {outcome: transitionInvalid},
	{StateCancelled, TriggerCancel}:     {outcome: transitionAlreadyApplied, to: StateCancelled},
	{StateCancelled, TriggerComplete}:   {outcome: transitionInvalid},
	{StateCancelled, TriggerRecordFact}: {outcome: transitionInvalid},
	{StateCompleted, TriggerConfirm}:    {outcome: transitionInvalid},
	{StateCompleted, TriggerCancel}:     {outcome: transitionInvalid},
	{StateCompleted, TriggerComplete}:   {outcome: transitionAlreadyApplied, to: StateCompleted},
	{StateCompleted, TriggerRecordFact}: {outcome: transitionInvalid},
}

func transition(from State, trigger Trigger, input TransitionInput) (TransitionResult, error) {
	definition, ok := transitionTable[transitionKey{from: from, trigger: trigger}]
	if !ok || definition.outcome == transitionInvalid {
		return TransitionResult{}, InvalidTransition{From: from, Trigger: trigger}
	}
	if definition.outcome == transitionAlreadyApplied {
		return TransitionResult{}, AlreadyInState{State: from}
	}
	if definition.guard != nil {
		if err := definition.guard(input); err != nil {
			return TransitionResult{}, err
		}
	}
	return TransitionResult{From: from, Trigger: trigger, To: definition.to}, nil
}
