package services

import "time"

// PocketBaseDate returns the canonical UTC-midnight text representation used
// by PocketBase Date fields. Bare YYYY-MM-DD filter values do not compare
// equal to stored Date values.
func PocketBaseDate(day time.Time) string {
	return day.Format("2006-01-02") + " 00:00:00.000Z"
}

// PocketBaseDateTime returns the millisecond UTC representation PocketBase
// stores in Date fields and expects in equality filters.
func PocketBaseDateTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05.000Z")
}
