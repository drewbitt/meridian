// Package ingest provides sleep data importers from various sources.
package ingest

import "time"

// SleepRecord is the canonical sleep data type that all importers produce.
// It maps directly to the PocketBase sleep_records collection.
type SleepRecord struct {
	Date            time.Time `json:"date"`
	SleepStart      time.Time `json:"sleep_start"`
	SleepEnd        time.Time `json:"sleep_end"`
	Source          string    `json:"source"`
	DurationMinutes int       `json:"duration_minutes"`
	DeepMinutes     int       `json:"deep_minutes,omitempty"`
	REMMinutes      int       `json:"rem_minutes,omitempty"`
	LightMinutes    int       `json:"light_minutes,omitempty"`
	AwakeMinutes    int       `json:"awake_minutes,omitempty"`
	IsNap           bool      `json:"is_nap,omitempty"`
	NapExplicit     bool      `json:"nap_explicit,omitempty"`
}

// Source identifiers for sleep records.
const (
	SourceManual        = "manual"
	SourceFitbit        = "fitbit"
	SourceHealthConnect = "healthconnect"
	SourceAppleHealth   = "applehealth"
	SourceGadgetbridge  = "gadgetbridge"
)

// DateOnly truncates a time to midnight in its location.
func DateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// SleepNightDate returns the "night of" date for a sleep timestamp.
// Sleep starting before noon is attributed to the previous night's date
// (e.g., falling asleep at 2am March 30 → night of March 29).
//
// Important: t.Hour() uses t's inherent timezone. Callers must ensure t
// carries the correct timezone (e.g., from time.ParseInLocation or a
// format with offset like RFC3339). Times parsed without offset default
// to UTC, which may misattribute the night for non-UTC users.
func SleepNightDate(t time.Time) time.Time {
	d := DateOnly(t)
	if t.Hour() < 12 {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// LikelyNap applies Meridian's fallback nap heuristic in the timestamp's own
// timezone. Importers should prefer an explicit source-provided classification
// when one is available.
func LikelyNap(start, end time.Time) bool {
	return start.Hour() >= 10 && end.After(start) && end.Sub(start) < 2*time.Hour
}
