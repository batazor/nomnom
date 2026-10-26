// Package publishroutes applies the routes of the public gateway from the
// registry (ADR 41): a route per Market to the Router of its Cell and the list
// of Markets for the client. It runs after every registry write and once at
// start; the whole set is built from one snapshot of the registry, read and
// applied under a lock, so a slower writer never applies an older snapshot.
package publishroutes

import (
	"context"
	"fmt"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

// ErrRoutesUnavailable: the routes were not applied. The registry write stays,
// and a repeat of the command applies them again.
var ErrRoutesUnavailable = fmt.Errorf("%w: routes of the public gateway not applied", application.ErrDependencyUnavailable)

// MarketRoute is a Market with the Router of its Market Placement.
type MarketRoute struct {
	Market market.Market
	Router cellvo.Router
}

// Registry reads the snapshot of the routes under the lock of route
// publication.
type Registry interface {
	// Locked runs publish while it holds the lock; routes are every Market by
	// name, then by id, read after the lock is taken.
	Locked(ctx context.Context, publish func(ctx context.Context, routes []MarketRoute) error) error
}

// Applier applies the whole set of routes to the public gateway.
type Applier interface {
	Apply(ctx context.Context, routes []MarketRoute) error
}

// Publisher is the port of the commands that change the registry.
type Publisher interface {
	Publish(ctx context.Context) error
}

type Handler struct {
	registry Registry
	applier  Applier
}

var _ Publisher = Handler{}

func New(registry Registry, applier Applier) Handler {
	return Handler{registry: registry, applier: applier}
}

// Publish applies the routes of the current registry.
func (h Handler) Publish(ctx context.Context) error {
	return h.registry.Locked(ctx, func(ctx context.Context, routes []MarketRoute) error {
		if err := h.applier.Apply(ctx, routes); err != nil {
			return fmt.Errorf("%w: %w", ErrRoutesUnavailable, err)
		}
		return nil
	})
}
