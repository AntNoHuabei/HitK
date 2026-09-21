package service

import "time"

// timeFromMS converts a millisecond epoch to a Time. Kept in one place so the
// frontend's numeric timestamps and Go's time type never drift apart.
func timeFromMS(ms int64) time.Time {
	return time.UnixMilli(ms)
}
