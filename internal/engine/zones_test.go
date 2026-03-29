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

	points := PredictEnergy(periods, predStart, predEnd)
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
	points := PredictEnergy(periods, wakeTime, wakeTime.Add(20*time.Hour))
	schedule := ClassifyZones(points, wakeTime)

	// Best focus should be set.
	if schedule.BestFocusStart.IsZero() {
		t.Error("Expected BestFocusStart to be set")
	}
	if schedule.BestFocusEnd.IsZero() {
		t.Error("Expected BestFocusEnd to be set")
	}

	// Focus window should be after wake time.
	if schedule.BestFocusStart.Before(wakeTime) {
		t.Error("BestFocusStart should be after wake time")
	}
}
