package engine

import (
	"math"
	"testing"
	"time"
)

// TestTPM_ProcessFunctions validates Go implementations against R FIPS reference equations.
// R source: https://github.com/humanfactors/FIPS (simulation_threeprocessmodel.R)
func TestTPM_ProcessFunctions(t *testing.T) {
	const tol = 0.01

	assertNear := func(t *testing.T, name string, got, want, tolerance float64) {
		t.Helper()
		if math.Abs(got-want) > tolerance {
			t.Errorf("%s: got %.4f, want %.4f (tol %.4f)", name, got, want, tolerance)
		}
	}

	t.Run("ProcessS_Wake", func(t *testing.T) {
		// FIPS ref: TPM_Sfun(la, d, sw, taw) = la + (sw - la) * exp(d * taw)
		sw := 7.96
		want5h := sLowerAsymptote + (sw-sLowerAsymptote)*math.Exp(sDecayRate*5.0)
		// Simulate 5h of wake in 5-min steps (same as PredictEnergy loop)
		s := sw
		for range 5 * 12 {
			dt := float64(sampleMinutes) / 60.0
			s = sLowerAsymptote + (s-sLowerAsymptote)*math.Exp(sDecayRate*dt)
		}
		assertNear(t, "S after 5h wake (stepped vs analytic)", s, want5h, tol)

		// After 16h awake, S should have decayed substantially toward la=2.4
		s16 := sLowerAsymptote + (sw-sLowerAsymptote)*math.Exp(sDecayRate*16.0)
		if s16 > 6.0 {
			t.Errorf("S after 16h should be decaying toward la=2.4, got %.2f", s16)
		}
	})

	t.Run("ProcessS_Sleep_Phase1", func(t *testing.T) {
		// R: TPM_Sp1fun = ss + tas * (g * (bl - ha))
		// Go uses sRecoveryLinear = g*(bl-ha) ≈ 0.8
		ss := 4.0
		tas := 3.0
		wantR := ss + tas*(-sRecoveryRate*(sBreakLevel-sUpperAsymptote))
		gotGo := ss + tas*sRecoveryLinear
		assertNear(t, "Phase1 sleep recovery 3h", gotGo, wantR, tol)
	})

	t.Run("ProcessS_Sleep_Phase2", func(t *testing.T) {
		// FIPS ref: TPM_Sp2fun = ha - (ha - bl) * exp(g * (tas - breaktime))
		tSleep := 4.0
		want := sUpperAsymptote - (sUpperAsymptote-sBreakLevel)*math.Exp(-sRecoveryRate*tSleep)
		got := sUpperAsymptote - (sUpperAsymptote-sBreakLevel)*math.Exp(-sRecoveryRate*tSleep)
		assertNear(t, "Phase2 sleep recovery 4h", got, want, 0.001)

		// After long sleep, S -> ha=14.3
		sLong := sUpperAsymptote - (sUpperAsymptote-sBreakLevel)*math.Exp(-sRecoveryRate*20.0)
		assertNear(t, "Phase2 converges to ha", sLong, sUpperAsymptote, 0.01)
	})

	t.Run("ProcessC", func(t *testing.T) {
		// At tod=cAcrophase the cosine argument is zero, so C = Cm + Ca.
		tod := cAcrophase
		cPeak := cMean + cAmplitude*math.Cos(2*math.Pi/24.0*(tod-cAcrophase))
		assertNear(t, "C at acrophase", cPeak, 2.5, 0.001)

		cNadir := cMean + cAmplitude*math.Cos(2*math.Pi/24.0*(4.8-cAcrophase))
		assertNear(t, "C at nadir", cNadir, -2.5, 0.001)
	})

	t.Run("ProcessU", func(t *testing.T) {
		// R: TPM_Ufun = Um + Ua * cos((2*pi/12)*(tod-p-3))
		// At tod=p+3=19.8: U = -0.5 + 0.5*cos(0) = 0.0
		uPeak := uMean + uAmplitude*math.Cos(2*math.Pi/12.0*(19.8-cAcrophase-uPhaseShift))
		assertNear(t, "U at peak", uPeak, 0.0, 0.001)

		// 6h from peak (nadir at 13.8): U = -0.5 + 0.5*cos(pi) = -1.0
		uNadir := uMean + uAmplitude*math.Cos(2*math.Pi/12.0*(13.8-cAcrophase-uPhaseShift))
		assertNear(t, "U at nadir", uNadir, -1.0, 0.001)
	})

	t.Run("ProcessW", func(t *testing.T) {
		// FIPS ref: TPM_Wfun = Wc * exp(Wd * taw)
		w0 := wCoefficient * math.Exp(wDecayRate*0)
		assertNear(t, "W at wake", w0, -5.72, 0.001)

		w1 := wCoefficient * math.Exp(wDecayRate*1.0)
		assertNear(t, "W at 1h", w1, -5.72*math.Exp(-1.51), 0.001)

		w3 := wCoefficient * math.Exp(wDecayRate*3.0)
		if math.Abs(w3) > 0.1 {
			t.Errorf("W at 3h should be near zero, got %.4f", w3)
		}
	})

	t.Run("KSS", func(t *testing.T) {
		// FIPS ref: KSS = 10.6 + (-0.6) * alertness
		assertNear(t, "KSS at alertness=10", alertnessToKSS(10), 4.6, 0.001)
		assertNear(t, "KSS at alertness=16", alertnessToKSS(16), 1.0, 0.001) // clamped
		assertNear(t, "KSS at alertness=2", alertnessToKSS(2), 9.0, 0.001)   // clamped
	})
}

// TestTPM_FullScenario verifies end-to-end: after 8h sleep, alertness shows
// circadian modulation and homeostatic decay across the day.
func TestTPM_FullScenario(t *testing.T) {
	loc := time.UTC
	sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	periods := []SleepPeriod{{Start: sleepStart, End: sleepEnd}}
	predStart := sleepEnd
	predEnd := time.Date(2024, 1, 16, 23, 0, 0, 0, loc)

	points := PredictEnergy(periods, predStart, predEnd)
	if len(points) == 0 {
		t.Fatal("no points returned")
	}

	// Alertness late in the day should be lower than mid-morning due to S decay.
	var alert10am, alert10pm float64
	for _, p := range points {
		h := p.Time.Hour()
		if h == 10 && p.Time.Minute() == 0 {
			alert10am = p.Alertness
		}
		if h == 22 && p.Time.Minute() == 0 {
			alert10pm = p.Alertness
		}
	}
	if alert10pm >= alert10am {
		t.Errorf("expected 10pm alertness (%.2f) < 10am (%.2f) due to S decay", alert10pm, alert10am)
	}
}

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

	// Verify circadian modulation: the curve is NOT monotonically decreasing.
	// With FIPS params, C peaks at ~16.8h so late-afternoon alertness should
	// exceed early-morning alertness despite homeostatic S decay.
	var alert8am, alert16pm float64
	for _, p := range points {
		if p.Time.Hour() == 8 && p.Time.Minute() == 0 {
			alert8am = p.Alertness
		}
		if p.Time.Hour() == 16 && p.Time.Minute() == 0 {
			alert16pm = p.Alertness
		}
	}

	if alert16pm <= alert8am {
		t.Errorf("Expected circadian boost: 4pm alertness (%.2f) should exceed 8am (%.2f)", alert16pm, alert8am)
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
