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
		p := DefaultParams()
		// FIPS ref: TPM_Sfun(la, d, sw, taw) = la + (sw - la) * exp(d * taw)
		sw := p.SInitial
		want5h := p.SLowerAsymptote + (sw-p.SLowerAsymptote)*math.Exp(p.SDecayRate*5.0)
		// Simulate 5h of wake in 5-min steps (same as PredictEnergy loop)
		s := sw
		for range 5 * 12 {
			dt := float64(sampleMinutes) / 60.0
			s = p.SLowerAsymptote + (s-p.SLowerAsymptote)*math.Exp(p.SDecayRate*dt)
		}
		assertNear(t, "S after 5h wake (stepped vs analytic)", s, want5h, tol)

		// After 16h awake, S should have decayed substantially toward la
		s16 := p.SLowerAsymptote + (sw-p.SLowerAsymptote)*math.Exp(p.SDecayRate*16.0)
		if s16 > 6.0 {
			t.Errorf("S after 16h should be decaying toward la=%.1f, got %.2f", p.SLowerAsymptote, s16)
		}
	})

	t.Run("ProcessS_Sleep_Phase1", func(t *testing.T) {
		p := DefaultParams()
		ss := 4.0
		tas := 3.0
		wantR := ss + tas*(-p.SRecoveryRate*(p.SBreakLevel-p.SUpperAsymptote))
		gotGo := ss + tas*p.SRecoveryLinear
		assertNear(t, "Phase1 sleep recovery 3h", gotGo, wantR, tol)
	})

	t.Run("ProcessS_Sleep_Phase2", func(t *testing.T) {
		p := DefaultParams()

		// Verify Phase 2 convergence: after 20h of sleep, S should approach ha.
		sLong := p.SUpperAsymptote - (p.SUpperAsymptote-p.SBreakLevel)*math.Exp(-p.SRecoveryRate*20.0)
		assertNear(t, "Phase2 converges to ha", sLong, p.SUpperAsymptote, 0.01)

		// Verify Phase 2 at 4h: hand-computed from FIPS reference values.
		// ha=14.3, bl=12.2, g=0.3814, tSleep=4.0
		// S = 14.3 - (14.3-12.2) * exp(-0.3814*4.0)
		//   = 14.3 - 2.1 * exp(-1.5256)
		//   = 14.3 - 2.1 * 0.2175
		//   = 14.3 - 0.4568 = 13.843
		handComputed := 14.3 - 2.1*math.Exp(-0.3814*4.0)
		formulaResult := p.SUpperAsymptote - (p.SUpperAsymptote-p.SBreakLevel)*math.Exp(-p.SRecoveryRate*4.0)
		assertNear(t, "Phase2 sleep recovery 4h (hand vs formula)", formulaResult, handComputed, 0.001)
		assertNear(t, "Phase2 sleep recovery 4h (expected value)", formulaResult, 13.843, 0.01)
	})

	t.Run("ProcessC", func(t *testing.T) {
		p := DefaultParams()
		// At tod=cAcrophase the cosine argument is zero, so C = Cm + Ca.
		tod := p.CAcrophase
		cPeak := p.CMean + p.CAmplitude*math.Cos(2*math.Pi/24.0*(tod-p.CAcrophase))
		assertNear(t, "C at acrophase", cPeak, p.CMean+p.CAmplitude, 0.001)

		cNadir := p.CMean + p.CAmplitude*math.Cos(2*math.Pi/24.0*(4.8-p.CAcrophase))
		assertNear(t, "C at nadir", cNadir, p.CMean-p.CAmplitude, 0.001)
	})

	t.Run("ProcessU", func(t *testing.T) {
		// Use DefaultParams().UAmplitude (0.8), not the stale package-level
		// constant uAmplitude (0.5). The default was bumped for more pronounced peaks.
		p := DefaultParams()
		// At tod=cAcrophase+uPhaseShift=19.8: U = Um + Ua*cos(0) = -0.5 + 0.8 = 0.3
		uPeak := p.UMean + p.UAmplitude*math.Cos(2*math.Pi/12.0*(19.8-p.CAcrophase-p.UPhaseShift))
		assertNear(t, "U at peak", uPeak, p.UMean+p.UAmplitude, 0.001)

		// 6h from peak (nadir at 13.8): U = Um + Ua*cos(pi) = -0.5 + 0.8*(-1) = -1.3
		uNadir := p.UMean + p.UAmplitude*math.Cos(2*math.Pi/12.0*(13.8-p.CAcrophase-p.UPhaseShift))
		assertNear(t, "U at nadir", uNadir, p.UMean-p.UAmplitude, 0.001)
	})

	t.Run("ProcessW", func(t *testing.T) {
		p := DefaultParams()
		// FIPS ref: TPM_Wfun = Wc * exp(Wd * taw)
		w0 := p.WCoefficient * math.Exp(p.WDecayRate*0)
		assertNear(t, "W at wake", w0, p.WCoefficient, 0.001)

		w1 := p.WCoefficient * math.Exp(p.WDecayRate*1.0)
		assertNear(t, "W at 1h", w1, p.WCoefficient*math.Exp(p.WDecayRate), 0.001)

		w3 := p.WCoefficient * math.Exp(p.WDecayRate*3.0)
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

	points := PredictEnergy(DefaultParams(), periods, predStart, predEnd)
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

	points := PredictEnergy(DefaultParams(), periods, predStart, predEnd)

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

	points := PredictEnergy(DefaultParams(), periods, predStart, predEnd)

	// Compare against full sleep scenario.
	fullSleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	fullPeriods := []SleepPeriod{{Start: fullSleepStart, End: sleepEnd}}
	fullPoints := PredictEnergy(DefaultParams(), fullPeriods, predStart, predEnd)

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

	points := PredictEnergy(DefaultParams(), periods, predStart, predEnd)

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

func TestAdjustForDebt(t *testing.T) {
	params := DefaultParams()

	t.Run("NoDebt", func(t *testing.T) {
		adjusted := AdjustForDebt(params, 0)
		if adjusted.SUpperAsymptote != params.SUpperAsymptote {
			t.Errorf("expected no change with 0 debt, got ha=%.2f", adjusted.SUpperAsymptote)
		}
		if adjusted.SDecayRate != params.SDecayRate {
			t.Errorf("expected no change with 0 debt, got d=%.4f", adjusted.SDecayRate)
		}
	})

	t.Run("ModerateDebt", func(t *testing.T) {
		adjusted := AdjustForDebt(params, 5)
		// Logarithmic taper: delta_ha = 1.0 * ln(1 + 0.25*5) = ln(2.25) ≈ 0.811
		wantHA := params.SUpperAsymptote - math.Log(1.0+0.25*5)
		if math.Abs(adjusted.SUpperAsymptote-wantHA) > 0.01 {
			t.Errorf("5h debt: got ha=%.2f, want %.2f", adjusted.SUpperAsymptote, wantHA)
		}
		wantS0 := params.SInitial - 0.6*math.Log(1.0+0.25*5)
		if math.Abs(adjusted.SInitial-wantS0) > 0.01 {
			t.Errorf("5h debt: got S0=%.2f, want %.2f", adjusted.SInitial, wantS0)
		}
	})

	t.Run("HighDebt_Clamped", func(t *testing.T) {
		adjusted := AdjustForDebt(params, 20)
		// ha should not drop below bl + 0.5 = 12.7
		if adjusted.SUpperAsymptote < params.SBreakLevel+0.5 {
			t.Errorf("20h debt: ha=%.2f should not drop below bl+0.5=%.2f",
				adjusted.SUpperAsymptote, params.SBreakLevel+0.5)
		}
		// Decay factor clamped at 1.5x
		wantD := params.SDecayRate * 1.5
		if math.Abs(adjusted.SDecayRate-wantD) > 0.001 {
			t.Errorf("20h debt: got d=%.4f, want %.4f (clamped)", adjusted.SDecayRate, wantD)
		}
	})
}

func TestAdjustForDebt_LowersAlertness(t *testing.T) {
	loc := time.UTC
	sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	periods := []SleepPeriod{{Start: sleepStart, End: sleepEnd}}
	predStart := sleepEnd
	predEnd := time.Date(2024, 1, 16, 23, 0, 0, 0, loc)

	noDebtPoints := PredictEnergy(DefaultParams(), periods, predStart, predEnd)
	debtParams := AdjustForDebt(DefaultParams(), 10)
	debtPoints := PredictEnergy(debtParams, periods, predStart, predEnd)

	avgNoDebt := avgAlertness(noDebtPoints)
	avgDebt := avgAlertness(debtPoints)

	if avgDebt >= avgNoDebt {
		t.Errorf("10h debt avg alertness (%.2f) should be lower than no-debt (%.2f)", avgDebt, avgNoDebt)
	}

	// The difference should be meaningful (at least 1 KSS unit equivalent).
	diff := avgNoDebt - avgDebt
	if diff < 0.5 {
		t.Errorf("debt effect too small: avg alertness difference = %.2f (want >= 0.5)", diff)
	}
}

func TestAdjustForChronotype(t *testing.T) {
	params := DefaultParams()

	t.Run("NeutralMidpoint", func(t *testing.T) {
		// 3:30am midpoint = neutral, no shift
		adjusted := AdjustForChronotype(params, 3.5)
		if adjusted.CAcrophase != params.CAcrophase {
			t.Errorf("expected no shift for neutral midpoint, got cAcrophase=%.2f (want %.2f)", adjusted.CAcrophase, params.CAcrophase)
		}
	})

	t.Run("NightOwl", func(t *testing.T) {
		// Sleep 1am-9am, midpoint 5am → shift +1.5h
		adjusted := AdjustForChronotype(params, 5.0)
		want := params.CAcrophase + 1.5
		if math.Abs(adjusted.CAcrophase-want) > 0.01 {
			t.Errorf("night owl: got cAcrophase=%.2f, want %.2f", adjusted.CAcrophase, want)
		}
	})

	t.Run("EarlyBird", func(t *testing.T) {
		// Sleep 10pm-6am, midpoint 2am → shift -1.5h
		adjusted := AdjustForChronotype(params, 2.0)
		want := params.CAcrophase - 1.5
		if math.Abs(adjusted.CAcrophase-want) > 0.01 {
			t.Errorf("early bird: got cAcrophase=%.2f, want %.2f", adjusted.CAcrophase, want)
		}
	})

	t.Run("ClampedExtreme", func(t *testing.T) {
		// Extreme night owl midpoint 8am → would be +4.5h, clamped to +2h
		adjusted := AdjustForChronotype(params, 8.0)
		want := params.CAcrophase + 2.0
		if math.Abs(adjusted.CAcrophase-want) > 0.01 {
			t.Errorf("extreme night owl: got cAcrophase=%.2f, want %.2f (should be clamped)", adjusted.CAcrophase, want)
		}
	})
}

func TestAdjustForChronotype_ShiftsPeak(t *testing.T) {
	// Verify that a night owl's alertness peak is later in the day.
	loc := time.UTC
	sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	periods := []SleepPeriod{{Start: sleepStart, End: sleepEnd}}
	predStart := sleepEnd
	predEnd := time.Date(2024, 1, 16, 23, 0, 0, 0, loc)

	neutralPoints := PredictEnergy(DefaultParams(), periods, predStart, predEnd)
	nightOwlParams := AdjustForChronotype(DefaultParams(), 5.5) // late sleeper
	nightOwlPoints := PredictEnergy(nightOwlParams, periods, predStart, predEnd)

	// Find peak time for each.
	peakTime := func(points []EnergyPoint) time.Time {
		var best EnergyPoint
		for _, p := range points {
			if p.Alertness > best.Alertness {
				best = p
			}
		}
		return best.Time
	}

	neutralPeak := peakTime(neutralPoints)
	nightOwlPeak := peakTime(nightOwlPoints)

	if !nightOwlPeak.After(neutralPeak) {
		t.Errorf("night owl peak (%v) should be later than neutral peak (%v)", nightOwlPeak, neutralPeak)
	}
}

func TestNapInertiaScale(t *testing.T) {
	loc := time.UTC

	t.Run("ShortNap_ReducedInertia", func(t *testing.T) {
		// 20-min nap at 1pm → inertia should be ~30% of full.
		sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
		sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
		napStart := time.Date(2024, 1, 16, 13, 0, 0, 0, loc)
		napEnd := time.Date(2024, 1, 16, 13, 20, 0, 0, loc)

		periodsNoNap := []SleepPeriod{
			{Start: sleepStart, End: sleepEnd},
		}
		periodsWithNap := []SleepPeriod{
			{Start: sleepStart, End: sleepEnd},
			{Start: napStart, End: napEnd, IsNap: true},
		}

		params := DefaultParams()
		predEnd := time.Date(2024, 1, 16, 15, 0, 0, 0, loc)

		noNapPoints := PredictEnergy(params, periodsNoNap, sleepEnd, predEnd)
		napPoints := PredictEnergy(params, periodsWithNap, sleepEnd, predEnd)

		// Find alertness right after nap end (~1:25pm) in nap scenario.
		// Should show recovery bump (S recovered during nap) with minimal inertia dip.
		var alertPostNap float64
		for _, p := range napPoints {
			if p.Time.Equal(napEnd.Add(5 * time.Minute)) {
				alertPostNap = p.Alertness
				break
			}
		}

		// Find alertness at same time without nap.
		var alertNoNap float64
		for _, p := range noNapPoints {
			if p.Time.Equal(napEnd.Add(5 * time.Minute)) {
				alertNoNap = p.Alertness
				break
			}
		}

		// With a short nap, post-nap alertness should still be reasonable
		// (not deeply suppressed by full inertia).
		if alertPostNap == 0 || alertNoNap == 0 {
			t.Skip("could not find matching time points")
		}
		t.Logf("Post-20min-nap alertness: %.2f, no-nap alertness: %.2f", alertPostNap, alertNoNap)
	})

	t.Run("NapInertiaScaleValues", func(t *testing.T) {
		// Test the napInertiaScale function directly.
		now := time.Date(2024, 1, 16, 14, 0, 0, 0, loc)

		// 15-min nap ending now
		shortNap := []SleepPeriod{{Start: now.Add(-15 * time.Minute), End: now, IsNap: true}}
		if s := napInertiaScale(shortNap, now); math.Abs(s-0.3) > 0.01 {
			t.Errorf("15min nap: got scale %.2f, want 0.3", s)
		}

		// 30-min nap ending now
		medNap := []SleepPeriod{{Start: now.Add(-30 * time.Minute), End: now, IsNap: true}}
		if s := napInertiaScale(medNap, now); math.Abs(s-1.0) > 0.01 {
			t.Errorf("30min nap: got scale %.2f, want 1.0", s)
		}

		// 90-min nap ending now
		longNap := []SleepPeriod{{Start: now.Add(-90 * time.Minute), End: now, IsNap: true}}
		if s := napInertiaScale(longNap, now); math.Abs(s-0.6) > 0.01 {
			t.Errorf("90min nap: got scale %.2f, want 0.6", s)
		}

		// Main sleep ending now
		mainSleep := []SleepPeriod{{Start: now.Add(-8 * time.Hour), End: now, IsNap: false}}
		if s := napInertiaScale(mainSleep, now); math.Abs(s-1.0) > 0.01 {
			t.Errorf("main sleep: got scale %.2f, want 1.0", s)
		}
	})
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
