package fulfillment

import "time"

// Plan is the backward plan of a Scheduled Order computed by Plan Schedule:
// PlannedReadyAt is fixed by the first plan, the replan checkpoint moves only
// StartAt and CourierRequestAt (FR-677, FR-679).
type Plan struct {
	PlannedReadyAt   time.Time
	StartAt          time.Time
	CourierRequestAt time.Time
	ReplanAt         time.Time
}

// schedule is the journal of the scheduled route: the slot reservation made
// by Place Order, the plan and the steps due at its moments.
type schedule struct {
	slotConfirmed bool
	// slotClosed: the reservation is released or was not confirmed, nothing
	// is left to release.
	slotClosed bool

	planned   bool
	plan      Plan
	replanned bool

	replanDue  bool
	startDue   bool
	courierDue bool
	queued     bool
}

// Scheduled reports the scheduled route of a Scheduled Order.
func (p *Process) Scheduled() bool { return p.schedule != nil }

// Plan is the current plan of a Scheduled Order; zero before PlanSchedule or
// on the ASAP route.
func (p *Process) Plan() Plan {
	if p.schedule == nil {
		return Plan{}
	}
	return p.schedule.plan
}

// ConfirmPlan records the plan of StepPlanSchedule or StepReplanSchedule
// computed at the given time. A first plan made after its replan checkpoint
// is already fresh: the checkpoint does not run. A replan keeps the
// PlannedReadyAt the kitchen got and lets the new moments be due again.
func (p *Process) ConfirmPlan(step Step, plan Plan, at time.Time) {
	s := p.schedule
	if s == nil {
		return
	}
	switch step.Kind {
	case StepPlanSchedule:
		s.plan = plan
		s.planned = true
		s.replanned = !plan.ReplanAt.After(at)
	case StepReplanSchedule:
		s.plan.StartAt = plan.StartAt
		s.plan.CourierRequestAt = plan.CourierRequestAt
		s.replanned = true
		s.replanDue = false
		s.startDue = false
		s.courierDue = false
	}
}

func (p *Process) confirmSlot(confirmed bool) {
	if !confirmed {
		// The hold expired and the window filled up: the reservation is gone,
		// the Authorization is cancelled by the Saga.
		p.schedule.slotClosed = true
		p.fail(OutcomeFailure, ReasonSlotUnavailable)
		return
	}
	p.schedule.slotConfirmed = true
}

// queue starts the deadline of Preparation at QueueRestaurantOrder (FR-416).
func (p *Process) queue(at time.Time) {
	p.schedule.queued = true
	p.armPreparation(at)
}

// dueScheduledSteps marks every passed moment of the plan as due once the
// kitchen accepted the Scheduled Order; a moment already in the past is due
// at once (FR-678).
func (p *Process) dueScheduledSteps(passed func(time.Time) bool) bool {
	s := p.schedule
	if s == nil || !p.accepted || p.closing() {
		return false
	}
	due := false
	if !s.replanned && !s.replanDue && passed(s.plan.ReplanAt) {
		s.replanDue, due = true, true
	}
	if !s.queued && !s.startDue && passed(s.plan.StartAt) {
		s.startDue, due = true, true
	}
	if !p.deliveryCreated && !s.courierDue && passed(s.plan.CourierRequestAt) {
		s.courierDue, due = true, true
	}
	return due
}
