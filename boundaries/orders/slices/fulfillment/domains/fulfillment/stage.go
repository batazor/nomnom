package fulfillment

// Stage is the Workflow Stage: orchestration progress, never the Order State.
type Stage string

const (
	StageAuthorizingPayment Stage = "authorizing_payment"
	StageWaitingAcceptance  Stage = "waiting_acceptance"
	// StageScheduled: the kitchen accepted a Scheduled Order that waits for
	// its planned start (QueueRestaurantOrder), FR-678.
	StageScheduled       Stage = "scheduled"
	StagePreparing       Stage = "preparing"
	StageWaitingOperator Stage = "waiting_operator"
	StageWaitingPickup   Stage = "waiting_pickup"
	StageDelivering      Stage = "delivering"
	StageCompleting      Stage = "completing"
	StageCompensating    Stage = "compensating"
	StageCompleted       Stage = "completed"
	StageCancelled       Stage = "cancelled"
)

// Stage is the current Workflow Stage of the process.
func (p *Process) Stage() Stage {
	switch {
	case p.final != "":
		return p.final
	case !p.authorized:
		return StageAuthorizingPayment
	case !p.accepted:
		return StageWaitingAcceptance
	case p.escalated:
		return StageWaitingOperator
	case p.schedule != nil && !p.schedule.queued:
		return StageScheduled
	case !p.ready || !p.assigned:
		return StagePreparing
	case !p.pickedUp:
		return StageWaitingPickup
	default:
		return StageDelivering
	}
}
