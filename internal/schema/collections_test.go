package schema

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestEnsureCollectionsDeduplicatesBrokenDateUpserts(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Cleanup() })
	if err := EnsureCollections(app); err != nil {
		t.Fatal(err)
	}
	user, err := app.FindAuthRecordByEmail("users", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}

	schedules, err := app.FindCollectionByNameOrId("energy_schedules")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		record := core.NewRecord(schedules)
		record.Set("user", user.Id)
		record.Set("date", "2026-07-25")
		record.Set("wake_time", time.Date(2026, 7, 25, 7, i, 0, 0, time.UTC))
		record.Set("schedule_json", []any{})
		if err := app.Save(record); err != nil {
			t.Fatal(err)
		}
	}

	sleepRecords, err := app.FindCollectionByNameOrId("sleep_records")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		record := core.NewRecord(sleepRecords)
		record.Set("user", user.Id)
		record.Set("date", "2026-07-25")
		record.Set("sleep_start", "2026-07-25 23:00:00.000Z")
		record.Set("sleep_end", "2026-07-26 07:00:00.000Z")
		record.Set("source", "fitbit")
		record.Set("duration_minutes", 450)
		if err := app.Save(record); err != nil {
			t.Fatal(err)
		}
	}

	if err := EnsureCollections(app); err != nil {
		t.Fatal(err)
	}
	scheduleCount, err := app.CountRecords("energy_schedules")
	if err != nil {
		t.Fatal(err)
	}
	sleepCount, err := app.CountRecords("sleep_records")
	if err != nil {
		t.Fatal(err)
	}
	if scheduleCount != 1 || sleepCount != 1 {
		t.Errorf("deduplicated counts: schedules=%d sleep=%d, want 1 each", scheduleCount, sleepCount)
	}
}
