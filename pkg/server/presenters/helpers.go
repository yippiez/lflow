package presenters

import (
	"time"
)

// FormatTS rounds up the given timestamp to the microsecond
// so as to make the times in the responses consistent
func FormatTS(ts time.Time) time.Time {
	return ts.UTC().Round(time.Microsecond)
}
