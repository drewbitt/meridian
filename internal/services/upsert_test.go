package services

import (
	"testing"
	"time"

	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/drewbitt/meridian/internal/schema"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestUpsertSleepRecord_GoogleHealthAbsorbsOverlappingLegacyFitbitRow(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Cleanup() })
	if err := schema.EnsureCollections(app); err != nil {
		t.Fatal(err)
	}
	user, err := app.FindAuthRecordByEmail("users", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 7, 24, 3, 0, 0, 0, time.UTC)
	end := start.Add(8 * time.Hour)
	collection, err := app.FindCollectionByNameOrId("sleep_records")
	if err != nil {
		t.Fatal(err)
	}
	legacy := core.NewRecord(collection)
	legacy.Set("user", user.Id)
	legacy.Set("date", "2026-07-23")
	legacy.Set("sleep_start", start.Add(-5*time.Minute))
	legacy.Set("sleep_end", end.Add(5*time.Minute))
	legacy.Set("source", "fitbit")
	legacy.Set("duration_minutes", 400)
	if err := app.Save(legacy); err != nil {
		t.Fatal(err)
	}

	got, err := UpsertSleepRecord(app, user.Id, ingest.SleepRecord{
		Date:            time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
		SleepStart:      start,
		SleepEnd:        end,
		Source:          ingest.SourceGoogleHealth,
		DurationMinutes: 430,
		DeepMinutes:     100,
		REMMinutes:      90,
		LightMinutes:    240,
		AwakeMinutes:    50,
		NapExplicit:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Id != legacy.Id {
		t.Errorf("record id = %q, want legacy id %q", got.Id, legacy.Id)
	}
	if got.GetString("source") != ingest.SourceGoogleHealth {
		t.Errorf("source = %q", got.GetString("source"))
	}
	if got.GetInt("duration_minutes") != 430 ||
		got.GetInt("deep_minutes") != 100 ||
		!got.GetBool("nap_explicit") {
		t.Errorf("updated record = %+v", got)
	}

	count, err := app.CountRecords("sleep_records")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("sleep record count = %d, want 1", count)
	}
}
