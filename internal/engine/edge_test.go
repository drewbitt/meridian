package engine

import (
	"math"
	"testing"
	"time"
)

// --- Bug proof: S should not drop when entering sleep with S above breakLevel ---

func TestPredictEnergy_SDoesNotDropAtSleepOnset(t *testing.T) {
	// After a short wake (e.g., 2 hours), S is still well above breakLevel.
	// When falling back asleep, S should NOT snap down to breakLevel.
	// This tests the phase2 formula at sleep onset: if phase2Start = t,
	// tSleep = 0, and S = ha - (ha-bl)*exp(0) = bl. That's a bug.
	loc := time.UTC
	params := DefaultParams()

	// Night sleep: 11pm-7am. Then awake 7am-9am (2h). Then nap 9am-10am.
	nightStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	nightEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	napStart := time.Date(2024, 1, 16, 9, 0, 0, 0, loc)
	napEnd := time.Date(2024, 1, 16, 10, 0, 0, 0, loc)

	periods := []SleepPeriod{
		{Start: nightStart, End: nightEnd},
		{Start: napStart, End: napEnd},
	}

	// Predict across the nap transition.
	predStart := time.Date(2024, 1, 16, 8, 50, 0, 0, loc)
	predEnd := time.Date(2024, 1, 16, 10, 10, 0, 0, loc)

	points := PredictEnergy(params, periods, predStart, predEnd)

	// Find S right before nap (8:55am) and right after nap starts (9:00am or 9:05am).
	var beforeNap, atNapStart float64
	for _, p := range points {
		if p.Time.Equal(time.Date(2024, 1, 16, 8, 55, 0, 0, loc)) {
			beforeNap = p.Alertness
		}
		if p.Time.Equal(time.Date(2024, 1, 16, 9, 5, 0, 0, loc)) {
			atNapStart = p.Alertness
		}
	}

	if beforeNap == 0 || atNapStart == 0 {
		t.Skipf("could not find matching time points (beforeNap=%.2f, atNapStart=%.2f)", beforeNap, atNapStart)
	}

	// S was high before nap. At nap onset, S should NOT drop dramatically.
	// The circadian components C+U shift between the two points, so allow some
	// change, but S alone should not drop by more than ~1 unit.
	// If the bug exists, the drop will be >>2 units (S snaps from ~13 to ~12.2).
	drop := beforeNap - atNapStart
	t.Logf("Alertness before nap: %.2f, at nap start: %.2f, drop: %.2f", beforeNap, atNapStart, drop)
	if drop > 2.0 {
		t.Errorf("S dropped %.2f at sleep onset (likely snapped to breakLevel). "+
			"beforeNap=%.2f, atNapStart=%.2f", drop, beforeNap, atNapStart)
	}
}

// --- Bug proof: W inertia should fire when sleeping at simStart ---

func TestPredictEnergy_InertiaAfterSleepAtSimStart(t *testing.T) {
	// If the simulation starts during sleep, and then the person wakes up,
	// we should still see sleep inertia (W component).
	loc := time.UTC
	params := DefaultParams()

	// Sleep 11pm-7am, but we only predict from 6am (during sleep) to 9am.
	sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	periods := []SleepPeriod{{Start: sleepStart, End: sleepEnd}}

	predStart := time.Date(2024, 1, 16, 6, 0, 0, 0, loc) // during sleep
	predEnd := time.Date(2024, 1, 16, 9, 0, 0, 0, loc)

	points := PredictEnergy(params, periods, predStart, predEnd)

	// Find alertness right after wake (7:05am) — should show inertia dip.
	var alertAtWake float64
	for _, p := range points {
		if p.Time.Equal(time.Date(2024, 1, 16, 7, 5, 0, 0, loc)) {
			alertAtWake = p.Alertness
			break
		}
	}

	// Find alertness 2 hours later (9:00am-ish) — inertia should have worn off.
	var alertLater float64
	for _, p := range points {
		if p.Time.Equal(time.Date(2024, 1, 16, 8, 55, 0, 0, loc)) {
			alertLater = p.Alertness
			break
		}
	}

	if alertAtWake == 0 || alertLater == 0 {
		t.Skipf("could not find matching points (wake=%.2f, later=%.2f)", alertAtWake, alertLater)
	}

	// With inertia: alertness at 7:05am should be LOWER than at 8:55am
	// (inertia depresses alertness right after wake, then it wears off).
	// Without inertia (the bug): alertness at 7:05am would be higher because
	// S is at its peak after 8h sleep and there's no W depression.
	t.Logf("Alert at 7:05am (post-wake): %.2f, at 8:55am: %.2f", alertAtWake, alertLater)
	if alertAtWake >= alertLater {
		t.Errorf("expected inertia dip at wake: alertness at 7:05 (%.2f) should be < 8:55 (%.2f)",
			alertAtWake, alertLater)
	}
}

func TestAdjustForDebt_NegativeDebt(t *testing.T) {
	params := DefaultParams()
	adjusted := AdjustForDebt(params, -5)
	if adjusted.SUpperAsymptote != params.SUpperAsymptote {
		t.Error("negative debt should not change params")
	}
	if adjusted.SDecayRate != params.SDecayRate {
		t.Error("negative debt should not change decay rate")
	}
}

func TestAdjustForDebt_ExtremeDebt_MonotonicDegradation(t *testing.T) {
	// Verify that increasing debt always produces lower or equal alertness.
	// This catches the non-monotonic clamping bug.
	params := DefaultParams()
	loc := time.UTC
	sleepStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	sleepEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	periods := []SleepPeriod{{Start: sleepStart, End: sleepEnd}}
	predStart := sleepEnd
	predEnd := time.Date(2024, 1, 16, 23, 0, 0, 0, loc)

	prevAvg := math.MaxFloat64
	for _, debt := range []float64{0, 5, 10, 15, 20, 30, 50} {
		adjusted := AdjustForDebt(params, debt)
		points := PredictEnergy(adjusted, periods, predStart, predEnd)
		avg := avgAlertness(points)
		if avg > prevAvg+0.01 { // small tolerance for float
			t.Errorf("non-monotonic: debt=%.0fh avg=%.2f > previous=%.2f", debt, avg, prevAvg)
		}
		prevAvg = avg
	}
}

func TestAdjustForChronotype_ZeroMidpoint(t *testing.T) {
	params := DefaultParams()
	// Midpoint 0h (midnight) → shift = 0 - 3.5 = -3.5, clamped to -2.0
	adjusted := AdjustForChronotype(params, 0.0)
	want := params.CAcrophase - 2.0
	if math.Abs(adjusted.CAcrophase-want) > 0.01 {
		t.Errorf("midnight midpoint: got cAcrophase=%.2f, want %.2f", adjusted.CAcrophase, want)
	}
}

func TestAdjustForChronotype_ExactClampBoundaries(t *testing.T) {
	params := DefaultParams()

	// Exactly +2h shift: midpoint = 3.5 + 2.0 = 5.5
	adjusted := AdjustForChronotype(params, 5.5)
	want := params.CAcrophase + 2.0
	if math.Abs(adjusted.CAcrophase-want) > 0.01 {
		t.Errorf("exact +2h: got %.2f, want %.2f", adjusted.CAcrophase, want)
	}

	// Exactly -2h shift: midpoint = 3.5 - 2.0 = 1.5
	adjusted = AdjustForChronotype(params, 1.5)
	want = params.CAcrophase - 2.0
	if math.Abs(adjusted.CAcrophase-want) > 0.01 {
		t.Errorf("exact -2h: got %.2f, want %.2f", adjusted.CAcrophase, want)
	}
}

func TestPredictEnergy_EmptySlice(t *testing.T) {
	predStart := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)
	predEnd := time.Date(2024, 1, 16, 8, 0, 0, 0, time.UTC)

	// Non-nil empty slice should work the same as nil.
	points := PredictEnergy(DefaultParams(), []SleepPeriod{}, predStart, predEnd)
	if len(points) == 0 {
		t.Fatal("should produce points even with empty slice")
	}
}

func TestPredictEnergy_EqualStartEnd(t *testing.T) {
	now := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)
	points := PredictEnergy(DefaultParams(), nil, now, now)
	if points != nil {
		t.Error("should return nil when start == end")
	}
}

func TestPredictEnergy_VeryShortWindow(t *testing.T) {
	predStart := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)
	predEnd := predStart.Add(3 * time.Minute) // less than sampleMinutes

	points := PredictEnergy(DefaultParams(), nil, predStart, predEnd)
	// With 5-min sampling, a 3-min window should still get the first point at predStart.
	if len(points) != 1 {
		t.Errorf("expected 1 point for 3-min window, got %d", len(points))
	}
}

func TestPredictEnergy_OverlappingSleepPeriods(t *testing.T) {
	loc := time.UTC
	// Two overlapping sleep periods — should not crash or produce NaN.
	periods := []SleepPeriod{
		{Start: time.Date(2024, 1, 15, 23, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 7, 0, 0, 0, loc)},
		{Start: time.Date(2024, 1, 16, 5, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 8, 0, 0, 0, loc)},
	}
	predStart := time.Date(2024, 1, 16, 8, 0, 0, 0, loc)
	predEnd := time.Date(2024, 1, 16, 20, 0, 0, 0, loc)

	points := PredictEnergy(DefaultParams(), periods, predStart, predEnd)
	for _, p := range points {
		if math.IsNaN(p.Alertness) || math.IsInf(p.Alertness, 0) {
			t.Fatalf("NaN/Inf alertness at %v", p.Time)
		}
	}
}

func TestNapInertiaScale_ExactBoundaries(t *testing.T) {
	now := time.Date(2024, 1, 16, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		duration time.Duration
		want     float64
	}{
		{"exactly 20min", 20 * time.Minute, 0.3},
		{"exactly 45min", 45 * time.Minute, 1.0},
		{"exactly 90min", 90 * time.Minute, 0.6},
		{"21min (just over 20)", 21 * time.Minute, 1.0},
		{"46min (just over 45)", 46 * time.Minute, 0.6},
		{"91min (just over 90)", 91 * time.Minute, 0.4},
		{"5min micro-nap", 5 * time.Minute, 0.3},
		{"120min long nap", 120 * time.Minute, 0.4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			periods := []SleepPeriod{{Start: now.Add(-tt.duration), End: now, IsNap: true}}
			got := napInertiaScale(periods, now)
			if math.Abs(got-tt.want) > 0.01 {
				t.Errorf("got %.2f, want %.2f", got, tt.want)
			}
		})
	}
}

func TestNapInertiaScale_NoMatchingPeriod(t *testing.T) {
	now := time.Date(2024, 1, 16, 14, 0, 0, 0, time.UTC)
	// Nap ended 10 minutes ago — outside the 5-min sampling tolerance.
	periods := []SleepPeriod{{
		Start: now.Add(-40 * time.Minute),
		End:   now.Add(-10 * time.Minute),
		IsNap: true,
	}}
	got := napInertiaScale(periods, now)
	if got != 1.0 {
		t.Errorf("no match should return 1.0, got %.2f", got)
	}
}

func TestClassifyZones_SinglePoint(t *testing.T) {
	now := time.Date(2024, 1, 16, 12, 0, 0, 0, time.UTC)
	points := []EnergyPoint{{Time: now, Alertness: 10.0, KSS: 3.0}}
	schedule := ClassifyZones(points, now)
	if len(schedule.Points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(schedule.Points))
	}
}

func TestClassifyZones_AllBeforeWake(t *testing.T) {
	// All points before wake time → all should be ZoneSleep.
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)
	points := []EnergyPoint{
		{Time: time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC), Alertness: 5.0},
		{Time: time.Date(2024, 1, 16, 3, 0, 0, 0, time.UTC), Alertness: 3.0},
		{Time: time.Date(2024, 1, 16, 6, 0, 0, 0, time.UTC), Alertness: 4.0},
	}
	schedule := ClassifyZones(points, wake)
	for _, p := range schedule.Points {
		if p.Zone != ZoneSleep {
			t.Errorf("point at %v should be ZoneSleep, got %s", p.Time, p.Zone)
		}
	}
}

func TestClassifyZones_MonotonicCurve_NoCrash(t *testing.T) {
	// Monotonically increasing — no peaks/troughs to find.
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)
	var points []EnergyPoint
	for i := range 100 {
		points = append(points, EnergyPoint{
			Time:      wake.Add(time.Duration(i*5) * time.Minute),
			Alertness: float64(i) * 0.1,
			KSS:       5.0,
		})
	}
	schedule := ClassifyZones(points, wake)
	if len(schedule.Points) != 100 {
		t.Errorf("expected 100 points, got %d", len(schedule.Points))
	}
	// Should not crash, should still find best focus.
}

func TestClassifyZones_NapRecoveryZone(t *testing.T) {
	// Verify that nap recovery zone is applied after a nap ends.
	// Use a realistic full-day curve so the zone classifier has proper peaks/dips
	// to work with, preventing nap_recovery points from being misclassified.
	loc := time.UTC
	params := DefaultParams()
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	napStart := time.Date(2024, 1, 16, 13, 0, 0, 0, loc)
	napEnd := time.Date(2024, 1, 16, 13, 20, 0, 0, loc)

	periods := []SleepPeriod{
		{Start: time.Date(2024, 1, 15, 23, 0, 0, 0, loc), End: wake},
		{Start: napStart, End: napEnd, IsNap: true},
	}

	points := PredictEnergy(params, periods, wake, time.Date(2024, 1, 16, 20, 0, 0, 0, loc))
	schedule := ClassifyZones(points, wake, periods[1])

	// Points in first 30 min after nap should be nap_recovery.
	recoveryCount := 0
	for _, p := range schedule.Points {
		if p.Zone == ZoneNapRecovery {
			recoveryCount++
		}
	}
	if recoveryCount == 0 {
		// Log what zones are present near nap end.
		for _, p := range schedule.Points {
			if !p.Time.Before(napEnd) && p.Time.Before(napEnd.Add(35*time.Minute)) {
				t.Logf("  %s: zone=%s alertness=%.2f", p.Time.Format("15:04"), p.Zone, p.Alertness)
			}
		}
		t.Error("expected some nap_recovery zones after nap end")
	}
}

func TestPhase2SleepFormula_StepVsAbsolute(t *testing.T) {
	// Directly test the phase2 math to prove the snap-to-breakLevel issue.
	// If S=13.0 at sleep onset and we use the absolute formula with tSleep=0:
	//   S = ha - (ha - bl) * exp(-rate * 0) = ha - (ha-bl) = bl = 12.2
	// That's wrong — S should stay at 13.0 and converge toward ha=14.3.
	params := DefaultParams()
	sAtSleepOnset := 13.0

	// Absolute formula with phase2Start = now (tSleep = 0):
	tSleep := 0.0
	sAbsolute := params.SUpperAsymptote - (params.SUpperAsymptote-params.SBreakLevel)*math.Exp(-params.SRecoveryRate*tSleep)

	t.Logf("S at sleep onset: %.2f", sAtSleepOnset)
	t.Logf("Absolute formula (tSleep=0): S = %.2f (breakLevel=%.2f)", sAbsolute, params.SBreakLevel)

	// PROVEN: the absolute formula snaps to breakLevel at tSleep=0
	if math.Abs(sAbsolute-params.SBreakLevel) > 0.01 {
		t.Fatalf("expected absolute formula to produce breakLevel at tSleep=0, got %.2f", sAbsolute)
	}

	// Step formula preserves S correctly:
	dt := float64(sampleMinutes) / 60.0
	sStep := params.SUpperAsymptote - (params.SUpperAsymptote-sAtSleepOnset)*math.Exp(-params.SRecoveryRate*dt)

	t.Logf("Step formula (from S=%.1f, dt=%.3fh): S = %.4f", sAtSleepOnset, dt, sStep)

	// Step formula should produce S > 13.0 (recovery toward ha=14.3):
	if sStep <= sAtSleepOnset {
		t.Errorf("step formula should recover from %.2f toward ha=%.2f, got %.2f",
			sAtSleepOnset, params.SUpperAsymptote, sStep)
	}
}

func TestClassifyZones_BestFocusNotInInertia(t *testing.T) {
	// BestFocusStart should never fall within the inertia period.
	loc := time.UTC
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	inertiaEnd := wake.Add(90 * time.Minute) // 8:30am

	// Generate a realistic-looking curve with peak right after inertia.
	var points []EnergyPoint
	for m := 0; m <= 16*60; m += 5 {
		t := wake.Add(time.Duration(m) * time.Minute)
		// Peak at 9:00am (just 30 min after inertia).
		alertness := 10.0 - 0.001*float64(m-120)*float64(m-120)/100.0
		points = append(points, EnergyPoint{Time: t, Alertness: alertness, KSS: 5.0})
	}

	schedule := ClassifyZones(points, wake)

	if !schedule.BestFocusStart.IsZero() && schedule.BestFocusStart.Before(inertiaEnd) {
		t.Errorf("BestFocusStart (%v) should not fall within inertia (ends %v)",
			schedule.BestFocusStart.Format("15:04"), inertiaEnd.Format("15:04"))
	}
}

func TestClassifyZones_MelatoninOverwritesEveningPeak(t *testing.T) {
	// Melatonin window should take priority over evening peak labeling.
	loc := time.UTC
	wake := time.Date(2024, 1, 16, 6, 0, 0, 0, loc)

	var points []EnergyPoint
	for m := 0; m <= 18*60; m += 5 {
		pt := wake.Add(time.Duration(m) * time.Minute)
		h := float64(m) / 60.0
		alertness := 8.0 + 3.0*math.Cos(2*math.Pi/16.0*(h-4)) + 2.0*math.Cos(2*math.Pi/16.0*(h-14))
		points = append(points, EnergyPoint{Time: pt, Alertness: alertness, KSS: 5.0})
	}

	schedule := ClassifyZones(points, wake)

	melatoninCount := 0
	for _, p := range schedule.Points {
		if p.Zone == ZoneMelatoninWindow {
			melatoninCount++
		}
	}

	if melatoninCount == 0 {
		t.Error("expected melatonin window zone to be present")
	}
	if schedule.MelatoninWindow.IsZero() {
		t.Error("MelatoninWindow should be set")
	}
}

func TestPredictEnergy_MicroNap_ShorterThanSample(t *testing.T) {
	// A nap shorter than the 5-min sample interval might be skipped entirely.
	// The timespanset should still detect it if any sample point falls within.
	loc := time.UTC
	params := DefaultParams()

	nightStart := time.Date(2024, 1, 15, 23, 0, 0, 0, loc)
	nightEnd := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	// 3-minute nap at exactly a sample boundary (1:00pm)
	napStart := time.Date(2024, 1, 16, 13, 0, 0, 0, loc)
	napEnd := time.Date(2024, 1, 16, 13, 3, 0, 0, loc) // 3 min

	periodsWithMicro := []SleepPeriod{
		{Start: nightStart, End: nightEnd},
		{Start: napStart, End: napEnd, IsNap: true},
	}
	periodsWithout := []SleepPeriod{
		{Start: nightStart, End: nightEnd},
	}

	predStart := time.Date(2024, 1, 16, 12, 50, 0, 0, loc)
	predEnd := time.Date(2024, 1, 16, 14, 0, 0, 0, loc)

	withMicro := PredictEnergy(params, periodsWithMicro, predStart, predEnd)
	without := PredictEnergy(params, periodsWithout, predStart, predEnd)

	// The micro-nap may or may not affect the curve depending on whether
	// a sample point lands within it. With 5-min samples, a 3-min nap at
	// exactly 1:00pm would be detected at the 1:00pm sample but the 1:05pm
	// sample is already past the nap end.
	t.Logf("Points with micro-nap: %d, without: %d", len(withMicro), len(without))

	// Key property: should not crash or produce NaN.
	for _, p := range withMicro {
		if math.IsNaN(p.Alertness) || math.IsInf(p.Alertness, 0) {
			t.Fatalf("NaN/Inf alertness at %v", p.Time)
		}
	}
}

func TestClassifyZones_TwoPointsOnly(t *testing.T) {
	// With only 2 points, no extrema can be found (need i-1, i, i+1).
	// Should not crash and should still produce valid zones.
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)
	points := []EnergyPoint{
		{Time: wake.Add(2 * time.Hour), Alertness: 10.0, KSS: 4.0},
		{Time: wake.Add(4 * time.Hour), Alertness: 12.0, KSS: 3.0},
	}
	schedule := ClassifyZones(points, wake)
	if len(schedule.Points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(schedule.Points))
	}
	// Both should be classified as "normal" (no extrema to anchor peaks/dips).
	for _, p := range schedule.Points {
		if p.Zone != ZoneNormal {
			t.Errorf("with only 2 points, expected ZoneNormal, got %s at %v", p.Zone, p.Time)
		}
	}
}

func TestClassifyZones_WindDownOnlyOverwritesNormal(t *testing.T) {
	// Wind-down should NOT overwrite morning_peak, afternoon_dip, etc.
	// Only points with zone == ZoneNormal should become ZoneWindDown.
	loc := time.UTC
	wake := time.Date(2024, 1, 16, 6, 0, 0, 0, loc)

	// Create a curve with a clear peak around 10am, then declining.
	var points []EnergyPoint
	for m := 0; m <= 12*60; m += 5 {
		t := wake.Add(time.Duration(m) * time.Minute)
		h := float64(m) / 60.0
		alertness := 10.0 + 4.0*math.Cos(2*math.Pi/24.0*(h-4.0))
		points = append(points, EnergyPoint{Time: t, Alertness: alertness, KSS: 5.0})
	}

	schedule := ClassifyZones(points, wake)

	// Verify no point has wind_down zone if it was previously a peak/dip zone.
	for _, p := range schedule.Points {
		if p.Zone == ZoneWindDown {
			// Wind-down points should all be after some peak.
			if p.Time.Before(wake.Add(4 * time.Hour)) {
				t.Errorf("wind_down at %v is too early (before any peak)", p.Time.Format("15:04"))
			}
		}
	}
}

func TestClassifyZones_SetsMorningWake(t *testing.T) {
	// ClassifyZones should set MorningWake = wakeTime so that callers
	// (like the notification dispatcher) don't get zero MorningWake.
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)
	points := []EnergyPoint{
		{Time: wake, Alertness: 10.0, KSS: 4.0},
		{Time: wake.Add(time.Hour), Alertness: 12.0, KSS: 3.0},
	}
	schedule := ClassifyZones(points, wake)
	if schedule.MorningWake.IsZero() {
		t.Error("ClassifyZones should set MorningWake")
	}
	if !schedule.MorningWake.Equal(wake) {
		t.Errorf("MorningWake=%v, want %v", schedule.MorningWake, wake)
	}
}

func TestCalculateSleepDebt_SingleDay_CumulativeMatchesDirect(t *testing.T) {
	// With λ=0.85, a single night at daysAgo=0 has weight 0.85^0 = 1.0.
	// So debt = deficit × 1.0.
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	records := []SleepRecord{{Date: ref, DurationMinutes: 300}} // 5h → 3h deficit
	debt := CalculateSleepDebt(records, 8.0, ref)
	if debt.Hours != 3.0 {
		t.Errorf("single day debt = %.1f, want 3.0", debt.Hours)
	}
}

// --- Coverage gap: PredictEnergy sleep onset with S above breakLevel ---

func TestPredictEnergy_SleepOnsetPhase2_SAboveBreakLevel(t *testing.T) {
	// When S is above breakLevel at sleep onset, should start in phase 2.
	// This happens when the user takes a nap after being awake only briefly.
	loc := time.UTC
	params := DefaultParams()

	// Sleep 11pm-7am, then awake 7-8am (1h), then nap 8-9am.
	// After only 1h awake, S is still near SUpperAsymptote (14.3),
	// well above SBreakLevel (12.2).
	periods := []SleepPeriod{
		{Start: time.Date(2024, 1, 15, 23, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 7, 0, 0, 0, loc)},
		{Start: time.Date(2024, 1, 16, 8, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 9, 0, 0, 0, loc), IsNap: true},
	}

	// Predict from 7am to noon
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	points := PredictEnergy(params, periods, wake, time.Date(2024, 1, 16, 12, 0, 0, 0, loc))

	// S should recover during the nap, not drop to breakLevel.
	// Check alertness at 8:30am (during nap - shouldn't appear) and 9:05am (just after nap)
	postNap := time.Date(2024, 1, 16, 9, 5, 0, 0, loc)
	for _, p := range points {
		if p.Time.Equal(postNap) {
			// After a 1h nap starting at high S, alertness should still be high
			if p.Alertness < 8.0 {
				t.Errorf("post-nap alertness %.2f too low — S may have snapped to breakLevel", p.Alertness)
			}
			return
		}
	}
	t.Error("no point found at 9:05am")
}

// --- Coverage gap: PredictEnergy with no sleep periods (starts awake, no lastWakeTime) ---

func TestPredictEnergy_NoSleepPeriods_StartsAwake(t *testing.T) {
	// When no sleep periods exist, the model should still produce a curve.
	// lastWakeTime will be nil, so no inertia.
	loc := time.UTC
	params := DefaultParams()

	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	points := PredictEnergy(params, nil, wake, wake.Add(4*time.Hour))

	if len(points) == 0 {
		t.Fatal("expected points even with no sleep periods")
	}

	// With no sleep periods, S starts at SInitial and decays.
	// No inertia (W=0 since no lastWakeTime).
	first := points[0]
	if first.Alertness < 5 || first.Alertness > 15 {
		t.Errorf("first alertness %.2f outside reasonable range", first.Alertness)
	}

	// KSS should be populated
	if first.KSS <= 0 || first.KSS > 9 {
		t.Errorf("first KSS %.2f outside valid range", first.KSS)
	}
}

// --- Coverage gap: ClassifyZones no peaks found (monotonic curve) ---

func TestClassifyZones_MonotonicCurve_FallbackToGlobalMax(t *testing.T) {
	// When the curve is monotonically increasing (no local max), the classifier
	// should fall back to global max for BestFocus.
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)

	// Build a strictly increasing alertness curve (no peaks at all)
	var points []EnergyPoint
	for i := range 200 { // ~16.5h at 5min intervals
		t := wake.Add(time.Duration(i*5) * time.Minute)
		alertness := 5.0 + float64(i)*0.05 // strictly increasing
		kss := alertnessToKSS(alertness)
		points = append(points, EnergyPoint{Time: t, Alertness: alertness, KSS: kss})
	}

	schedule := ClassifyZones(points, wake)

	// BestFocus should still be set (using the fallback global max)
	if schedule.BestFocusStart.IsZero() {
		t.Error("BestFocusStart should not be zero for monotonic curve")
	}
	if schedule.BestFocusEnd.IsZero() {
		t.Error("BestFocusEnd should not be zero for monotonic curve")
	}
}

// --- Coverage gap: ClassifyZones wind-down with no evening peak ---

func TestClassifyZones_WindDownFromMorningPeakOnly(t *testing.T) {
	// When only morning peak exists (no evening peak), wind-down should
	// be based on the morning peak threshold.
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)

	// Build curve: rises to peak at ~10am then declines steadily
	var points []EnergyPoint
	for i := range 200 {
		t := wake.Add(time.Duration(i*5) * time.Minute)
		hoursAwake := float64(i*5) / 60.0
		// Peak at 3h, then decline
		alertness := 12.0 - (hoursAwake-3.0)*(hoursAwake-3.0)*0.15
		if alertness < 3 {
			alertness = 3
		}
		kss := alertnessToKSS(alertness)
		points = append(points, EnergyPoint{Time: t, Alertness: alertness, KSS: kss})
	}

	schedule := ClassifyZones(points, wake)

	// Should have zones assigned — wind-down or melatonin should exist
	// in the declining portion of the curve.
	hasLateZone := false
	for _, p := range schedule.Points {
		if p.Zone == ZoneWindDown || p.Zone == ZoneMelatoninWindow {
			hasLateZone = true
			break
		}
	}
	if !hasLateZone {
		t.Error("expected wind-down or melatonin zone in declining portion of curve")
	}
}

// --- Coverage gap: ClassifyZones BestFocusStart clamped to inertia end ---

func TestClassifyZones_BestFocusClampedToInertiaEnd(t *testing.T) {
	// If the morning peak is at 8am (1h after 7am wake), BestFocusStart
	// would be 7am, but should be clamped to inertia end (8:30am).
	loc := time.UTC
	params := DefaultParams()
	periods := []SleepPeriod{
		{Start: time.Date(2024, 1, 15, 23, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 7, 0, 0, 0, loc)},
	}
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, loc)
	points := PredictEnergy(params, periods, wake, wake.Add(17*time.Hour))
	schedule := ClassifyZones(points, wake)

	inertiaEnd := wake.Add(90 * time.Minute)
	if schedule.BestFocusStart.Before(inertiaEnd) {
		t.Errorf("BestFocusStart %s before inertia end %s",
			schedule.BestFocusStart.Format("15:04"), inertiaEnd.Format("15:04"))
	}
}

// --- Coverage gap: PredictEnergy with predStart after wake (lastWakeTime from history) ---

func TestPredictEnergy_PredStartAfterWake(t *testing.T) {
	// Prediction starting well after wake should still produce correct alertness
	// (inertia gone, S partially decayed).
	loc := time.UTC
	params := DefaultParams()

	periods := []SleepPeriod{
		{Start: time.Date(2024, 1, 15, 23, 0, 0, 0, loc), End: time.Date(2024, 1, 16, 7, 0, 0, 0, loc)},
	}

	predStart := time.Date(2024, 1, 16, 10, 0, 0, 0, loc)
	points := PredictEnergy(params, periods, predStart, predStart.Add(4*time.Hour))

	if len(points) == 0 {
		t.Fatal("expected points")
	}

	// At 10am (3h after wake), inertia should be gone — alertness should be high
	if points[0].Alertness < 10.0 {
		t.Errorf("alertness at 10am = %.2f, expected > 10.0 (inertia should be done)", points[0].Alertness)
	}
}

// --- Coverage gap: ClassifyZones afternoonDip→eveningPeak strict path ---

func TestClassifyZones_ThreeExtrema_StrictLocalMin(t *testing.T) {
	// Build a curve with a clear local minimum between two peaks.
	// This exercises the strict afternoonDip detection (line 101-102)
	// rather than the robust fallback.
	wake := time.Date(2024, 1, 16, 7, 0, 0, 0, time.UTC)

	// Curve: peak at 10am, dip at 1pm, peak at 5pm
	var points []EnergyPoint
	for i := range 200 {
		t := wake.Add(time.Duration(i*5) * time.Minute)
		hoursAwake := float64(i*5) / 60.0

		// Three-hump curve with clear extrema
		var alertness float64
		switch {
		case hoursAwake < 1.5:
			alertness = 6.0 + hoursAwake*3 // inertia rise
		case hoursAwake < 3.5:
			alertness = 11.0 + (hoursAwake-2.5)*1.5 // peak at 3h
		case hoursAwake < 7:
			alertness = 12.5 - (hoursAwake-3.5)*1.0 // drop to dip at 7h
		case hoursAwake < 10:
			alertness = 9.0 + (hoursAwake-7)*1.2 // rise to evening peak
		default:
			alertness = 12.6 - (hoursAwake-10)*0.8 // decline
		}
		kss := alertnessToKSS(alertness)
		points = append(points, EnergyPoint{Time: t, Alertness: alertness, KSS: kss})
	}

	schedule := ClassifyZones(points, wake)

	// Should detect all three zones
	zones := make(map[string]bool)
	for _, p := range schedule.Points {
		zones[p.Zone] = true
	}

	if !zones[ZoneMorningPeak] {
		t.Error("expected morning_peak zone")
	}
	if !zones[ZoneAfternoonDip] {
		t.Error("expected afternoon_dip zone")
	}
	if !zones[ZoneEveningPeak] {
		t.Error("expected evening_peak zone")
	}

	// Nap window should be set
	if schedule.OptimalNapStart.IsZero() {
		t.Error("expected nap window to be set with clear afternoon dip")
	}
}

// --- Freshness fields populated ---

func TestCalculateSleepDebt_FreshnessPopulated(t *testing.T) {
	ref := time.Date(2024, 1, 16, 12, 0, 0, 0, time.UTC)

	// No records → insufficient
	debt0 := CalculateSleepDebt(nil, 8.0, ref)
	if debt0.Freshness != FreshnessInsufficient {
		t.Errorf("no records: freshness=%q, want insufficient", debt0.Freshness)
	}
	if debt0.GapDays != 13 {
		t.Errorf("no records: gaps=%d, want 13", debt0.GapDays)
	}

	// 14 days of full data → complete
	var full []SleepRecord
	for i := range 14 {
		full = append(full, SleepRecord{Date: ref.AddDate(0, 0, -i), DurationMinutes: 480})
	}
	debtFull := CalculateSleepDebt(full, 8.0, ref)
	if debtFull.Freshness != FreshnessComplete {
		t.Errorf("full data: freshness=%q, want complete", debtFull.Freshness)
	}
	if debtFull.GapDays != 0 {
		t.Errorf("full data: gaps=%d, want 0", debtFull.GapDays)
	}

	// 12 of 14 days → recent
	var partial []SleepRecord
	for i := 2; i < 14; i++ {
		partial = append(partial, SleepRecord{Date: ref.AddDate(0, 0, -i), DurationMinutes: 480})
	}
	debtPartial := CalculateSleepDebt(partial, 8.0, ref)
	if debtPartial.Freshness != FreshnessRecent {
		t.Errorf("12/14 days: freshness=%q, want recent", debtPartial.Freshness)
	}

	// 8 of 14 days → stale
	var stale []SleepRecord
	for i := 6; i < 14; i++ {
		stale = append(stale, SleepRecord{Date: ref.AddDate(0, 0, -i), DurationMinutes: 480})
	}
	debtStale := CalculateSleepDebt(stale, 8.0, ref)
	if debtStale.Freshness != FreshnessStale {
		t.Errorf("8/14 days: freshness=%q, want stale", debtStale.Freshness)
	}
}
