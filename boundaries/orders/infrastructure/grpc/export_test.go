package grpcadapter

import "time"

// PinClock fixes the clock of a ServiceTokenSubjectResolver for the external
// tests: the shared user assertion vectors live at most 60 s around their now.
func PinClock(resolver SubjectResolver, now time.Time) {
	resolver.(*ServiceTokenSubjectResolver).now = func() time.Time { return now }
}
