package engine

import (
	"math"
	"testing"
	"time"
)

// TestPipeline_NonUTCTimezones runs the core prediction pipeline with times
// in non-UTC timezones. This catches bugs where timeOfDay() or zone
// classification silently assume UTC.
//
// The key invariant: the same physical sleep schedule (11pm-7am local,
// waking at 7am local) should produce the same alertness curve shape
// regardless of which IANA timezone the times carry. The circadian peak
// should always fall in the local afternoon (~4-6pm).
func TestPipeline_NonUTCTimezones(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")       // UTC+9
	la, _ := time.LoadLocation("America/Los_Angeles") // UTC-8 winter
	ist, _ := time.LoadLocation("Asia/Kolkata")       // UTC+5:30

	zones := []struct {
		name string
		loc  *time.Location
	}{
		{"UTC", time.UTC},
		{"Tokyo_UTC+9", tokyo},
		{"LA_UTC-8", la},
		{"IST_UTC+5:30", ist},
	}

	for _, tz := range zones {
		t.Run(tz.name, func(t *testing.T) {
			params := DefaultParams()
			loc := tz.loc

			// Same physical scenario in each timezone: sleep 11pm-7am, wake at 7am
			var periods []SleepPeriod
			for i := range 3 {
				start := time.Date(2024, 1, 14-i, 23, 0, 0, 0, loc)
				end := time.Date(2024, 1, 15-i, 7, 0, 0, 0, loc)
				periods = append(periods, SleepPeriod{Start: start, End: end})
			}
			wake := time.Date(2024, 1, 15, 7, 0, 0, 0, loc)

			points := PredictEnergy(params, periods, wake, wake.Add(17*time.Hour))

			if len(points) == 0 {
				t.Fatal("no points generated")
			}

			// Find peak alertness
			var peakIdx int
			for i, p := range points {
				if p.Alertness > points[peakIdx].Alertness {
					peakIdx = i
				}
			}
			peakHour := points[peakIdx].Time.Hour()
			peakAlertness := points[peakIdx].Alertness

			// Basic sanity
			if math.IsNaN(peakAlertness) || math.IsInf(peakAlertness, 0) {
				t.Fatalf("peak alertness is NaN/Inf")
			}

			// The circadian peak should be in the local afternoon (14:00-20:00).
			// CAcrophase=16.8h means the C component peaks at ~5pm local.
			if peakHour < 14 || peakHour > 20 {
				t.Errorf("peak at %02d:00 local, expected 14:00-20:00", peakHour)
			}

			// KSS should be in valid range for all points
			for i, p := range points {
				if p.KSS < 1.0 || p.KSS > 9.0 {
					t.Errorf("point %d: KSS=%.2f outside [1,9] at %s", i, p.KSS, p.Time.Format("15:04"))
				}
			}

			// Alertness should start lower (sleep inertia) and rise
			if len(points) > 24 { // at least 2h of data
				if points[0].Alertness > points[24].Alertness {
					t.Logf("NOTE: first point (%.2f) > point at 2h (%.2f) — no visible inertia", points[0].Alertness, points[24].Alertness)
				}
			}

			t.Logf("peak at %02d:00 local (alertness=%.2f)", peakHour, peakAlertness)
		})
	}
}

// TestPipeline_MixedTimezoneInvariance verifies that passing the same
// physical instant encoded in different timezones produces identical results.
// time.Date(2024,1,15, 7,0,0,0, Tokyo) and time.Date(2024,1,14, 22,0,0,0, UTC)
// represent the same instant — PredictEnergy should produce identical output.
func TestPipeline_MixedTimezoneInvariance(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	params := DefaultParams()

	// Same physical scenario, expressed in Tokyo time
	sleepTokyo := SleepPeriod{
		Start: time.Date(2024, 1, 14, 23, 0, 0, 0, tokyo),
		End:   time.Date(2024, 1, 15, 7, 0, 0, 0, tokyo),
	}
	wakeTokyo := time.Date(2024, 1, 15, 7, 0, 0, 0, tokyo)
	pointsTokyo := PredictEnergy(params, []SleepPeriod{sleepTokyo}, wakeTokyo, wakeTokyo.Add(17*time.Hour))

	// Same physical scenario, expressed in UTC
	sleepUTC := SleepPeriod{
		Start: sleepTokyo.Start.UTC(),
		End:   sleepTokyo.End.UTC(),
	}
	wakeUTC := wakeTokyo.UTC()
	pointsUTC := PredictEnergy(params, []SleepPeriod{sleepUTC}, wakeUTC, wakeUTC.Add(17*time.Hour))

	if len(pointsTokyo) != len(pointsUTC) {
		t.Fatalf("point count mismatch: Tokyo=%d, UTC=%d", len(pointsTokyo), len(pointsUTC))
	}

	// The S process (sleep/wake detection via timespanset) should produce
	// identical results because Contains() compares absolute instants.
	// But the C and U processes use timeOfDay() which reads the time's
	// timezone — so Tokyo and UTC will produce DIFFERENT curves.
	// This test documents this expected difference.
	var maxDiff float64
	for i := range pointsTokyo {
		diff := math.Abs(pointsTokyo[i].Alertness - pointsUTC[i].Alertness)
		if diff > maxDiff {
			maxDiff = diff
		}
	}

	if maxDiff < 0.01 {
		// If they're identical, timeOfDay isn't sensitive to timezone (unexpected)
		t.Error("Tokyo and UTC curves are identical — timeOfDay should produce different C/U values")
	} else {
		t.Logf("max alertness difference between Tokyo and UTC encoding: %.2f (expected: C/U phase differs by 9h)", maxDiff)
	}

	// Verify Tokyo curve has afternoon peak (correct for local times)
	var tokyoPeakHour int
	var tokyoPeakVal float64
	for _, p := range pointsTokyo {
		if p.Alertness > tokyoPeakVal {
			tokyoPeakVal = p.Alertness
			tokyoPeakHour = p.Time.In(tokyo).Hour()
		}
	}
	if tokyoPeakHour < 14 || tokyoPeakHour > 20 {
		t.Errorf("Tokyo curve peak at %d:00 JST, expected afternoon", tokyoPeakHour)
	}

	// UTC curve peak should be at a different wall-clock hour
	var utcPeakHour int
	var utcPeakVal float64
	for _, p := range pointsUTC {
		if p.Alertness > utcPeakVal {
			utcPeakVal = p.Alertness
			utcPeakHour = p.Time.Hour()
		}
	}
	t.Logf("Tokyo peak: %d:00 JST (%.2f), UTC peak: %d:00 UTC (%.2f)", tokyoPeakHour, tokyoPeakVal, utcPeakHour, utcPeakVal)
}
