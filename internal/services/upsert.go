package services

import (
	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/pocketbase/pocketbase/core"
)

// UpsertSleepRecord finds or creates a sleep record, then updates all fields
// from the ingest record. Manual entries replace the same source/night;
// imported records use their start time as an event identity so a nap cannot
// overwrite the main sleep from the same source and day.
func UpsertSleepRecord(app core.App, userID string, rec ingest.SleepRecord) (*core.Record, error) {
	dateStr := PocketBaseDate(rec.Date)
	filter := "user = {:user} && date = {:date} && source = {:source}"
	params := map[string]any{"user": userID, "date": dateStr, "source": rec.Source}
	if rec.Source != ingest.SourceManual {
		filter = "user = {:user} && source = {:source} && sleep_start = {:start}"
		params = map[string]any{"user": userID, "source": rec.Source, "start": PocketBaseDateTime(rec.SleepStart)}
	}
	existing, _ := app.FindFirstRecordByFilter("sleep_records", filter, params)

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
	record.Set("is_nap", rec.IsNap)

	if err := app.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}
