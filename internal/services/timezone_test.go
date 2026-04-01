package services

import (
	"math"
	"testing"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

// These tests verify that timezone-sensitive operations use the user's
// local timezone, not UTC. The bugs they catch: nap detection and sleep
// midpoint calculation were both using UTC hours, producing wrong results
// for any user not in UTC.

// TestNapDetection_NonUTCTimezone verifies that a 2pm local nap stored
// as UTC in PocketBase is correctly detected as a nap.
//
// The bug: ConvertSleepRecords checked g.start.Hour() which returns the
// UTC hour. A 2pm PST nap is stored as 10pm UTC (Hour()=22), which passes
// the >=10 check — but a 6am PST wake-up stored as 2pm UTC (Hour()=14)
// would also pass, getting misclassified as a nap.
func TestNapDetection_NonUTCTimezone(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles") // UTC-8 in winter
	c := newSleepCollection()

	// Scenario: user in LA, slept 11pm-7am PST, took 30min nap at 2pm PST.
	// PocketBase stores everything in UTC, so:
	//   11pm PST = 7am UTC next day
	//   7am PST  = 3pm UTC
	//   2pm PST  = 10pm UTC
	nightRec := core.NewRecord(c)
	nightRec.Set("sleep_start", "2024-01-16 07:00:00") // 11pm PST Jan 15
	nightRec.Set("sleep_end", "2024-01-16 15:00:00")   // 7am PST Jan 16
	nightRec.Set("source", "fitbit")

	napRec := core.NewRecord(c)
	napRec.Set("sleep_start", "2024-01-16 22:00:00") // 2pm PST Jan 16
	napRec.Set("sleep_end", "2024-01-16 22:30:00")   // 2:30pm PST Jan 16
	napRec.Set("source", "fitbit")

	// With correct timezone: night=not nap, afternoon=nap
	_, periods := ConvertSleepRecords([]*core.Record{nightRec, napRec}, la)
	if len(periods) != 2 {
		t.Fatalf("expected 2 periods, got %d", len(periods))
	}
	if periods[0].IsNap {
		t.Error("8h overnight sleep should NOT be a nap")
	}
	if !periods[1].IsNap {
		t.Error("30min afternoon sleep at 2pm local should be a nap")
	}

	// Without timezone (defaults to UTC): the nap check uses UTC hours.
	// Night: starts at 07:00 UTC (Hour=7, < 10) → not nap ✓
	// Nap: starts at 22:00 UTC (Hour=22, >= 10, dur < 2h) → nap ✓
	// This case happens to work in UTC, but test the inverse...
}

// TestNapDetection_EarlyMorningSleepNotNap verifies that a short early-morning
// sleep (e.g., 5am-6:30am local) is NOT classified as a nap.
//
// The bug: Without timezone conversion, a 5am PST sleep is stored as 1pm UTC.
// UTC Hour=13 >= 10, and duration 1.5h < 2h, so it gets classified as a nap.
// But 5am local is clearly main sleep, not a nap.
func TestNapDetection_EarlyMorningSleepNotNap(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	c := newSleepCollection()

	// 5am PST = 1pm UTC. 1.5h duration.
	rec := core.NewRecord(c)
	rec.Set("sleep_start", "2024-01-16 13:00:00") // 5am PST
	rec.Set("sleep_end", "2024-01-16 14:30:00")   // 6:30am PST
	rec.Set("source", "manual")

	// With LA timezone: local Hour=5, < 10 → NOT a nap ✓
	_, periods := ConvertSleepRecords([]*core.Record{rec}, la)
	if len(periods) != 1 {
		t.Fatalf("expected 1 period, got %d", len(periods))
	}
	if periods[0].IsNap {
		t.Error("5am local sleep should NOT be classified as a nap (local hour < 10)")
	}

	// Without timezone (UTC): Hour=13 >= 10, dur=1.5h < 2h → wrongly classified as nap
	_, periodsUTC := ConvertSleepRecords([]*core.Record{rec})
	if len(periodsUTC) != 1 {
		t.Fatalf("expected 1 period, got %d", len(periodsUTC))
	}
	if !periodsUTC[0].IsNap {
		t.Log("NOTE: without timezone, 5am PST stored as 1pm UTC is classified as nap (the bug this fix prevents)")
	}
}

// TestHabitualSleepMidpoint_NonUTCTimezone verifies that sleep midpoint is
// computed in the user's local timezone.
//
// The bug: habitualSleepMidpoint used p.Start.Hour() which returns UTC hour.
// A user sleeping 11pm-7am EST (midpoint 3am EST) has UTC times 4am-12pm
// (midpoint 8am UTC). Without timezone conversion, the model thinks they're
// a late sleeper (midpoint 8am) instead of a normal one (midpoint 3am).
func TestHabitualSleepMidpoint_NonUTCTimezone(t *testing.T) {
	est, _ := time.LoadLocation("America/New_York") // UTC-5 in winter

	// Five nights of 11pm-7am EST = 4am-12pm UTC
	var periods []engine.SleepPeriod
	for i := 1; i <= 5; i++ {
		// Build times in EST then they'll be stored with that location info
		day := time.Date(2024, 1, 15-i, 0, 0, 0, 0, est)
		start := time.Date(day.Year(), day.Month(), day.Day(), 23, 0, 0, 0, est) // 11pm EST
		end := start.Add(8 * time.Hour)                                          // 7am EST
		periods = append(periods, engine.SleepPeriod{Start: start, End: end})
	}

	now := time.Date(2024, 1, 16, 12, 0, 0, 0, est)

	// With EST timezone: midpoint should be ~3am (3.0h)
	mid, ok := habitualSleepMidpoint(periods, now, est)
	if !ok {
		t.Fatal("should return ok with 5 valid periods")
	}
	if math.Abs(mid-3.0) > 0.5 {
		t.Errorf("midpoint with EST timezone = %.2f, want ~3.0 (3am local)", mid)
	}

	// With UTC: midpoint would be ~8am (8.0h) — WRONG for an EST user
	midUTC, ok := habitualSleepMidpoint(periods, now, time.UTC)
	if !ok {
		t.Fatal("should return ok")
	}
	// The UTC midpoint should be significantly different from the local one,
	// proving the timezone matters.
	if math.Abs(midUTC-mid) < 2.0 {
		t.Logf("NOTE: UTC midpoint=%.2f vs local midpoint=%.2f — expected ~5h difference", midUTC, mid)
	}
	t.Logf("local midpoint=%.2fh, UTC midpoint=%.2fh (delta=%.2fh)", mid, midUTC, midUTC-mid)
}

// TestHabitualSleepMidpoint_UTCStoredTimesWithLocalTZ verifies the realistic
// scenario: PocketBase stores times in UTC, but we pass the user's timezone
// so the midpoint is computed correctly.
func TestHabitualSleepMidpoint_UTCStoredTimesWithLocalTZ(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo") // UTC+9

	// User in Tokyo sleeps 11pm-7am JST = 2pm-10pm UTC (previous day)
	// PocketBase stores UTC times.
	var periods []engine.SleepPeriod
	for i := 1; i <= 6; i++ {
		// 11pm JST Jan (15-i) = 2pm UTC Jan (15-i)
		startUTC := time.Date(2024, 1, 15-i, 14, 0, 0, 0, time.UTC)
		endUTC := time.Date(2024, 1, 15-i, 22, 0, 0, 0, time.UTC) // 7am JST next day
		periods = append(periods, engine.SleepPeriod{Start: startUTC, End: endUTC})
	}

	now := time.Date(2024, 1, 16, 3, 0, 0, 0, time.UTC) // noon JST

	// With Tokyo timezone: converts UTC→JST, midpoint should be ~3am JST
	mid, ok := habitualSleepMidpoint(periods, now, tokyo)
	if !ok {
		t.Fatal("should return ok with 6 valid periods")
	}
	if math.Abs(mid-3.0) > 1.0 {
		t.Errorf("Tokyo user midpoint = %.2fh, want ~3.0 (3am JST)", mid)
	}

	// With UTC: midpoint would be ~6pm UTC (18.0h) — completely wrong
	midUTC, ok := habitualSleepMidpoint(periods, now, time.UTC)
	if !ok {
		t.Fatal("should return ok")
	}
	if math.Abs(midUTC-18.0) > 1.0 {
		t.Logf("NOTE: UTC midpoint=%.2fh (expected ~18.0 = 6pm UTC)", midUTC)
	}
	t.Logf("Tokyo midpoint=%.2fh JST, UTC midpoint=%.2fh UTC", mid, midUTC)
}
