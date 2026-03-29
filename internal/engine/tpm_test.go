package engine

import (
	"math"
	"testing"
	"time"
)

func TestPredictEnergy_BasicWake(t *testing.T) {
	// Scenario: 8 hours of sleep (11pm-7am), predict 7am-11pm.
	loc := time.UTC
	sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)

	periods := []SleepPeriod{{Start: sleepStart, End: sleepEnd}}
	predStart := sleepEnd
	predEnd := time.Date(2024, 1, 16, 23, 0, 0, 0, loc)

	points := PredictEnergy(periods, predStart, predEnd)

	if len(points) == 0 {
		t.Fatal("Expected non-empty energy points")
	}

	// Verify we get the expected number of points (16h * 12 points/hr = 192).
	expected := 16 * 60 / sampleMinutes
	if len(points) != expected {
		t.Errorf("Expected %d points, got %d", expected, len(points))
	}

	// First point should show sleep inertia (lower alertness).
	first := points[0]
	if first.Alertness > 12.0 {
		t.Errorf("Expected reduced alertness from sleep inertia, got %.2f", first.Alertness)
	}

	// Find peak alertness — should be higher than the first point.
	var maxAlertness float64
	for _, p := range points {
		if p.Alertness > maxAlertness {
			maxAlertness = p.Alertness
		}
	}
	if maxAlertness <= first.Alertness {
		t.Errorf("Expected peak alertness > first point: peak=%.2f, first=%.2f", maxAlertness, first.Alertness)
	}

	// KSS values should be in valid range [1, 9].
	for _, p := range points {
		if p.KSS < 1 || p.KSS > 9 {
			t.Errorf("KSS out of range at %v: %.2f", p.Time, p.KSS)
		}
	}
}

func TestPredictEnergy_SleepDeprivation(t *testing.T) {
	// Scenario: only 4 hours of sleep (3am-7am). Alertness should be lower overall.
	loc := time.UTC
	sleepStart := time.Date(2024, 1, 16, 3, 0, 0, 0, loc)
	sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)

	periods := []SleepPeriod{{Start: sleepStart, End: sleepEnd}}
	predStart := sleepEnd
	predEnd := time.Date(2024, 1, 16, 23, 0, 0, 0, loc)

	points := PredictEnergy(periods, predStart, predEnd)

	// Compare against full sleep scenario.
	fullSleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	fullPeriods := []SleepPeriod{{Start: fullSleepStart, End: sleepEnd}}
	fullPoints := PredictEnergy(fullPeriods, predStart, predEnd)

	// Average alertness should be lower with less sleep.
	avgShort := avgAlertness(points)
	avgFull := avgAlertness(fullPoints)

	if avgShort >= avgFull {
		t.Errorf("Expected lower average alertness with 4h sleep (%.2f) vs 8h (%.2f)", avgShort, avgFull)
	}
}

func TestPredictEnergy_CircadianRhythm(t *testing.T) {
	// Verify the circadian rhythm creates peaks and troughs.
	loc := time.UTC
	sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)

	periods := []SleepPeriod{{Start: sleepStart, End: sleepEnd}}
	predStart := sleepEnd
	predEnd := time.Date(2024, 1, 16, 23, 0, 0, 0, loc)

	points := PredictEnergy(periods, predStart, predEnd)

	// Verify the circadian + ultradian components create a visible dip.
	// The afternoon dip should have lower alertness than the morning peak.
	// Note: Due to homeostatic decay, the evening peak may be lower than morning,
	// so we only check morning peak > afternoon dip.
	var morningMax, afternoonMin float64
	morningMax = -100
	afternoonMin = 100

	for _, p := range points {
		hour := p.Time.Hour()
		switch {
		case hour >= 9 && hour <= 11:
			morningMax = math.Max(morningMax, p.Alertness)
		case hour >= 13 && hour <= 15:
			afternoonMin = math.Min(afternoonMin, p.Alertness)
		}
	}

	if afternoonMin >= morningMax {
		t.Errorf("Expected afternoon dip (%.2f) < morning peak (%.2f)", afternoonMin, morningMax)
	}
}

func TestPredictEnergy_EmptyPeriods(t *testing.T) {
	predStart := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)
	predEnd := time.Date(2024, 1, 16, 8, 0, 0, 0, time.UTC)

	points := PredictEnergy(nil, predStart, predEnd)
	if len(points) == 0 {
		t.Fatal("Should still produce points even with no sleep history")
	}
}

func TestPredictEnergy_InvalidRange(t *testing.T) {
	start := time.Date(2024, 1, 16, 8, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)

	points := PredictEnergy(nil, start, end)
	if points != nil {
		t.Error("Expected nil for invalid time range")
	}
}

func avgAlertness(points []EnergyPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	var sum float64
	for _, p := range points {
		sum += p.Alertness
	}
	return sum / float64(len(points))
}
