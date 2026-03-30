package services

import (
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// UserLocation returns the user's configured timezone, falling back to
// time.Local (which reflects the TZ env var) if not set.
func UserLocation(app core.App, userID string) *time.Location {
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err != nil {
		return time.Local
	}
	return LocationFromSettings(settings)
}

// LocationFromSettings returns the timezone from a settings record,
// falling back to time.Local.
func LocationFromSettings(settings *core.Record) *time.Location {
	if settings == nil {
		return time.Local
	}
	tz := settings.GetString("timezone")
	if tz == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Local
	}
	return loc
}
