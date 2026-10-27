package applications

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetSimulatorIsOnOnlyWhenOnEverywhere(t *testing.T) {
	orders, restaurants, delivery := NewMockSimulator(t), NewMockSimulator(t), NewMockSimulator(t)
	orders.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	restaurants.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	delivery.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	enabled, err := NewGetSimulator(orders, restaurants, delivery).Handle(context.Background())
	require.NoError(t, err)
	assert.True(t, enabled)

	on, off, alsoOn := NewMockSimulator(t), NewMockSimulator(t), NewMockSimulator(t)
	on.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	off.EXPECT().Enabled(mock.Anything).Return(false, nil).Once()
	alsoOn.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	enabled, err = NewGetSimulator(on, off, alsoOn).Handle(context.Background())
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestGetSimulatorFailsOnAnyFailure(t *testing.T) {
	on := NewMockSimulator(t)
	on.EXPECT().Enabled(mock.Anything).Return(true, nil).Once()
	denied := NewMockSimulator(t)
	denied.EXPECT().Enabled(mock.Anything).Return(false, ErrNotPermitted).Once()
	_, err := NewGetSimulator(on, denied).Handle(context.Background())
	assert.ErrorIs(t, err, ErrNotPermitted)
}

// AC-766: the switch reaches all three contexts.
func TestSetSimulatorEnabledSwitchesEveryContext(t *testing.T) {
	orders, restaurants, delivery := NewMockSimulator(t), NewMockSimulator(t), NewMockSimulator(t)
	orders.EXPECT().SetEnabled(mock.Anything, true).Return(true, nil).Once()
	restaurants.EXPECT().SetEnabled(mock.Anything, true).Return(true, nil).Once()
	delivery.EXPECT().SetEnabled(mock.Anything, true).Return(true, nil).Once()
	enabled, err := NewSetSimulatorEnabled(orders, restaurants, delivery).Handle(context.Background(), true)

	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestSetSimulatorEnabledFailsOnAnyFailure(t *testing.T) {
	down := errors.New("delivery is down")
	orders, restaurants, delivery := NewMockSimulator(t), NewMockSimulator(t), NewMockSimulator(t)
	orders.EXPECT().SetEnabled(mock.Anything, true).Return(true, nil).Once()
	restaurants.EXPECT().SetEnabled(mock.Anything, true).Return(true, nil).Once()
	delivery.EXPECT().SetEnabled(mock.Anything, true).Return(false, down).Once()
	_, err := NewSetSimulatorEnabled(orders, restaurants, delivery).Handle(context.Background(), true)
	assert.ErrorIs(t, err, down)
}
