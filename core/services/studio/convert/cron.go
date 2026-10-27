package convert

import (
	"fmt"
	"strings"
	"time"
)

// ScheduleFromInterval turns the interval of an old agent ("10m", "1h", "24h")
// into a five-field cron text. An interval that has no plain cron form is not
// converted.
func ScheduleFromInterval(s string) (string, bool) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d < time.Minute {
		return "", false
	}
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("0 0 */%d * *", int(d/(24*time.Hour))), true
	case d%time.Hour == 0 && d < 24*time.Hour:
		return fmt.Sprintf("0 */%d * * *", int(d/time.Hour)), true
	case d%time.Minute == 0 && d < time.Hour:
		return fmt.Sprintf("*/%d * * * *", int(d/time.Minute)), true
	}
	return "", false
}

// ScheduleFromCron keeps a five-field cron text, and refuses any other (a task
// with a seconds field has no counterpart).
func ScheduleFromCron(s string) (string, bool) {
	f := strings.Fields(s)
	if len(f) != 5 {
		return "", false
	}
	return strings.Join(f, " "), true
}
