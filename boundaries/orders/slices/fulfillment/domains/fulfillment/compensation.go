package fulfillment

// Compensation is the Saga over the journal of confirmed steps in reverse
// order, followed by recording the negative outcome of the Order. A step
// closed by its owner (rejection, PreparationFailed, DeliveryFailed) is not
// compensated, and the Restaurant Order is not cancelled after Pickup.
func (p *Process) Compensation() []Step {
	var plan []Step
	if p.deliveryCreated && !p.deliveryClosed {
		plan = append(plan, routeStep(StepCancelDelivery))
	}
	if p.restaurantRequested && !p.restaurantClosed && !p.pickedUp {
		plan = append(plan, routeStep(StepCancelRestaurantOrder))
	}
	switch {
	case p.captured:
		plan = append(plan, routeStep(StepRefundPayment))
	case p.authorized:
		plan = append(plan, routeStep(StepCancelPaymentAuthorization))
	}
	return append(plan, Step{Kind: StepFailOrCancelOrder})
}

// Compensate moves the failed process to StageCompensating and returns its
// Compensation plan. Confirming StepFailOrCancelOrder ends it in StageCancelled.
func (p *Process) Compensate() []Step {
	p.final = StageCompensating
	return p.Compensation()
}
