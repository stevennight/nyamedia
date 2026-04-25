package app

import (
	"testing"
	"time"
)

func TestCronScheduleMatches(t *testing.T) {
	tests := []struct {
		name  string
		cron  string
		now   time.Time
		match bool
	}{
		{name: "daily", cron: "0 4 * * *", now: time.Date(2026, 4, 25, 4, 0, 0, 0, time.UTC), match: true},
		{name: "wrong hour", cron: "0 4 * * *", now: time.Date(2026, 4, 25, 5, 0, 0, 0, time.UTC), match: false},
		{name: "every three days", cron: "0 4 */3 * *", now: time.Date(2026, 4, 25, 4, 0, 0, 0, time.UTC), match: true},
		{name: "sunday seven", cron: "0 4 * * 7", now: time.Date(2026, 4, 26, 4, 0, 0, 0, time.UTC), match: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedule, err := parseCronSchedule(tt.cron)
			if err != nil {
				t.Fatalf("parseCronSchedule() error = %v", err)
			}
			if got := schedule.matches(tt.now); got != tt.match {
				t.Fatalf("matches() = %v, want %v", got, tt.match)
			}
		})
	}
}

func TestParseCronScheduleRejectsInvalid(t *testing.T) {
	if _, err := parseCronSchedule("60 4 * * *"); err == nil {
		t.Fatal("expected invalid minute error")
	}
	if _, err := parseCronSchedule("0 4 * *"); err == nil {
		t.Fatal("expected field count error")
	}
}
