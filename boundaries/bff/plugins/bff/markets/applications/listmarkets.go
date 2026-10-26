// Package applications holds the scenario of the Market choice: the client
// picks the Market it sends in X-Nomnom-Market from this list (ADR 41).
package applications

import "context"

// Market is a city where delivery works.
type Market struct {
	ID       string
	Name     string
	Currency string
}

// MarketDirectory lists the Markets of the control plane.
type MarketDirectory interface {
	Markets(ctx context.Context) ([]Market, error)
}

// ListMarkets is public and needs no Market: it is how the client gets one.
type ListMarkets struct {
	directory MarketDirectory
}

func NewListMarkets(directory MarketDirectory) *ListMarkets {
	return &ListMarkets{directory: directory}
}

// Handle returns the Markets in the order of the control plane: by name.
func (h *ListMarkets) Handle(ctx context.Context) ([]Market, error) {
	return h.directory.Markets(ctx)
}
