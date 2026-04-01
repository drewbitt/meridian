package services

import (
	"math"
	"testing"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

// --- DetermineMorningWake edge cases ---

func TestDetermineMorningWake_AllNaps(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	today := time.Date(2024, 1, 16, 12, 0, 0, 0, loc)
	periods := []engine.SleepPeriod{
		{Start: time.Date(2024, 1, 16, 13, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 14, 0, 0, 0, loc), IsNap: true},
		{Start: time.Date(2024, 1, 16, 10, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 11, 0, 0, 0, loc), IsNap: true},
	}
	wake := DetermineMorningWake(periods, today, loc)
	want := time.Date(2024, 1, 16, 7, 0, 0, 0, loc) // fallback
	if !wake.Equal(want) {
		t.Errorf("all-nap scenario: got %v, want fallback %v", wake, want)
	}
}

func TestDetermineMorningWake_SleepOutsideNightWindow(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	today := time.Date(2024, 1, 16, 12, 0, 0, 0, loc)
	// Sleep entirely in the afternoon — outside the 8pm-noon window.
	periods := []engine.SleepPeriod{
		{Start: time.Date(2024, 1, 16, 14, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 22, 0, 0, 0, loc)},
	}
	wake := DetermineMorningWake(periods, today, loc)
	want := time.Date(2024, 1, 16, 7, 0, 0, 0, loc) // fallback
	if !wake.Equal(want) {
		t.Errorf("afternoon sleep: got %v, want fallback %v", wake, want)
	}
}

func TestDetermineMorningWake_NightWindowBoundary_8pm(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	today := time.Date(2024, 1, 16, 12, 0, 0, 0, loc)
	// Sleep starts exactly at 8pm (window start boundary).
	periods := []engine.SleepPeriod{
		{Start: time.Date(2024, 1, 15, 20, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 6, 0, 0, 0, loc)},
	}
	wake := DetermineMorningWake(periods, today, loc)
	want := time.Date(2024, 1, 16, 6, 0, 0, 0, loc)
	if !wake.Equal(want) {
		t.Errorf("8pm boundary: got %v, want %v", wake, want)
	}
}

func TestDetermineMorningWake_NightWindowBoundary_Noon(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	today := time.Date(2024, 1, 16, 14, 0, 0, 0, loc) // checking from 2pm
	// Sleep ends exactly at noon (window end).
	// End is AT noon, start is before noon → overlaps because end is not before nightStart and start is not after nightEnd.
	periods := []engine.SleepPeriod{
		{Start: time.Date(2024, 1, 16, 4, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 12, 0, 0, 0, loc)},
	}
	wake := DetermineMorningWake(periods, today, loc)
	want := time.Date(2024, 1, 16, 12, 0, 0, 0, loc)
	if !wake.Equal(want) {
		t.Errorf("noon boundary: got %v, want %v", wake, want)
	}
}

func TestDetermineMorningWake_SleepEndsAfterNoon(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	today := time.Date(2024, 1, 16, 14, 0, 0, 0, loc)
	// Sleep from 4am to 1pm — starts inside window, ends outside.
	// p.Start (4am) is NOT after nightEnd (noon), p.End (1pm) is NOT before nightStart (8pm yesterday)
	// So it overlaps — should be selected.
	periods := []engine.SleepPeriod{
		{Start: time.Date(2024, 1, 16, 4, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 13, 0, 0, 0, loc)},
	}
	wake := DetermineMorningWake(periods, today, loc)
	want := time.Date(2024, 1, 16, 13, 0, 0, 0, loc)
	if !wake.Equal(want) {
		t.Errorf("sleep ending after noon: got %v, want %v", wake, want)
	}
}

func TestDetermineMorningWake_MultipleSleeps_SelectsLongest(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	today := time.Date(2024, 1, 16, 12, 0, 0, 0, loc)
	periods := []engine.SleepPeriod{
		{Start: time.Date(2024, 1, 15, 22, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 3, 0, 0, 0, loc)}, // 5h
		{Start: time.Date(2024, 1, 16, 4, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 11, 0, 0, 0, loc)}, // 7h — longest
	}
	wake := DetermineMorningWake(periods, today, loc)
	want := time.Date(2024, 1, 16, 11, 0, 0, 0, loc) // end of longer period
	if !wake.Equal(want) {
		t.Errorf("multiple sleeps: got %v, want %v (longest period)", wake, want)
	}
}

func TestDetermineMorningWake_NapDoesNotShiftWake(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	// Sleep 11pm-7am + nap 1pm-3pm → morning wake still 7am
	periods := []engine.SleepPeriod{
		{Start: time.Date(2024, 1, 15, 23, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 7, 0, 0, 0, loc)},
		{Start: time.Date(2024, 1, 16, 13, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 15, 0, 0, 0, loc), IsNap: true},
	}
	today := time.Date(2024, 1, 16, 16, 0, 0, 0, loc)
	wake := DetermineMorningWake(periods, today, loc)
	want := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	if !wake.Equal(want) {
		t.Errorf("got %v, want %v — nap should not affect morning wake", wake, want)
	}
}

func TestDetermineMorningWake_NoSleepData_Fallback(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	today := time.Date(2024, 1, 16, 12, 0, 0, 0, loc)
	wake := DetermineMorningWake(nil, today, loc)
	want := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	if !wake.Equal(want) {
		t.Errorf("got %v, want %v (fallback 7am)", wake, want)
	}
}

// --- habitualSleepMidpoint edge cases ---

func TestHabitualSleepMidpoint_ExactlyFivePoints(t *testing.T) {
	t.Parallel()
	now := time.Now()
	var periods []engine.SleepPeriod
	for i := 1; i <= 5; i++ {
		periods = append(periods, engine.SleepPeriod{
			Start: now.Add(time.Duration(-i*24) * time.Hour).Add(-8 * time.Hour),
			End:   now.Add(time.Duration(-i*24) * time.Hour),
		})
	}
	_, ok := habitualSleepMidpoint(periods, now, time.UTC)
	if !ok {
		t.Error("should return ok with exactly 5 valid periods")
	}
}

func TestHabitualSleepMidpoint_FourPoints_NotEnough(t *testing.T) {
	now := time.Now()
	var periods []engine.SleepPeriod
	for i := 1; i <= 4; i++ {
		periods = append(periods, engine.SleepPeriod{
			Start: now.Add(time.Duration(-i*24) * time.Hour).Add(-8 * time.Hour),
			End:   now.Add(time.Duration(-i*24) * time.Hour),
		})
	}
	_, ok := habitualSleepMidpoint(periods, now, time.UTC)
	if ok {
		t.Error("should return false with only 4 periods (minimum is 5)")
	}
}

func TestHabitualSleepMidpoint_TwoPoints_NotEnough(t *testing.T) {
	now := time.Now()
	periods := []engine.SleepPeriod{
		{Start: now.Add(-1 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-1 * 24 * time.Hour)},
		{Start: now.Add(-2 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-2 * 24 * time.Hour)},
	}
	_, ok := habitualSleepMidpoint(periods, now, time.UTC)
	if ok {
		t.Error("should return false with only 2 periods")
	}
}

func TestHabitualSleepMidpoint_FilteredTooShort(t *testing.T) {
	now := time.Now()
	periods := []engine.SleepPeriod{
		// 6 periods but 3 are < 3 hours (filtered out) → only 3 valid < 5 minimum.
		{Start: now.Add(-1 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-1 * 24 * time.Hour)}, // 8h OK
		{Start: now.Add(-2 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-2 * 24 * time.Hour)}, // 8h OK
		{Start: now.Add(-3 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-3 * 24 * time.Hour)}, // 8h OK
		{Start: now.Add(-4 * 24 * time.Hour).Add(-2 * time.Hour), End: now.Add(-4 * 24 * time.Hour)}, // 2h filtered
		{Start: now.Add(-5 * 24 * time.Hour).Add(-1 * time.Hour), End: now.Add(-5 * 24 * time.Hour)}, // 1h filtered
		{Start: now.Add(-6 * 24 * time.Hour).Add(-1 * time.Hour), End: now.Add(-6 * 24 * time.Hour)}, // 1h filtered
	}
	_, ok := habitualSleepMidpoint(periods, now, time.UTC)
	if ok {
		t.Error("should return false when filtered below 5 valid points")
	}
}

func TestHabitualSleepMidpoint_OldDataIgnored(t *testing.T) {
	now := time.Now()
	periods := []engine.SleepPeriod{
		// 3 old (>14 days) + 4 recent = not enough (need 5).
		{Start: now.Add(-15 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-15 * 24 * time.Hour)},
		{Start: now.Add(-16 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-16 * 24 * time.Hour)},
		{Start: now.Add(-17 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-17 * 24 * time.Hour)},
		{Start: now.Add(-1 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-1 * 24 * time.Hour)},
		{Start: now.Add(-2 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-2 * 24 * time.Hour)},
		{Start: now.Add(-3 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-3 * 24 * time.Hour)},
		{Start: now.Add(-4 * 24 * time.Hour).Add(-8 * time.Hour), End: now.Add(-4 * 24 * time.Hour)},
	}
	_, ok := habitualSleepMidpoint(periods, now, time.UTC)
	if ok {
		t.Error("should return false — only 4 recent periods within 14-day window, 3 old ones ignored")
	}
}

func TestHabitualSleepMidpoint_ConsistentMidpoint(t *testing.T) {
	// Five consistent 11pm-7am sleepers → midpoint should be ~3am (3.0h).
	now := time.Now()
	loc := now.Location()
	var periods []engine.SleepPeriod
	for i := 1; i <= 5; i++ {
		day := now.AddDate(0, 0, -i)
		y, m, d := day.Date()
		periods = append(periods, engine.SleepPeriod{
			Start: time.Date(y, m, d-1, 23, 0, 0, 0, loc), // 11pm previous day
			End:   time.Date(y, m, d, 7, 0, 0, 0, loc),    // 7am
		})
	}
	mid, ok := habitualSleepMidpoint(periods, now, loc)
	if !ok {
		t.Fatal("should return ok")
	}
	// Midpoint of 11pm-7am = 3am = 3.0h.
	if math.Abs(mid-3.0) > 0.5 {
		t.Errorf("expected midpoint ~3.0, got %.2f", mid)
	}
}

// --- ResolveHabitTime ---

func TestResolveHabitTime_BasicAnchors(t *testing.T) {
	loc := time.UTC
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	schedule := engine.Schedule{
		MorningWake:     wake,
		CaffeineCutoff:  time.Date(2024, 1, 16, 12, 0, 0, 0, loc),
		MelatoninWindow: time.Date(2024, 1, 16, 21, 0, 0, 0, loc),
		OptimalNapStart: time.Date(2024, 1, 16, 14, 0, 0, 0, loc),
	}

	tests := map[string]struct {
		habit Habit
		want  time.Time
	}{
		"morning_wake+60min": {
			Habit{Anchor: "morning_wake", OffsetMinutes: 60},
			time.Date(2024, 1, 16, 8, 0, 0, 0, loc),
		},
		"caffeine_cutoff-30min": {
			Habit{Anchor: "caffeine_cutoff", OffsetMinutes: -30},
			time.Date(2024, 1, 16, 11, 30, 0, 0, loc),
		},
		"melatonin_window+0": {
			Habit{Anchor: "melatonin_window", OffsetMinutes: 0},
			time.Date(2024, 1, 16, 21, 0, 0, 0, loc),
		},
		"custom_time_14:30": {
			Habit{Anchor: "custom", CustomTime: "14:30"},
			time.Date(2024, 1, 16, 14, 30, 0, 0, loc),
		},
		"nap_window_zero_returns_zero": {
			Habit{Anchor: "nap_window", OffsetMinutes: 10},
			time.Time{}, // OptimalNapStart is set but this tests the no-nap schedule
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := schedule
			if name == "nap_window_zero_returns_zero" {
				s = engine.Schedule{MorningWake: wake} // no nap
			}
			got := ResolveHabitTime(tt.habit, s, loc)
			if !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveHabitTime_AllAnchorsZero(t *testing.T) {
	schedule := engine.Schedule{} // all zero
	loc := time.UTC
	anchors := []string{"morning_wake", "caffeine_cutoff", "melatonin_window", "nap_window"}
	for _, a := range anchors {
		h := Habit{Anchor: a, OffsetMinutes: 30}
		result := ResolveHabitTime(h, schedule, loc)
		if !result.IsZero() {
			t.Errorf("anchor %q with zero schedule should return zero time, got %v", a, result)
		}
	}
}

func TestResolveHabitTime_UnknownAnchor(t *testing.T) {
	loc := time.UTC
	schedule := engine.Schedule{
		MorningWake: time.Date(2024, 1, 16, 7, 0, 0, 0, loc),
	}
	h := Habit{Anchor: "unknown_anchor", OffsetMinutes: 30}
	result := ResolveHabitTime(h, schedule, loc)
	if !result.IsZero() {
		t.Errorf("unknown anchor should return zero time, got %v", result)
	}
}

func TestResolveHabitTime_LargeNegativeOffset(t *testing.T) {
	loc := time.UTC
	schedule := engine.Schedule{
		MelatoninWindow: time.Date(2024, 1, 16, 1, 0, 0, 0, loc), // 1am
	}
	// -180 min offset → should go to previous day 10pm.
	h := Habit{Anchor: "melatonin_window", OffsetMinutes: -180}
	result := ResolveHabitTime(h, schedule, loc)
	want := time.Date(2024, 1, 15, 22, 0, 0, 0, loc)
	if !result.Equal(want) {
		t.Errorf("large negative offset: got %v, want %v", result, want)
	}
}

func TestResolveHabitTime_InvalidCustomTime(t *testing.T) {
	loc := time.UTC
	schedule := engine.Schedule{
		MorningWake: time.Date(2024, 1, 16, 7, 0, 0, 0, loc),
	}
	h := Habit{Anchor: "custom", CustomTime: "25:99"}
	result := ResolveHabitTime(h, schedule, loc)
	if !result.IsZero() {
		t.Errorf("invalid custom time should return zero, got %v", result)
	}
}

func TestResolveHabitTime_CustomTimeZeroWake(t *testing.T) {
	loc := time.UTC
	schedule := engine.Schedule{} // MorningWake is zero
	h := Habit{Anchor: "custom", CustomTime: "14:30"}
	result := ResolveHabitTime(h, schedule, loc)
	if !result.IsZero() {
		t.Errorf("custom time with zero MorningWake should return zero, got %v", result)
	}
}

// --- DetermineMorningWake month boundary ---

func TestDetermineMorningWake_MonthBoundary(t *testing.T) {
	// On the 1st of a month, nightStart should be 8pm on the last day of previous month.
	// time.Date(2024, 2, 0, 20, ...) should normalize to Jan 31 20:00.
	loc := time.UTC
	today := time.Date(2024, 2, 1, 12, 0, 0, 0, loc)

	// Sleep from 11pm Jan 31 to 7am Feb 1.
	periods := []engine.SleepPeriod{
		{Start: time.Date(2024, 1, 31, 23, 0, 0, 0, loc), End: time.Date(2024, 2, 1, 7, 0, 0, 0, loc)},
	}
	wake := DetermineMorningWake(periods, today, loc)
	want := time.Date(2024, 2, 1, 7, 0, 0, 0, loc)
	if !wake.Equal(want) {
		t.Errorf("month boundary: got %v, want %v", wake, want)
	}
}

func TestDetermineMorningWake_YearBoundary(t *testing.T) {
	// Jan 1 should look back to Dec 31 8pm.
	loc := time.UTC
	today := time.Date(2024, 1, 1, 12, 0, 0, 0, loc)

	periods := []engine.SleepPeriod{
		{Start: time.Date(2023, 12, 31, 22, 0, 0, 0, loc), End: time.Date(2024, 1, 1, 6, 0, 0, 0, loc)},
	}
	wake := DetermineMorningWake(periods, today, loc)
	want := time.Date(2024, 1, 1, 6, 0, 0, 0, loc)
	if !wake.Equal(want) {
		t.Errorf("year boundary: got %v, want %v", wake, want)
	}
}

// --- habitualSleepMidpoint cross-midnight bug ---

func TestHabitualSleepMidpoint_CrossMidnight_WrapAround(t *testing.T) {
	// Bug: if sleep midpoints straddle midnight (e.g., 11pm and 1am),
	// the simple average gives (23+1)/2 = 12 (noon) instead of ~0 (midnight).
	// This would completely corrupt chronotype detection.
	now := time.Now()
	loc := now.Location()

	var periods []engine.SleepPeriod
	// Five nights with midpoints near midnight (11pm, midnight, 1am, 12:30am, 11:30pm).
	for i := 1; i <= 5; i++ {
		day := now.AddDate(0, 0, -i)
		y, m, d := day.Date()
		var start, end time.Time
		switch i {
		case 1: // 9pm-5am, midpoint=1am
			start = time.Date(y, m, d-1, 21, 0, 0, 0, loc)
			end = time.Date(y, m, d, 5, 0, 0, 0, loc)
		case 2: // 8pm-4am, midpoint=midnight
			start = time.Date(y, m, d-1, 20, 0, 0, 0, loc)
			end = time.Date(y, m, d, 4, 0, 0, 0, loc)
		case 3: // 7pm-3am, midpoint=11pm
			start = time.Date(y, m, d-1, 19, 0, 0, 0, loc)
			end = time.Date(y, m, d, 3, 0, 0, 0, loc)
		case 4: // 8:30pm-4:30am, midpoint=12:30am
			start = time.Date(y, m, d-1, 20, 30, 0, 0, loc)
			end = time.Date(y, m, d, 4, 30, 0, 0, loc)
		case 5: // 7:30pm-3:30am, midpoint=11:30pm
			start = time.Date(y, m, d-1, 19, 30, 0, 0, loc)
			end = time.Date(y, m, d, 3, 30, 0, 0, loc)
		}
		periods = append(periods, engine.SleepPeriod{Start: start, End: end})
	}

	mid, ok := habitualSleepMidpoint(periods, now, loc)
	if !ok {
		t.Fatal("should return ok with 3 valid periods")
	}

	// True midpoint should be near midnight (0h or 24h). With circular mean,
	// midpoints at 23h, 0h, and 1h correctly average to ~midnight.
	t.Logf("midpoint = %.2f (want ~0 or ~24)", mid)
	// Allow midnight ± 1 hour (0-1h or 23-24h).
	if mid > 1.0 && mid < 23.0 {
		t.Errorf("cross-midnight midpoints averaged to %.2f (should be near midnight)", mid)
	}
}

func TestResolveHabitTime_CustomIgnoresOffset(t *testing.T) {
	// Custom anchor uses the raw custom time, not base + offset.
	// Verify offset is truly ignored.
	loc := time.UTC
	schedule := engine.Schedule{
		MorningWake: time.Date(2024, 1, 16, 7, 0, 0, 0, loc),
	}
	h := Habit{Anchor: "custom", CustomTime: "14:30", OffsetMinutes: 60}
	result := ResolveHabitTime(h, schedule, loc)
	want := time.Date(2024, 1, 16, 14, 30, 0, 0, loc)
	if !result.Equal(want) {
		t.Errorf("custom with offset: got %v, want %v (offset should be ignored)", result, want)
	}
}

func TestResolveHabitTime_NapWindow(t *testing.T) {
	loc := time.UTC
	schedule := engine.Schedule{
		OptimalNapStart: time.Date(2024, 1, 16, 13, 0, 0, 0, loc),
	}
	h := Habit{Anchor: "nap_window", OffsetMinutes: -15}
	result := ResolveHabitTime(h, schedule, loc)
	want := time.Date(2024, 1, 16, 12, 45, 0, 0, loc)
	if !result.Equal(want) {
		t.Errorf("nap_window -15min: got %v, want %v", result, want)
	}
}

// --- Nap detection edge cases ---

func TestConvertSleepRecords_InvalidRecordsSkipped(t *testing.T) {
	c := newSleepCollection()

	// Start == End (zero duration).
	zd := core.NewRecord(c)
	zd.Set("date", "2024-01-16 10:00:00")
	zd.Set("sleep_start", "2024-01-16 10:00:00")
	zd.Set("sleep_end", "2024-01-16 10:00:00")
	zd.Set("source", "manual")

	// Start > End (invalid).
	inv := core.NewRecord(c)
	inv.Set("date", "2024-01-16 10:00:00")
	inv.Set("sleep_start", "2024-01-16 12:00:00")
	inv.Set("sleep_end", "2024-01-16 10:00:00")
	inv.Set("source", "manual")

	// Valid record.
	ok := core.NewRecord(c)
	ok.Set("date", "2024-01-16 23:00:00")
	ok.Set("sleep_start", "2024-01-15 23:00:00")
	ok.Set("sleep_end", "2024-01-16 07:00:00")
	ok.Set("source", "manual")

	records, periods := ConvertSleepRecords([]*core.Record{zd, inv, ok})
	if len(records) != 1 {
		t.Fatalf("expected 1 valid record, got %d", len(records))
	}
	if len(periods) != 1 {
		t.Fatalf("expected 1 valid period, got %d", len(periods))
	}
	if records[0].DurationMinutes != 480 {
		t.Errorf("expected 480 min duration, got %d", records[0].DurationMinutes)
	}
}

func TestConvertSleepRecords_AllInvalid(t *testing.T) {
	c := newSleepCollection()
	inv := core.NewRecord(c)
	inv.Set("date", "2024-01-16 10:00:00")
	inv.Set("sleep_start", "2024-01-16 12:00:00")
	inv.Set("sleep_end", "2024-01-16 10:00:00")
	inv.Set("source", "manual")

	records, periods := ConvertSleepRecords([]*core.Record{inv})
	if records != nil || periods != nil {
		t.Error("all-invalid input should return nil, nil")
	}
}

func TestNapDetection_EarlyMorningSleep_NotNap(t *testing.T) {
	c := newSleepCollection()
	// Short sleep at 9am (before 10am cutoff) should NOT be a nap.
	rec := core.NewRecord(c)
	rec.Set("date", "2024-01-16 09:00:00")
	rec.Set("sleep_start", "2024-01-16 09:00:00")
	rec.Set("sleep_end", "2024-01-16 10:30:00")
	rec.Set("source", "manual")

	_, periods := ConvertSleepRecords([]*core.Record{rec})
	if len(periods) != 1 {
		t.Fatalf("expected 1 period, got %d", len(periods))
	}
	if periods[0].IsNap {
		t.Error("9am sleep should NOT be a nap (before 10am cutoff)")
	}
}

func TestNapDetection_LongAfternoonSleep_NotNap(t *testing.T) {
	c := newSleepCollection()
	// 3-hour afternoon sleep should NOT be a nap (>= 2 hours).
	rec := core.NewRecord(c)
	rec.Set("date", "2024-01-16 13:00:00")
	rec.Set("sleep_start", "2024-01-16 13:00:00")
	rec.Set("sleep_end", "2024-01-16 16:00:00")
	rec.Set("source", "manual")

	_, periods := ConvertSleepRecords([]*core.Record{rec})
	if len(periods) != 1 {
		t.Fatalf("expected 1 period, got %d", len(periods))
	}
	if periods[0].IsNap {
		t.Error("3h afternoon sleep should NOT be a nap (>= 2h)")
	}
}

func TestNapDetection_ExactBoundary_10am_2h(t *testing.T) {
	c := newSleepCollection()
	// Exactly at 10am, exactly 2 hours → NOT a nap (duration is not < 2h).
	rec := core.NewRecord(c)
	rec.Set("date", "2024-01-16 10:00:00")
	rec.Set("sleep_start", "2024-01-16 10:00:00")
	rec.Set("sleep_end", "2024-01-16 12:00:00")
	rec.Set("source", "manual")

	_, periods := ConvertSleepRecords([]*core.Record{rec})
	if len(periods) != 1 {
		t.Fatalf("expected 1 period, got %d", len(periods))
	}
	if periods[0].IsNap {
		t.Error("exactly 2h at 10am should NOT be a nap (duration is not < 2h)")
	}
}

func TestNapDetection_JustUnder2h_IsNap(t *testing.T) {
	c := newSleepCollection()
	// 1h59m at 10am → IS a nap.
	rec := core.NewRecord(c)
	rec.Set("date", "2024-01-16 10:00:00")
	rec.Set("sleep_start", "2024-01-16 10:00:00")
	rec.Set("sleep_end", "2024-01-16 11:59:00")
	rec.Set("source", "manual")

	_, periods := ConvertSleepRecords([]*core.Record{rec})
	if len(periods) != 1 {
		t.Fatalf("expected 1 period, got %d", len(periods))
	}
	if !periods[0].IsNap {
		t.Error("1h59m at 10am should be a nap")
	}
}
