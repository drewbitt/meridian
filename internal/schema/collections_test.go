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

func TestEnsureCollectionsMigratesFitbitSettingsToGoogleHealth(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Cleanup() })
	if err := EnsureCollections(app); err != nil {
		t.Fatal(err)
	}

	settings, err := app.FindCollectionByNameOrId("settings")
	if err != nil {
		t.Fatal(err)
	}
	settings.Fields.Add(
		&core.TextField{Name: "fitbit_client_id"},
		&core.TextField{Name: "fitbit_client_secret"},
		&core.TextField{Name: "fitbit_access_token"},
		&core.TextField{Name: "fitbit_refresh_token"},
		&core.DateField{Name: "fitbit_token_expiry"},
		&core.DateField{Name: "fitbit_last_sync"},
		&core.DateField{Name: "fitbit_last_attempt"},
		&core.BoolField{Name: "fitbit_sleep_pending"},
	)
	if err := app.Save(settings); err != nil {
		t.Fatal(err)
	}

	if err := EnsureCollections(app); err != nil {
		t.Fatal(err)
	}
	settings, err = app.FindCollectionByNameOrId("settings")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"fitbit_client_id",
		"fitbit_client_secret",
		"fitbit_access_token",
		"fitbit_refresh_token",
		"fitbit_token_expiry",
		"fitbit_last_sync",
		"fitbit_last_attempt",
		"fitbit_sleep_pending",
	} {
		if field := settings.Fields.GetByName(name); field != nil {
			t.Errorf("legacy field %q still exists", name)
		}
	}
	for _, name := range []string{
		"google_health_client_id",
		"google_health_client_secret",
		"google_health_access_token",
		"google_health_refresh_token",
		"google_health_token_expiry",
		"google_health_last_sync",
		"google_health_last_attempt",
		"google_health_sleep_pending",
	} {
		if field := settings.Fields.GetByName(name); field == nil {
			t.Errorf("Google Health field %q missing", name)
		}
	}
	for _, name := range []string{
		"google_health_client_secret",
		"google_health_access_token",
		"google_health_refresh_token",
	} {
		field := settings.Fields.GetByName(name)
		if field == nil || !field.GetHidden() {
			t.Errorf("Google Health secret field %q is not hidden from API responses", name)
		}
	}

	sleepRecords, err := app.FindCollectionByNameOrId("sleep_records")
	if err != nil {
		t.Fatal(err)
	}
	if sleepRecords.Fields.GetByName("nap_explicit") == nil {
		t.Error("sleep_records.nap_explicit missing")
	}

	schedules, err := app.FindCollectionByNameOrId("energy_schedules")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"confidence",
		"confidence_reason",
		"observed_nights",
		"is_estimate",
	} {
		if field := schedules.Fields.GetByName(name); field == nil {
			t.Errorf("energy_schedules field %q missing", name)
		}
	}
}
