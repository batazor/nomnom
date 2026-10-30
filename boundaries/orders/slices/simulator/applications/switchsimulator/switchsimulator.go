// Package switchsimulator reads and switches the demo customer Simulator
// (FR-604). Both use cases require platform#operate_simulator.
package switchsimulator

import (
	"context"
	"fmt"

	"nomnom/orders/pkg/authz"
)

const (
	PermissionOperateSimulator authz.Permission   = "operate_simulator"
	ResourcePlatform           authz.ResourceType = "platform"
	// PlatformID is the singleton platform object of the schema.
	PlatformID = "nomnom"
)

type Authorizer interface {
	Check(ctx context.Context, check authz.AccessCheck) error
}

// State is the persisted switch; a service without a stored state is disabled.
type State interface {
	Enabled(ctx context.Context) (bool, error)
	SetEnabled(ctx context.Context, enabled bool) error
}

type GetQuery struct {
	Subject authz.Subject
}

type SetCommand struct {
	Subject authz.Subject
	Enabled bool
}

type Handlers struct {
	authorizer Authorizer
	state      State
}

func NewHandlers(authorizer Authorizer, state State) Handlers {
	return Handlers{authorizer: authorizer, state: state}
}

// Get returns whether the Simulator is enabled.
func (h Handlers) Get(ctx context.Context, query GetQuery) (bool, error) {
	if err := h.authorize(ctx, query.Subject); err != nil {
		return false, err
	}
	enabled, err := h.state.Enabled(ctx)
	if err != nil {
		return false, fmt.Errorf("read simulator state: %w", err)
	}
	return enabled, nil
}

// Set stores the switch and returns the stored value.
func (h Handlers) Set(ctx context.Context, command SetCommand) (bool, error) {
	if err := h.authorize(ctx, command.Subject); err != nil {
		return false, err
	}
	if err := h.state.SetEnabled(ctx, command.Enabled); err != nil {
		return false, fmt.Errorf("store simulator state: %w", err)
	}
	return command.Enabled, nil
}

func (h Handlers) authorize(ctx context.Context, subject authz.Subject) error {
	if err := subject.Validate(); err != nil {
		return err
	}
	return authz.FailClosed(h.authorizer.Check(ctx, authz.AccessCheck{
		Subject:    subject,
		Permission: PermissionOperateSimulator,
		Resource:   authz.Resource{Type: ResourcePlatform, ID: PlatformID},
	}))
}
