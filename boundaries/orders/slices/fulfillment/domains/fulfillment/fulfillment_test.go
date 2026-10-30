package fulfillment_test

import (
	"strconv"
	"testing"
	"time"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

var start = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// process drives the model the way the workflow does: it executes due steps,
// records them and passes the time explicitly.
type process struct {
	t        *testing.T
	p        *fulfillment.Process
	now      time.Time
	steps    []fulfillment.Step
	declined map[fulfillment.StepKind]bool
	ids      int
}

func newProcess(t *testing.T) *process {
	t.Helper()
	return &process{t: t, p: fulfillment.New(fulfillment.DefaultPolicy()), now: start, declined: map[fulfillment.StepKind]bool{}}
}

// at moves the clock to start+offset.
func (s *process) at(offset time.Duration) *process {
	s.now = start.Add(offset)
	return s
}

// run executes every due route step; payment steps listed in declined are declined.
func (s *process) run() *process {
	for {
		step, ok := s.p.Next()
		if !ok {
			return s
		}
		s.steps = append(s.steps, step)
		s.p.Confirm(step, !s.declined[step.Kind], s.now)
	}
}

// signal applies a signal with a fresh event id.
func (s *process) signal(kind fulfillment.SignalKind) fulfillment.Effect {
	s.ids++
	return s.p.Apply(fulfillment.Signal{Kind: kind, EventID: string(kind) + "-" + strconv.Itoa(s.ids)}, s.now)
}

// expire moves the clock to the nearest deadline and expires it.
func (s *process) expire() fulfillment.Expiry {
	s.t.Helper()
	next := s.p.NextDeadline()
	if next.IsZero() {
		s.t.Fatal("no active deadline")
	}
	s.now = next
	return s.p.Expire(s.now)
}

// accepted runs the route up to a created Delivery.
func (s *process) accepted() *process {
	s.run()
	s.signal(fulfillment.SignalRestaurantOrderAccepted)
	return s.run()
}

// pickedUp runs the route up to Pickup and Capture.
func (s *process) pickedUp() *process {
	s.accepted()
	s.signal(fulfillment.SignalCourierAssignedToDelivery)
	s.signal(fulfillment.SignalRestaurantOrderReady)
	s.signal(fulfillment.SignalDeliveryPickedUp)
	return s.run()
}

func (s *process) outcome() (fulfillment.Outcome, string) { return s.p.Outcome() }

func kindsOf(steps []fulfillment.Step) []fulfillment.StepKind {
	kinds := make([]fulfillment.StepKind, 0, len(steps))
	for _, step := range steps {
		kinds = append(kinds, step.Kind)
	}
	return kinds
}

func keysOf(steps []fulfillment.Step) []string {
	keys := make([]string, 0, len(steps))
	for _, step := range steps {
		keys = append(keys, step.Key)
	}
	return keys
}
