package temporal

import "nomnom/orders/slices/fulfillment/domains/fulfillment"

// Policy is the workflow form of the process deadlines. The embedded domain
// Policy keeps the flat Input payload; the product values are not agreed yet
// (ADR 5): DefaultPolicy contains technical placeholders.
type Policy struct {
	fulfillment.Policy
	// PublishStage upserts the OrderFulfillmentStage Search Attribute; it must
	// be registered in the namespace before enabling.
	PublishStage bool
}

// DefaultPolicy returns placeholder deadlines, not product decisions.
func DefaultPolicy() Policy {
	return Policy{Policy: fulfillment.DefaultPolicy(), PublishStage: true}
}
