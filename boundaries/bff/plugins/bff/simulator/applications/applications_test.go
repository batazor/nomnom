package applications

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSimulator struct {
	enabled bool
	err     error
	set     []bool
}

func (f *fakeSimulator) Enabled(context.Context) (bool, error) { return f.enabled, f.err }

func (f *fakeSimulator) SetEnabled(_ context.Context, enabled bool) (bool, error) {
	f.set = append(f.set, enabled)
	if f.err != nil {
		return false, f.err
	}
	f.enabled = enabled
	return enabled, nil
}

func TestGetSimulatorIsOnOnlyWhenOnEverywhere(t *testing.T) {
	enabled, err := NewGetSimulator(&fakeSimulator{enabled: true}, &fakeSimulator{enabled: true}, &fakeSimulator{enabled: true}).Handle(context.Background())
	require.NoError(t, err)
	assert.True(t, enabled)

	enabled, err = NewGetSimulator(&fakeSimulator{enabled: true}, &fakeSimulator{}, &fakeSimulator{enabled: true}).Handle(context.Background())
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestGetSimulatorFailsOnAnyFailure(t *testing.T) {
	_, err := NewGetSimulator(&fakeSimulator{enabled: true}, &fakeSimulator{err: ErrNotPermitted}).Handle(context.Background())
	assert.ErrorIs(t, err, ErrNotPermitted)
}

// AC-766: the switch reaches all three contexts.
func TestSetSimulatorEnabledSwitchesEveryContext(t *testing.T) {
	orders, restaurants, delivery := &fakeSimulator{}, &fakeSimulator{}, &fakeSimulator{}

	enabled, err := NewSetSimulatorEnabled(orders, restaurants, delivery).Handle(context.Background(), true)

	require.NoError(t, err)
	assert.True(t, enabled)
	for _, simulator := range []*fakeSimulator{orders, restaurants, delivery} {
		assert.Equal(t, []bool{true}, simulator.set)
	}
}

func TestSetSimulatorEnabledFailsOnAnyFailure(t *testing.T) {
	down := errors.New("delivery is down")
	_, err := NewSetSimulatorEnabled(&fakeSimulator{}, &fakeSimulator{}, &fakeSimulator{err: down}).Handle(context.Background(), true)
	assert.ErrorIs(t, err, down)
}
