package convert

import (
	"fmt"
	"os"
	"path/filepath"
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

// LocalZone names the zone the server runs in, which the old cron tasks used.
// The zone is empty, with no note, when it is UTC. It is empty with a note for
// the report when the server's zone is local time that has no name: the
// converted schedules then run in UTC.
func LocalZone() (zone, note string) {
	name := time.Local.String()
	if name == "Local" || name == "" {
		name = strings.TrimPrefix(os.Getenv("TZ"), ":")
		if name == "" {
			// /etc/localtime links into the zone database on most systems.
			if link, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
				if _, after, ok := strings.Cut(link, "zoneinfo/"); ok {
					name = after
				}
			}
		}
	}
	switch name {
	case "UTC", "Etc/UTC", "GMT", "Z":
		return "", ""
	}
	if _, err := time.LoadLocation(name); name == "" || name == "Local" || err != nil {
		return "", "the server zone has no name, so the schedule now runs in UTC: check its time zone"
	}
	return name, ""
}
