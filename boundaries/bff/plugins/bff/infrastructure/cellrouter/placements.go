package cellrouter

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	controlplanev1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/controlplane/v1"
)

// Placements is the Placement Map (Market → gateway of its Cell) held in
// memory. Start loads it in the background and refreshes it every TTL; a
// failed refresh keeps the last map (static stability, ADR 41). The control
// plane is never called on the request path.
type Placements struct {
	client    controlplanev1.ControlPlaneServiceClient
	ttl       time.Duration
	current   atomic.Pointer[map[string]string]
	refreshes metric.Int64Counter
	stop      context.CancelFunc
	done      chan struct{}
}

// NewPlacements reads GetPlacementMap of the control plane over conn; ttl is
// both the refresh period and the timeout of one refresh.
func NewPlacements(conn grpc.ClientConnInterface, ttl time.Duration) (*Placements, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("placement TTL must be positive, got %s", ttl)
	}
	refreshes, err := otel.Meter("github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/cellrouter").Int64Counter(
		"bff.placement_map.refreshes",
		metric.WithDescription("Refreshes of the Placement Map from the control plane, by outcome"),
	)
	if err != nil {
		return nil, fmt.Errorf("placement map metric: %w", err)
	}
	return &Placements{client: controlplanev1.NewControlPlaneServiceClient(conn), ttl: ttl, refreshes: refreshes}, nil
}

// Start loads the map in the background, then refreshes it every TTL until
// Stop. Requests before the first successful load are UNAVAILABLE.
func (p *Placements) Start() {
	ctx, stop := context.WithCancel(context.Background())
	p.stop, p.done = stop, make(chan struct{})
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(p.ttl)
		defer ticker.Stop()
		for {
			if err := p.Refresh(ctx); err != nil && ctx.Err() == nil {
				log.Printf("refresh placement map: %v; keeping the last map", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop ends the refresher and waits for it.
func (p *Placements) Stop() {
	if p.stop == nil {
		return
	}
	p.stop()
	<-p.done
}

// Refresh replaces the map with the one of the control plane; on any error
// the last map stays.
func (p *Placements) Refresh(ctx context.Context) error {
	err := p.load(ctx)
	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	p.refreshes.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	return err
}

func (p *Placements) load(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.ttl)
	defer cancel()
	// BFF reads the map as itself: no user assertion.
	response, err := p.client.GetPlacementMap(identity.WithoutUser(ctx), &controlplanev1.GetPlacementMapRequest{})
	if err != nil {
		return fmt.Errorf("get placement map: %w", err)
	}
	gateways := make(map[string]string, len(response.GetPlacements()))
	for _, placement := range response.GetPlacements() {
		market, gateway := placement.GetMarketId(), placement.GetGatewayAddress()
		if !marketSlug.MatchString(market) {
			return fmt.Errorf("placement map: invalid market_id %q", market)
		}
		if gateway == "" {
			return fmt.Errorf("placement map: market %s has no gateway address", market)
		}
		if _, seen := gateways[market]; seen {
			return fmt.Errorf("placement map: market %s placed twice", market)
		}
		gateways[market] = gateway
	}
	p.current.Store(&gateways)
	return nil
}

// Gateway is the internal gateway of the Cell that serves market.
func (p *Placements) Gateway(market string) (string, error) {
	gateways := p.current.Load()
	if gateways == nil {
		return "", &Refusal{Code: codes.Unavailable, Message: "placement map is not loaded yet"}
	}
	gateway, ok := (*gateways)[market]
	if !ok {
		return "", &Refusal{Code: codes.NotFound, Message: fmt.Sprintf("market %s is not served", market)}
	}
	return gateway, nil
}
