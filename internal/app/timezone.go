package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func (a *App) systemLocation(ctx context.Context) (*time.Location, string) {
	timezone := strings.TrimSpace(a.systemTimezone(ctx))
	if timezone != "" {
		location, err := time.LoadLocation(timezone)
		if err == nil {
			return location, timezone
		}
	}
	return time.Local, ""
}

func (a *App) systemTimezone(ctx context.Context) string {
	item, err := a.settings.Get(ctx, systemTimezoneSettingKey)
	if err != nil || item == nil {
		return ""
	}

	var timezone string
	if err := json.Unmarshal([]byte(item.ValueJSON), &timezone); err != nil {
		return ""
	}
	return strings.TrimSpace(timezone)
}

func validTimezoneName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	_, err := time.LoadLocation(value)
	return err == nil
}
