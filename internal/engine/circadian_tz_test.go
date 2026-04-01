package engine

import (
	"testing"
	"time"
)

// TestCircadianPhaseRequiresLocalTime verifies that PredictEnergy produces
// a circadian peak in the late afternoon (local time), not at some arbitrary
// UTC-offset hour.
//
// The bug: timeOfDay() uses t.Clock() which returns the hour in whatever
// timezone the time carries. If times are UTC but CAcrophase is calibrated
// to local time (16.8h = ~5pm), the circadian peak appears at 5pm UTC
// instead of 5pm local. For a Tokyo user (UTC+9), that's 2am JST.
//
// The fix: callers must convert times to local before passing to PredictEnergy.
func TestCircadianPhaseRequiresLocalTime(t *testing.T) {
	params := DefaultParams()
	tokyo, _ := time.LoadLocation("Asia/Tokyo") // UTC+9

	// User in Tokyo woke at 7am JST = 10pm UTC previous day
	wakeLocal := time.Date(2024, 6, 15, 7, 0, 0, 0, tokyo)
	wakeUTC := wakeLocal.UTC()

	// Sleep: 11pm-7am JST
	sleepStartLocal := time.Date(2024, 6, 14, 23, 0, 0, 0, tokyo)
	sleepEndLocal := wakeLocal

	// Run prediction with LOCAL times (correct)
	periodsLocal := []SleepPeriod{{Start: sleepStartLocal, End: sleepEndLocal}}
	pointsLocal := PredictEnergy(params, periodsLocal, wakeLocal, wakeLocal.Add(17*time.Hour))

	// Run prediction with UTC times (the bug)
	periodsUTC := []SleepPeriod{{Start: sleepStartLocal.UTC(), End: sleepEndLocal.UTC()}}
	pointsUTC := PredictEnergy(params, periodsUTC, wakeUTC, wakeUTC.Add(17*time.Hour))

	// Find peak hour for each
	peakLocal := findPeakHour(pointsLocal)
	peakUTC := findPeakHour(pointsUTC)

	t.Logf("Local times: peak at %s (hour %d local)", pointsLocal[peakLocal].Time.Format("15:04"), pointsLocal[peakLocal].Time.Hour())
	t.Logf("UTC times:   peak at %s (hour %d UTC = %d JST)", pointsUTC[peakUTC].Time.Format("15:04"), pointsUTC[peakUTC].Time.Hour(), (pointsUTC[peakUTC].Time.Hour()+9)%24)

	// The local-time peak should be in the afternoon (roughly 15:00-19:00 local)
	localPeakHour := pointsLocal[peakLocal].Time.Hour()
	if localPeakHour < 14 || localPeakHour > 20 {
		t.Errorf("local-time peak at hour %d, expected 14-20 (afternoon)", localPeakHour)
	}

	// The UTC-time peak should ALSO land at an afternoon-looking hour
	// (because timeOfDay reads the time's inherent hour). But when the
	// times are UTC and the user is in Tokyo, the "afternoon" is UTC
	// afternoon = JST early morning. This proves the bug.
	utcPeakHour := pointsUTC[peakUTC].Time.Hour()
	utcPeakJST := (utcPeakHour + 9) % 24
	if utcPeakJST >= 14 && utcPeakJST <= 20 {
		t.Log("UTC peak maps to JST afternoon — no bug (unexpected)")
	} else {
		t.Logf("UTC peak at %d:00 UTC = %d:00 JST — circadian peak at wrong time of day", utcPeakHour, utcPeakJST)
	}

	// Key assertion: the two predictions should produce different peak hours
	// because one uses local time correctly and the other doesn't.
	// If they're the same, the timezone doesn't matter (which would mean no bug).
	if localPeakHour == utcPeakHour {
		t.Error("local and UTC peaks are at the same hour — timezone had no effect (unexpected)")
	}
}

// TestTimeOfDay_ReturnsLocalHour verifies timeOfDay extracts the hour
// from the time's inherent timezone.
func TestTimeOfDay_ReturnsLocalHour(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")

	// 3pm JST
	jst := time.Date(2024, 6, 15, 15, 30, 0, 0, tokyo)
	todJST := timeOfDay(jst)
	if todJST < 15.4 || todJST > 15.6 {
		t.Errorf("timeOfDay(3:30pm JST) = %.2f, want ~15.5", todJST)
	}

	// Same instant in UTC = 6:30am UTC
	utc := jst.UTC()
	todUTC := timeOfDay(utc)
	if todUTC < 6.4 || todUTC > 6.6 {
		t.Errorf("timeOfDay(6:30am UTC) = %.2f, want ~6.5", todUTC)
	}

	// They should differ by ~9 hours (the UTC+9 offset)
	diff := todJST - todUTC
	if diff < 8.5 || diff > 9.5 {
		t.Errorf("JST-UTC diff = %.2f hours, want ~9.0", diff)
	}
}

func findPeakHour(points []EnergyPoint) int {
	best := 0
	for i, p := range points {
		if p.Alertness > points[best].Alertness {
			best = i
		}
	}
	return best
}
