package fulfillorder

import "time"

// Policy holds the business deadlines of the workflow. The product values are
// not agreed yet (ADR 5): DefaultPolicy contains technical placeholders.
type Policy struct {
	AcceptanceTimeout       time.Duration
	AssignmentTimeout       time.Duration
	MaxReplans              int
	OperatorDecisionTimeout time.Duration
	PreparationTimeout      time.Duration
	// MaxPreparationExtension caps how far PreparationDelayed moves the deadline.
	MaxPreparationExtension time.Duration
	PickupTimeout           time.Duration
	DeliveryTimeout         time.Duration
	// PublishStage upserts the OrderFulfillmentStage Search Attribute; it must
	// be registered in the namespace before enabling.
	PublishStage bool
}

// DefaultPolicy returns placeholder deadlines, not product decisions.
func DefaultPolicy() Policy {
	return Policy{
		AcceptanceTimeout:       10 * time.Minute,
		AssignmentTimeout:       15 * time.Minute,
		MaxReplans:              2,
		OperatorDecisionTimeout: 30 * time.Minute,
		PreparationTimeout:      90 * time.Minute,
		MaxPreparationExtension: 60 * time.Minute,
		PickupTimeout:           60 * time.Minute,
		DeliveryTimeout:         120 * time.Minute,
		PublishStage:            true,
	}
}
