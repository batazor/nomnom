// Package system provides the process clock.
package system

import "time"

// Clock reads the wall clock in UTC.
type Clock struct{}

func (Clock) Now() time.Time { return time.Now().UTC() }
