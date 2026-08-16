package services

import (
	"net/http"
	"testing"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/drewbitt/meridian/internal/schema"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

type notificationRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn notificationRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

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
	if debt.GapDays != 14 {
		t.Errorf("gap days: got %d, want 14", debt.GapDays)
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
	var titles []string
	previousClient := httpClient
	httpClient = &http.Client{Transport: notificationRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		titles = append(titles, r.Header.Get("Title"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { httpClient = previousClient })

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
	settings.Set("ntfy_server", "https://ntfy.invalid")
	settings.Set("ntfy_topic", "test-topic")
	settings.Set("timezone", "UTC")
	if err := app.Save(settings); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	wake := now.Add(-30 * time.Minute)
	if _, err := UpsertSleepRecord(app, user.Id, ingest.SleepRecord{
		Date:            ingest.SleepNightDate(wake.Add(-8 * time.Hour)),
		SleepStart:      wake.Add(-8 * time.Hour),
		SleepEnd:        wake,
		DurationMinutes: 480,
		Source:          ingest.SourceManual,
		NapExplicit:     true,
	}); err != nil {
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

func TestScheduleSummaryNotification_DelayedTrackerLifecycle(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, loc)

	if _, ok := scheduleSummaryNotification(engine.Schedule{}, now, loc); ok {
		t.Error("missing completed sleep should not consume the daily summary")
	}

	current := engine.Schedule{
		Points:      []engine.EnergyPoint{{Time: now}},
		MorningWake: now.Add(-2 * time.Hour),
	}
	title, ok := scheduleSummaryNotification(current, now, loc)
	if !ok || title != "Good morning!" {
		t.Errorf("recent upload: title=%q ready=%v", title, ok)
	}

	delayed := current
	delayed.MorningWake = now.Add(-6 * time.Hour)
	title, ok = scheduleSummaryNotification(delayed, now, loc)
	if !ok || title != "Today's sleep synced" {
		t.Errorf("delayed upload: title=%q ready=%v", title, ok)
	}

	yesterday := current
	yesterday.MorningWake = now.Add(-18 * time.Hour)
	if _, ok := scheduleSummaryNotification(yesterday, now, loc); ok {
		t.Error("previous-date wake should not generate a new-day summary")
	}
}
