package engine

import (
	"testing"
	"time"
)

func TestClassifyZones_BasicSchedule(t *testing.T) {
	// Generate a typical day's energy curve.
	loc := time.UTC
	sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)

	periods := []SleepPeriod{{Start: sleepStart, End: sleepEnd}}
	predStart := sleepEnd.Add(-1 * time.Hour) // Start from 6am to include pre-wake
	predEnd := time.Date(2024, 1, 16, 23, 0, 0, 0, loc)

	points := PredictEnergy(DefaultParams(), periods, predStart, predEnd)
	schedule := ClassifyZones(points, sleepEnd)

	// Should have points.
	if len(schedule.Points) == 0 {
		t.Fatal("Expected classified points")
	}

	// Check that zones were assigned.
	zoneCounts := make(map[string]int)
	for _, p := range schedule.Points {
		zoneCounts[p.Zone]++
	}

	if zoneCounts[ZoneSleepInertia] == 0 {
		t.Error("Expected sleep inertia zone")
	}

	// Melatonin window should be ~14h after wake.
	expectedMel := sleepEnd.Add(14 * time.Hour)
	if schedule.MelatoninWindow.Hour() != expectedMel.Hour() {
		t.Errorf("Expected melatonin window at %dh, got %dh",
			expectedMel.Hour(), schedule.MelatoninWindow.Hour())
	}

	// Caffeine cutoff should be 10h before melatonin window.
	expectedCaffeine := schedule.MelatoninWindow.Add(-10 * time.Hour)
	if schedule.CaffeineCutoff.Hour() != expectedCaffeine.Hour() {
		t.Errorf("Expected caffeine cutoff at %dh, got %dh",
			expectedCaffeine.Hour(), schedule.CaffeineCutoff.Hour())
	}
}

// TestClassifyZones_BestFocusNeverZero is a regression test for the bug where
// BestFocusStart/End were left as zero values (rendering as "12:00am - 12:00am")
// when no morning alertness peak was detected. With one night of data the FIPS
// model produces a monotonically-rising curve, so we now use the global daily
// peak instead of a local morning peak.
func TestClassifyZones_BestFocusNeverZero(t *testing.T) {
	loc := time.UTC
	// Mirror the real user's data: short sleep, wake at 08:04.
	sleepStart := time.Date(2026, 3, 29, 3, 4, 0, 0, loc)
	wakeTime := time.Date(2026, 3, 29, 8, 4, 0, 0, loc)

	periods := []SleepPeriod{{Start: sleepStart, End: wakeTime}}
	points := PredictEnergy(DefaultParams(), periods, wakeTime, wakeTime.Add(24*time.Hour))
	schedule := ClassifyZones(points, wakeTime)

	if schedule.BestFocusStart.IsZero() {
		t.Fatal("BestFocusStart must never be zero")
	}
	if schedule.BestFocusEnd.IsZero() {
		t.Fatal("BestFocusEnd must never be zero")
	}
	if !schedule.BestFocusStart.After(wakeTime) {
		t.Errorf("BestFocusStart %v should be after wakeTime %v", schedule.BestFocusStart, wakeTime)
	}
	if !schedule.BestFocusEnd.After(schedule.BestFocusStart) {
		t.Errorf("BestFocusEnd %v should be after BestFocusStart %v", schedule.BestFocusEnd, schedule.BestFocusStart)
	}
}

func TestClassifyZones_EmptyPoints(t *testing.T) {
	schedule := ClassifyZones(nil, time.Now())
	if len(schedule.Points) != 0 {
		t.Error("Expected empty schedule for nil points")
	}
}

func TestClassifyZones_DerivedTimes(t *testing.T) {
	loc := time.UTC
	wakeTime := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)

	sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	periods := []SleepPeriod{{Start: sleepStart, End: wakeTime}}
	points := PredictEnergy(DefaultParams(), periods, wakeTime, wakeTime.Add(20*time.Hour))
	schedule := ClassifyZones(points, wakeTime)

	// BestFocusStart is derived from the model's actual alertness peak and
	// must always be set and after wake time.
	if schedule.BestFocusStart.IsZero() {
		t.Error("BestFocusStart should always be set")
	}
	if schedule.BestFocusStart.Before(wakeTime) {
		t.Error("BestFocusStart should be after wake time")
	}

	// Melatonin window and caffeine cutoff should always be derived.
	if schedule.MelatoninWindow.IsZero() {
		t.Error("Expected MelatoninWindow to be set")
	}
	if schedule.CaffeineCutoff.IsZero() {
		t.Error("Expected CaffeineCutoff to be set")
	}
}
