package app

import (
	"context"
	"fmt"
	"log"
	mathrand "math/rand"
	"strconv"
	"strings"
	"time"

	"NyaMedia/internal/model"
)

const scheduledScanMaxJitter = 10 * time.Minute

type cronSchedule struct {
	minutes    map[int]struct{}
	hours      map[int]struct{}
	days       map[int]struct{}
	months     map[int]struct{}
	weekdays   map[int]struct{}
	dayAny     bool
	weekdayAny bool
}

func (a *App) startLibraryScanScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.checkScheduledLibraryScans(ctx, now.Local())
		}
	}
}

func (a *App) checkScheduledLibraryScans(ctx context.Context, now time.Time) {
	libraries, err := a.libraries.ListEnabled(context.Background())
	if err != nil {
		logErr := fmt.Sprintf("load libraries for scheduled scans: %v", err)
		log.Println(logErr)
		return
	}

	for _, library := range libraries {
		cronValue := strings.TrimSpace(library.ScanCron)
		if cronValue == "" {
			continue
		}
		schedule, err := parseCronSchedule(cronValue)
		if err != nil {
			log.Printf("invalid scan cron library=%s cron=%q error=%v", library.ID, cronValue, err)
			continue
		}
		if !schedule.matches(now) {
			continue
		}
		if !a.markScheduledScan(library.ID, now) {
			continue
		}
		a.startScheduledLibraryScan(ctx, library, now)
	}
}

func (a *App) markScheduledScan(libraryID string, now time.Time) bool {
	minuteKey := now.Truncate(time.Minute).Format(time.RFC3339)
	a.scheduleMu.Lock()
	defer a.scheduleMu.Unlock()
	if a.scheduledScans[libraryID] == minuteKey {
		return false
	}
	a.scheduledScans[libraryID] = minuteKey
	return true
}

func (a *App) startScheduledLibraryScan(ctx context.Context, library model.Library, scheduledAt time.Time) {
	jitter := time.Duration(mathrand.Int63n(int64(scheduledScanMaxJitter + 1)))
	go func() {
		if jitter > 0 {
			timer := time.NewTimer(jitter)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
		reason := map[string]any{
			"reason":       "scheduled scan",
			"scan_cron":    library.ScanCron,
			"scheduled_at": scheduledAt.Format(time.RFC3339),
			"jitter_ms":    jitter.Milliseconds(),
		}
		if err := a.enqueueLibraryScan(context.Background(), library.ID, reason); err != nil {
			log.Printf("enqueue scheduled scan library=%s: %v", library.ID, err)
		}
	}()
}

func parseCronSchedule(value string) (cronSchedule, error) {
	fields := strings.Fields(value)
	if len(fields) != 5 {
		return cronSchedule{}, fmt.Errorf("cron must contain 5 fields")
	}

	minutes, _, err := parseCronField(fields[0], 0, 59, false)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("minute: %w", err)
	}
	hours, _, err := parseCronField(fields[1], 0, 23, false)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("hour: %w", err)
	}
	days, dayAny, err := parseCronField(fields[2], 1, 31, false)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("day of month: %w", err)
	}
	months, _, err := parseCronField(fields[3], 1, 12, false)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("month: %w", err)
	}
	weekdays, weekdayAny, err := parseCronField(fields[4], 0, 7, true)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("weekday: %w", err)
	}

	return cronSchedule{
		minutes:    minutes,
		hours:      hours,
		days:       days,
		months:     months,
		weekdays:   weekdays,
		dayAny:     dayAny,
		weekdayAny: weekdayAny,
	}, nil
}

func parseCronField(value string, minValue, maxValue int, normalizeSunday bool) (map[int]struct{}, bool, error) {
	if value == "" {
		return nil, false, fmt.Errorf("empty field")
	}
	result := make(map[int]struct{})
	fieldAny := value == "*"
	for _, part := range strings.Split(value, ",") {
		if part == "" {
			return nil, false, fmt.Errorf("empty list item")
		}
		step := 1
		base := part
		if strings.Contains(part, "/") {
			pieces := strings.Split(part, "/")
			if len(pieces) != 2 || pieces[0] == "" || pieces[1] == "" {
				return nil, false, fmt.Errorf("invalid step %q", part)
			}
			base = pieces[0]
			parsedStep, err := strconv.Atoi(pieces[1])
			if err != nil || parsedStep <= 0 {
				return nil, false, fmt.Errorf("invalid step %q", pieces[1])
			}
			step = parsedStep
		}

		start, end, err := cronRange(base, minValue, maxValue)
		if err != nil {
			return nil, false, err
		}
		for item := start; item <= end; item += step {
			value := item
			if normalizeSunday && value == 7 {
				value = 0
			}
			result[value] = struct{}{}
		}
	}
	return result, fieldAny, nil
}

func cronRange(value string, minValue, maxValue int) (int, int, error) {
	if value == "*" {
		return minValue, maxValue, nil
	}
	if strings.Contains(value, "-") {
		pieces := strings.Split(value, "-")
		if len(pieces) != 2 {
			return 0, 0, fmt.Errorf("invalid range %q", value)
		}
		start, err := parseCronNumber(pieces[0], minValue, maxValue)
		if err != nil {
			return 0, 0, err
		}
		end, err := parseCronNumber(pieces[1], minValue, maxValue)
		if err != nil {
			return 0, 0, err
		}
		if start > end {
			return 0, 0, fmt.Errorf("range start greater than end %q", value)
		}
		return start, end, nil
	}
	number, err := parseCronNumber(value, minValue, maxValue)
	if err != nil {
		return 0, 0, err
	}
	return number, number, nil
}

func parseCronNumber(value string, minValue, maxValue int) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", value)
	}
	if number < minValue || number > maxValue {
		return 0, fmt.Errorf("value %d outside %d-%d", number, minValue, maxValue)
	}
	return number, nil
}

func (c cronSchedule) matches(now time.Time) bool {
	if _, ok := c.minutes[now.Minute()]; !ok {
		return false
	}
	if _, ok := c.hours[now.Hour()]; !ok {
		return false
	}
	if _, ok := c.months[int(now.Month())]; !ok {
		return false
	}

	_, dayMatches := c.days[now.Day()]
	_, weekdayMatches := c.weekdays[int(now.Weekday())]
	if c.dayAny || c.weekdayAny {
		return dayMatches && weekdayMatches
	}
	return dayMatches || weekdayMatches
}
