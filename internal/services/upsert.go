package services

import (
	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/pocketbase/pocketbase/core"
)

// UpsertSleepRecord finds or creates a sleep record for the given user, date,
// and source, then updates all fields from the ingest record. Returns the
// saved PocketBase record.
func UpsertSleepRecord(app core.App, userID string, rec ingest.SleepRecord) (*core.Record, error) {
	dateStr := rec.Date.Format("2006-01-02")
	existing, _ := app.FindFirstRecordByFilter("sleep_records",
		"user = {:user} && date = {:date} && source = {:source}",
		map[string]any{"user": userID, "date": dateStr, "source": rec.Source},
	)

	var record *core.Record
	if existing != nil {
		record = existing
	} else {
		collection, err := app.FindCollectionByNameOrId("sleep_records")
		if err != nil {
			return nil, err
		}
		record = core.NewRecord(collection)
		record.Set("user", userID)
	}

	record.Set("date", dateStr)
	record.Set("sleep_start", rec.SleepStart)
	record.Set("sleep_end", rec.SleepEnd)
	record.Set("source", rec.Source)
	record.Set("duration_minutes", rec.DurationMinutes)
	record.Set("deep_minutes", rec.DeepMinutes)
	record.Set("rem_minutes", rec.REMMinutes)
	record.Set("light_minutes", rec.LightMinutes)
	record.Set("awake_minutes", rec.AwakeMinutes)

	if err := app.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}
