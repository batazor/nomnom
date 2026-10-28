package order

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var scheduleDay = time.Date(2026, time.October, 25, 0, 0, 0, 0, time.UTC)

func at(hour, minute int) time.Time {
	return scheduleDay.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func window1900(t *testing.T) DeliveryWindow {
	t.Helper()
	window, err := NewDeliveryWindow(at(19, 0), at(19, 15))
	require.NoError(t, err)
	return window
}

var schedulePolicy = SchedulePolicy{PreparationBuffer: 5 * time.Minute, ReplanLead: 30 * time.Minute}

// AC-906: window 19:00–19:15, transit 20m, baseline 25m, buffer 5m, courier
// lead 15m.
func TestPlanScheduleComputesTheBackwardPlan(t *testing.T) {
	plan, err := PlanSchedule(window1900(t), ScheduleEstimates{
		Preparation: 25 * time.Minute, CourierLead: 15 * time.Minute, Transit: 20 * time.Minute,
	}, schedulePolicy, time.Time{})

	require.NoError(t, err)
	assert.Equal(t, SchedulePlan{
		PlannedReadyAt:   at(18, 40),
		StartAt:          at(18, 10),
		CourierRequestAt: at(18, 25),
		ReplanAt:         at(17, 40),
	}, plan)
}

// AC-909: at the checkpoint the baseline grew from 25 to 35 minutes: start_at
// moves 10 minutes earlier, planned_ready_at stays even if transit changed.
func TestPlanScheduleReplanKeepsPlannedReadyAt(t *testing.T) {
	plan, err := PlanSchedule(window1900(t), ScheduleEstimates{
		Preparation: 35 * time.Minute, CourierLead: 15 * time.Minute, Transit: 10 * time.Minute,
	}, schedulePolicy, at(18, 40))

	require.NoError(t, err)
	assert.Equal(t, at(18, 40), plan.PlannedReadyAt)
	assert.Equal(t, at(18, 0), plan.StartAt)
	assert.Equal(t, at(18, 25), plan.CourierRequestAt)
}

// AC-910: the plan returns a moment in the past as computed; the workflow
// runs such a step at once.
func TestPlanScheduleKeepsMomentsInThePast(t *testing.T) {
	plan, err := PlanSchedule(window1900(t), ScheduleEstimates{
		Preparation: 3 * time.Hour, CourierLead: 15 * time.Minute, Transit: 20 * time.Minute,
	}, schedulePolicy, at(18, 40))

	require.NoError(t, err)
	assert.Equal(t, at(15, 35), plan.StartAt)
	assert.Equal(t, at(15, 5), plan.ReplanAt)
}

func TestPlanScheduleRefusesInvalidInput(t *testing.T) {
	estimates := ScheduleEstimates{Preparation: time.Minute, CourierLead: time.Minute, Transit: time.Minute}
	_, err := PlanSchedule(DeliveryWindow{}, estimates, schedulePolicy, time.Time{})
	require.ErrorIs(t, err, ErrNotScheduled)

	_, err = PlanSchedule(window1900(t), estimates, SchedulePolicy{PreparationBuffer: 0, ReplanLead: time.Minute}, time.Time{})
	require.ErrorIs(t, err, ErrInvalidSchedulePolicy)

	_, err = PlanSchedule(window1900(t), ScheduleEstimates{Preparation: -time.Minute}, schedulePolicy, time.Time{})
	require.ErrorIs(t, err, ErrInvalidScheduleEstimates)
}

func TestDeliveryWindowRequiresStartBeforeEnd(t *testing.T) {
	for name, bounds := range map[string][2]time.Time{
		"empty":         {at(19, 15), at(19, 15)},
		"reversed":      {at(19, 15), at(19, 0)},
		"without start": {{}, at(19, 15)},
		"without end":   {at(19, 0), {}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewDeliveryWindow(bounds[0], bounds[1])
			require.ErrorIs(t, err, ErrInvalidDeliveryWindow)
		})
	}
	assert.True(t, DeliveryWindow{}.IsZero())
	assert.False(t, window1900(t).IsZero())
}
