// Package applications holds the demo Simulator scenarios of the header
// switch (FR-607): orders, restaurants and delivery each keep their own
// Simulator and authorize platform#operate_simulator themselves; BFF only
// forwards the verified user and combines the answers. Protobuf and GraphQL
// types do not reach it (BFF ADR 2).
package applications

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotPermitted is a user who may not operate the Simulator.
var ErrNotPermitted = errors.New("simulator operation is not permitted")

// Simulator is the Simulator of one bounded context, called on behalf of the
// user in the context. Both return ErrNotPermitted for PERMISSION_DENIED.
type Simulator interface {
	Enabled(ctx context.Context) (bool, error)
	SetEnabled(ctx context.Context, enabled bool) (bool, error)
}

// GetSimulator reads the Simulator of every context.
type GetSimulator struct {
	simulators []Simulator
}

func NewGetSimulator(simulators ...Simulator) GetSimulator {
	return GetSimulator{simulators: simulators}
}

// Handle answers true only when the Simulator is on in every context; any
// failure fails the whole read.
func (h GetSimulator) Handle(ctx context.Context) (bool, error) {
	enabled := true
	for _, simulator := range h.simulators {
		on, err := simulator.Enabled(ctx)
		if err != nil {
			return false, err
		}
		enabled = enabled && on
	}
	return enabled, nil
}

// SetSimulatorEnabled switches the Simulator of every context in turn.
type SetSimulatorEnabled struct {
	simulators []Simulator
}

func NewSetSimulatorEnabled(simulators ...Simulator) SetSimulatorEnabled {
	return SetSimulatorEnabled{simulators: simulators}
}

// Handle stops at the first failure: the contexts switched before it keep the
// new state, a repeat completes the switch. The answer is true only when
// every context answered enabled.
func (h SetSimulatorEnabled) Handle(ctx context.Context, enabled bool) (bool, error) {
	result := true
	for _, simulator := range h.simulators {
		on, err := simulator.SetEnabled(ctx, enabled)
		if err != nil {
			return false, fmt.Errorf("set simulator enabled: %w", err)
		}
		result = result && on
	}
	return result, nil
}
