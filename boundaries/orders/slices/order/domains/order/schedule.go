package order

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidSchedulePolicy: the buffer and the replan lead are positive
// (FR-693); the configuration loader refuses other values at startup.
var ErrInvalidSchedulePolicy = errors.New("schedule policy requires a positive preparation buffer and replan lead")

// ErrInvalidScheduleEstimates: an estimate is a non-negative duration.
var ErrInvalidScheduleEstimates = errors.New("schedule estimates must not be negative")

// SchedulePolicy holds SCHEDULED_ORDER__PREPARATION_BUFFER and
// SCHEDULED_ORDER__REPLAN_LEAD.
type SchedulePolicy struct {
	PreparationBuffer time.Duration
	ReplanLead        time.Duration
}

// ScheduleEstimates are read when the plan is made: Preparation is the
// baseline preparation time of the restaurant, CourierLead (Create Delivery →
// Pickup) and Transit (Pickup → handover) the Delivery Duration Estimate of
// the Market.
type ScheduleEstimates struct {
	Preparation time.Duration
	CourierLead time.Duration
	Transit     time.Duration
}

// SchedulePlan is the backward plan of a Scheduled Order. The workflow keeps
// it in its Event History; the Order does not store it.
type SchedulePlan struct {
	PlannedReadyAt   time.Time
	StartAt          time.Time
	CourierRequestAt time.Time
	ReplanAt         time.Time
}

// PlanSchedule is the backward calculation of a Scheduled Order over UTC
// instants (FR-677):
//
//	planned_ready_at   = window_start − transit
//	start_at           = planned_ready_at − preparation − preparation_buffer
//	courier_request_at = planned_ready_at − courier_lead
//	replan_at          = start_at − replan_lead
//
// A non-zero plannedReadyAt is the moment the kitchen already got and is kept:
// the replan checkpoint moves only start_at and courier_request_at. Moments in
// the past are returned as computed; the caller runs such a step at once.
func PlanSchedule(window DeliveryWindow, estimates ScheduleEstimates, policy SchedulePolicy, plannedReadyAt time.Time) (SchedulePlan, error) {
	switch {
	case window.IsZero():
		return SchedulePlan{}, ErrNotScheduled
	case policy.PreparationBuffer <= 0 || policy.ReplanLead <= 0:
		return SchedulePlan{}, fmt.Errorf("%w: buffer %s, replan lead %s", ErrInvalidSchedulePolicy, policy.PreparationBuffer, policy.ReplanLead)
	case estimates.Preparation < 0 || estimates.CourierLead < 0 || estimates.Transit < 0:
		return SchedulePlan{}, fmt.Errorf("%w: %+v", ErrInvalidScheduleEstimates, estimates)
	}
	if plannedReadyAt.IsZero() {
		plannedReadyAt = window.Start().Add(-estimates.Transit)
	}
	start := plannedReadyAt.Add(-estimates.Preparation - policy.PreparationBuffer)
	return SchedulePlan{
		PlannedReadyAt:   plannedReadyAt.UTC(),
		StartAt:          start.UTC(),
		CourierRequestAt: plannedReadyAt.Add(-estimates.CourierLead).UTC(),
		ReplanAt:         start.Add(-policy.ReplanLead).UTC(),
	}, nil
}
