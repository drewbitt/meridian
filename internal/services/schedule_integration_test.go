package services

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/drewbitt/meridian/internal/schema"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestComputeUserSchedule_NoSleepDataHasNoForecast(t *testing.T) {
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

	schedule, points, debt, err := ComputeUserSchedule(app, user.Id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 0 || len(schedule.Points) != 0 {
		t.Error("empty account received a forecast")
	}
	if schedule.MorningWake.IsZero() {
		t.Error("expected fallback wake time for scheduler deduplication")
	}
	if debt.GapDays != 13 {
		t.Errorf("gap days: got %d, want 13", debt.GapDays)
	}
}

func TestUpsertSleepRecord_IsIdempotentAndPreservesDistinctImports(t *testing.T) {
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

	day := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	manual := ingest.SleepRecord{
		Date: day, SleepStart: day.Add(23 * time.Hour),
		SleepEnd: day.Add(31 * time.Hour), DurationMinutes: 480,
		Source: ingest.SourceManual,
	}
	if _, err := UpsertSleepRecord(app, user.Id, manual); err != nil {
		t.Fatal(err)
	}
	manual.SleepStart = manual.SleepStart.Add(30 * time.Minute)
	manual.SleepEnd = manual.SleepEnd.Add(30 * time.Minute)
	if _, err := UpsertSleepRecord(app, user.Id, manual); err != nil {
		t.Fatal(err)
	}

	nap1 := ingest.SleepRecord{
		Date: day, SleepStart: day.Add(13 * time.Hour),
		SleepEnd: day.Add(13*time.Hour + 30*time.Minute), DurationMinutes: 30,
		Source: ingest.SourceHealthConnect, IsNap: true,
	}
	nap2 := nap1
	nap2.SleepStart = day.Add(15 * time.Hour)
	nap2.SleepEnd = day.Add(15*time.Hour + 20*time.Minute)
	nap2.DurationMinutes = 20
	for _, rec := range []ingest.SleepRecord{nap1, nap1, nap2} {
		if _, err := UpsertSleepRecord(app, user.Id, rec); err != nil {
			t.Fatal(err)
		}
	}

	count, err := app.CountRecords("sleep_records")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("sleep rows: got %d, want 3 (one manual night and two distinct imports)", count)
	}
}

func TestRunMorningJob_ExistingScheduleDoesNotSuppressNotification(t *testing.T) {
	var mu sync.Mutex
	var titles []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		titles = append(titles, r.Header.Get("Title"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

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

	settingsCollection, err := app.FindCollectionByNameOrId("settings")
	if err != nil {
		t.Fatal(err)
	}
	settings := core.NewRecord(settingsCollection)
	settings.Set("user", user.Id)
	settings.Set("sleep_need_hours", 8)
	settings.Set("notifications_enabled", true)
	settings.Set("ntfy_server", srv.URL)
	settings.Set("ntfy_topic", "test-topic")
	settings.Set("timezone", "UTC")
	if err := app.Save(settings); err != nil {
		t.Fatal(err)
	}

	scheduleCollection, err := app.FindCollectionByNameOrId("energy_schedules")
	if err != nil {
		t.Fatal(err)
	}
	existing := core.NewRecord(scheduleCollection)
	existing.Set("user", user.Id)
	existing.Set("date", PocketBaseDate(time.Now().UTC()))
	existing.Set("wake_time", time.Now().UTC())
	existing.Set("schedule_json", []any{})
	if err := app.Save(existing); err != nil {
		t.Fatal(err)
	}

	if err := RunMorningJob(app, user.Id); err != nil {
		t.Fatalf("first morning job: %v", err)
	}
	if err := RunMorningJob(app, user.Id); err != nil {
		t.Fatalf("second morning job: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	greetings := 0
	for _, title := range titles {
		if title == "Good morning!" {
			greetings++
		}
	}
	if greetings != 1 {
		t.Errorf("good-morning notifications: got %d, want exactly 1 (all titles: %v)", greetings, titles)
	}
	count, err := app.CountRecords("energy_schedules")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("schedule rows: got %d, want 1", count)
	}
}
