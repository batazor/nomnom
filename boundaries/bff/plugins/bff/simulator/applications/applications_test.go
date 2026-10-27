package applications

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// simulatorThatIs expects one read of a Simulator that is on or off.
func simulatorThatIs(t *testing.T, enabled bool) *MockSimulator {
	simulator := NewMockSimulator(t)
	simulator.EXPECT().Enabled(mock.Anything).Return(enabled, nil).Once()
	return simulator
}

// switching expects one switch of a Simulator on, answered with err.
func switching(t *testing.T, err error) *MockSimulator {
	simulator := NewMockSimulator(t)
	simulator.EXPECT().SetEnabled(mock.Anything, true).Return(err == nil, err).Once()
	return simulator
}

func TestGetSimulatorIsOnOnlyWhenOnEverywhere(t *testing.T) {
	enabled, err := NewGetSimulator(simulatorThatIs(t, true), simulatorThatIs(t, true), simulatorThatIs(t, true)).Handle(context.Background())
	require.NoError(t, err)
	assert.True(t, enabled)

	enabled, err = NewGetSimulator(simulatorThatIs(t, true), simulatorThatIs(t, false), simulatorThatIs(t, true)).Handle(context.Background())
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestGetSimulatorFailsOnAnyFailure(t *testing.T) {
	denied := NewMockSimulator(t)
	denied.EXPECT().Enabled(mock.Anything).Return(false, ErrNotPermitted).Once()
	_, err := NewGetSimulator(simulatorThatIs(t, true), denied).Handle(context.Background())
	assert.ErrorIs(t, err, ErrNotPermitted)
}

// AC-766: the switch reaches all three contexts.
func TestSetSimulatorEnabledSwitchesEveryContext(t *testing.T) {
	enabled, err := NewSetSimulatorEnabled(switching(t, nil), switching(t, nil), switching(t, nil)).Handle(context.Background(), true)

	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestSetSimulatorEnabledFailsOnAnyFailure(t *testing.T) {
	down := errors.New("delivery is down")
	_, err := NewSetSimulatorEnabled(switching(t, nil), switching(t, nil), switching(t, down)).Handle(context.Background(), true)
	assert.ErrorIs(t, err, down)
}
