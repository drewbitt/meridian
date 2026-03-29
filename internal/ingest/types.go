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
}

// Source identifiers for sleep records.
const (
	SourceManual        = "manual"
	SourceFitbit        = "fitbit"
	SourceHealthConnect = "healthconnect"
	SourceAppleHealth   = "applehealth"
	SourceGadgetbridge  = "gadgetbridge"
)
