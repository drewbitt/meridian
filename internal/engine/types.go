package engine

import "time"

// SleepRecord is a minimal sleep record used by the engine for calculations.
// The ingest package has a richer version; this is what the engine needs.
type SleepRecord struct {
	Date            time.Time
	SleepStart      time.Time
	SleepEnd        time.Time
	DurationMinutes int
}
