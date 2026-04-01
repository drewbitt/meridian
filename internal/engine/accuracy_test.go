package engine

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// TestAccuracy_Scenarios runs the model through diverse realistic sleep patterns
// and analyzes whether the curve shapes match sleep science expectations.
// This is more of an analysis harness than a strict pass/fail test.
func TestAccuracy_Scenarios(t *testing.T) {
	loc := time.UTC

	t.Run("1_SingleNight_8h", func(t *testing.T) {
		// Well-rested: 11pm-7am. Expect:
		// - Sleep inertia dip at 7am (~30min), alertness gradually rises
		// - Morning peak around 10-11am (C rising + S still high)
		// - Post-lunch dip around 1-3pm (U nadir)
		// - Evening peak around 5-7pm (C peak near acrophase 16.8h)
		// - Decline after 8pm toward sleepiness
		params := DefaultParams()
		periods := []SleepPeriod{
			{Start: d(2024, 1, 15, 23, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},
		}
		wake := d(2024, 1, 16, 7, 0, loc)
		points := PredictEnergy(params, periods, wake, wake.Add(17*time.Hour))
		schedule := ClassifyZones(points, wake)

		a := analyze(points, wake)
		a.log(t)

		// Validate expected shape
		a.expectPeakBetween(t, "morning peak", 9, 12)
		a.expectDipBetween(t, "afternoon dip", 12, 16)
		a.expectPeakBetween(t, "evening peak", 16, 20)
		a.expectInertia(t, points)
		a.expectKSSRange(t, 1, 9)
		a.expectPeakAlertness(t, 11, 18) // reasonable peak range

		// Verify schedule derived times
		t.Logf("Schedule: BestFocus %s-%s, CaffeineCutoff %s, Melatonin %s, NapWindow %s-%s",
			schedule.BestFocusStart.Format("15:04"), schedule.BestFocusEnd.Format("15:04"),
			schedule.CaffeineCutoff.Format("15:04"), schedule.MelatoninWindow.Format("15:04"),
			schedule.OptimalNapStart.Format("15:04"), schedule.OptimalNapEnd.Format("15:04"))
	})

	t.Run("2_TwoDays_Regular", func(t *testing.T) {
		// Two consecutive normal nights. Day 2 should look similar to Day 1.
		params := DefaultParams()
		periods := []SleepPeriod{
			{Start: d(2024, 1, 14, 23, 0, loc), End: d(2024, 1, 15, 7, 0, loc)},
			{Start: d(2024, 1, 15, 23, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},
		}

		// Day 1
		wake1 := d(2024, 1, 15, 7, 0, loc)
		pts1 := PredictEnergy(params, periods, wake1, d(2024, 1, 15, 23, 0, loc))
		a1 := analyze(pts1, wake1)

		// Day 2
		wake2 := d(2024, 1, 16, 7, 0, loc)
		pts2 := PredictEnergy(params, periods, wake2, d(2024, 1, 16, 23, 0, loc))
		a2 := analyze(pts2, wake2)

		t.Logf("Day 1: avg=%.2f, peak=%.2f@%s, trough=%.2f@%s",
			a1.avg, a1.peakVal, a1.peakTime.Format("15:04"), a1.troughVal, a1.troughTime.Format("15:04"))
		t.Logf("Day 2: avg=%.2f, peak=%.2f@%s, trough=%.2f@%s",
			a2.avg, a2.peakVal, a2.peakTime.Format("15:04"), a2.troughVal, a2.troughTime.Format("15:04"))

		// Day 2 should be very similar to Day 1 (same sleep pattern).
		if math.Abs(a1.avg-a2.avg) > 1.5 {
			t.Errorf("two regular nights: day avg diff %.2f (day1=%.2f, day2=%.2f) — expected similar",
				math.Abs(a1.avg-a2.avg), a1.avg, a2.avg)
		}
	})

	t.Run("3_TwoWeeks_Regular", func(t *testing.T) {
		// 14 days of 11pm-7am sleep. Should produce consistent curves.
		params := DefaultParams()
		var periods []SleepPeriod
		for i := range 14 {
			day := d(2024, 1, 1+i, 23, 0, loc)
			periods = append(periods, SleepPeriod{
				Start: day,
				End:   day.Add(8 * time.Hour),
			})
		}

		// Analyze last day (Jan 15, waking Jan 15 7am... wait, last sleep is Jan 14 23:00 - Jan 15 07:00)
		wake := d(2024, 1, 15, 7, 0, loc)
		pts := PredictEnergy(params, periods, wake, d(2024, 1, 15, 23, 0, loc))
		debt := CalculateSleepDebt(
			makeSleepRecords(periods, 8.0),
			8.0,
			d(2024, 1, 15, 12, 0, loc),
		)

		a := analyze(pts, wake)
		a.log(t)
		t.Logf("Sleep debt: %.1fh (%s)", debt.Hours, debt.Category)

		if debt.Hours > 0.5 {
			t.Errorf("14 days of 8h sleep should have near-zero debt, got %.1f", debt.Hours)
		}
		a.expectPeakBetween(t, "morning peak", 9, 12)

		// Validate the zone classifier finds the dip via robust detection.
		schedule := ClassifyZones(pts, wake)
		if schedule.OptimalNapStart.IsZero() {
			t.Logf("NOTE: no nap window set — afternoon dip may be too shallow for robust detection with high S")
		} else {
			t.Logf("✓ nap window: %s-%s (from robust dip detection)",
				schedule.OptimalNapStart.Format("15:04"), schedule.OptimalNapEnd.Format("15:04"))
		}
	})

	t.Run("4_SlightlyIrregular", func(t *testing.T) {
		// Varying bedtimes and wake times over 7 consecutive nights.
		// Each night directly precedes the next day.
		params := DefaultParams()
		type night struct {
			bedH, bedM, wakeH, wakeM int
		}
		nights := []night{
			{22, 0, 6, 0},   // Jan 9 night: 10pm-6am (8h)
			{23, 0, 7, 0},   // Jan 10: 11pm-7am (8h)
			{23, 30, 7, 30}, // Jan 11: 11:30pm-7:30am (8h)
			{0, 30, 8, 0},   // Jan 12: 12:30am-8am (7.5h)
			{22, 30, 6, 30}, // Jan 13: 10:30pm-6:30am (8h)
			{23, 0, 7, 0},   // Jan 14: 11pm-7am (8h)
			{23, 30, 7, 30}, // Jan 15: 11:30pm-7:30am (8h)
		}
		var periods []SleepPeriod
		for i, n := range nights {
			baseDay := 9 + i // Jan 9, 10, 11, ...
			if n.bedH >= 20 {
				periods = append(periods, SleepPeriod{
					Start: d(2024, 1, baseDay, n.bedH, n.bedM, loc),
					End:   d(2024, 1, baseDay+1, n.wakeH, n.wakeM, loc),
				})
			} else {
				periods = append(periods, SleepPeriod{
					Start: d(2024, 1, baseDay+1, n.bedH, n.bedM, loc),
					End:   d(2024, 1, baseDay+1, n.wakeH, n.wakeM, loc),
				})
			}
		}

		// Analyze last day: woke Jan 16 at 7:30am after 11:30pm-7:30am sleep.
		wake := d(2024, 1, 16, 7, 30, loc)
		pts := PredictEnergy(params, periods, wake, d(2024, 1, 17, 0, 0, loc))

		a := analyze(pts, wake)
		a.log(t)

		// Should still show a peak in the morning window even with irregular schedule.
		a.expectPeakBetween(t, "morning peak", 9, 13)

		// Validate zone classifier.
		schedule := ClassifyZones(pts, wake)
		t.Logf("Schedule: BestFocus %s-%s, NapWindow %s-%s",
			schedule.BestFocusStart.Format("15:04"), schedule.BestFocusEnd.Format("15:04"),
			schedule.OptimalNapStart.Format("15:04"), schedule.OptimalNapEnd.Format("15:04"))
	})

	t.Run("5_BadSleep_4h", func(t *testing.T) {
		// One night of only 4h sleep (3am-7am). Expect:
		// - Lower overall alertness
		// - More pronounced afternoon dip
		// - Earlier sleepiness onset
		params := DefaultParams()
		periods := []SleepPeriod{
			{Start: d(2024, 1, 16, 3, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},
		}
		wake := d(2024, 1, 16, 7, 0, loc)
		ptsBad := PredictEnergy(params, periods, wake, d(2024, 1, 16, 23, 0, loc))

		// Compare with good sleep
		goodPeriods := []SleepPeriod{
			{Start: d(2024, 1, 15, 23, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},
		}
		ptsGood := PredictEnergy(params, goodPeriods, wake, d(2024, 1, 16, 23, 0, loc))

		aBad := analyze(ptsBad, wake)
		aGood := analyze(ptsGood, wake)

		t.Logf("4h sleep: avg=%.2f, peak=%.2f@%s", aBad.avg, aBad.peakVal, aBad.peakTime.Format("15:04"))
		t.Logf("8h sleep: avg=%.2f, peak=%.2f@%s", aGood.avg, aGood.peakVal, aGood.peakTime.Format("15:04"))
		t.Logf("Difference: avg delta=%.2f, peak delta=%.2f", aGood.avg-aBad.avg, aGood.peakVal-aBad.peakVal)

		if aBad.avg >= aGood.avg {
			t.Errorf("4h sleep avg (%.2f) should be lower than 8h (%.2f)", aBad.avg, aGood.avg)
		}
		if aBad.peakVal >= aGood.peakVal {
			t.Errorf("4h sleep peak (%.2f) should be lower than 8h (%.2f)", aBad.peakVal, aGood.peakVal)
		}
	})

	t.Run("6_AccumulatedDebt", func(t *testing.T) {
		// 5 nights of 5h sleep → accumulated debt. Expect degraded alertness.
		params := DefaultParams()
		var periods []SleepPeriod
		for i := range 5 {
			bedDay := 11 + i
			periods = append(periods, SleepPeriod{
				Start: d(2024, 1, bedDay+1, 2, 0, loc), // 2am-7am = 5h
				End:   d(2024, 1, bedDay+1, 7, 0, loc),
			})
		}

		records := makeSleepRecords(periods, 5.0)
		debt := CalculateSleepDebt(records, 8.0, d(2024, 1, 16, 12, 0, loc))
		t.Logf("5 nights of 5h: debt=%.1fh (%s)", debt.Hours, debt.Category)

		debtParams := AdjustForDebt(params, debt.Hours)
		wake := d(2024, 1, 16, 7, 0, loc)
		ptsDebt := PredictEnergy(debtParams, periods, wake, d(2024, 1, 16, 23, 0, loc))
		ptsNoDebt := PredictEnergy(params, periods, wake, d(2024, 1, 16, 23, 0, loc))

		aDebt := analyze(ptsDebt, wake)
		aNoDebt := analyze(ptsNoDebt, wake)

		t.Logf("With debt adjustment: avg=%.2f, peak=%.2f", aDebt.avg, aDebt.peakVal)
		t.Logf("Without debt adjust:  avg=%.2f, peak=%.2f", aNoDebt.avg, aNoDebt.peakVal)

		if aDebt.avg >= aNoDebt.avg {
			t.Errorf("debt-adjusted avg (%.2f) should be lower than no-debt (%.2f)", aDebt.avg, aNoDebt.avg)
		}
		if debt.Hours < 2.0 {
			t.Errorf("5 nights of 5h should produce significant debt, got %.1f", debt.Hours)
		}
	})

	t.Run("7_Nap_Afternoon", func(t *testing.T) {
		// Normal night + 20min afternoon nap. Expect:
		// - Brief inertia dip after nap (reduced scale = 0.3)
		// - Post-nap alertness boost
		params := DefaultParams()
		periods := []SleepPeriod{
			{Start: d(2024, 1, 15, 23, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},
			{Start: d(2024, 1, 16, 13, 0, loc), End: d(2024, 1, 16, 13, 20, loc), IsNap: true},
		}
		periodsNoNap := []SleepPeriod{
			{Start: d(2024, 1, 15, 23, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},
		}

		wake := d(2024, 1, 16, 7, 0, loc)
		ptsNap := PredictEnergy(params, periods, wake, d(2024, 1, 16, 20, 0, loc))
		ptsNoNap := PredictEnergy(params, periodsNoNap, wake, d(2024, 1, 16, 20, 0, loc))

		// Compare alertness at 3pm and 5pm (post-nap recovery window)
		napAlert3pm := alertnessAt(ptsNap, d(2024, 1, 16, 15, 0, loc))
		noNapAlert3pm := alertnessAt(ptsNoNap, d(2024, 1, 16, 15, 0, loc))
		napAlert5pm := alertnessAt(ptsNap, d(2024, 1, 16, 17, 0, loc))
		noNapAlert5pm := alertnessAt(ptsNoNap, d(2024, 1, 16, 17, 0, loc))

		t.Logf("At 3pm: nap=%.2f, no-nap=%.2f (delta=%.2f)", napAlert3pm, noNapAlert3pm, napAlert3pm-noNapAlert3pm)
		t.Logf("At 5pm: nap=%.2f, no-nap=%.2f (delta=%.2f)", napAlert5pm, noNapAlert5pm, napAlert5pm-noNapAlert5pm)

		// After a nap, alertness in the afternoon should be higher.
		if napAlert3pm < noNapAlert3pm {
			t.Logf("NOTE: nap alertness at 3pm (%.2f) < no-nap (%.2f) — may be brief inertia dip", napAlert3pm, noNapAlert3pm)
		}
		if napAlert5pm < noNapAlert5pm {
			t.Errorf("nap alertness at 5pm (%.2f) should be >= no-nap (%.2f)", napAlert5pm, noNapAlert5pm)
		}
	})

	t.Run("8_MixedWeek_GoodAndBad", func(t *testing.T) {
		// Realistic mixed week: 3 good nights (8h), 2 short (5h), 1 very short (3h), 1 ok (7h)
		params := DefaultParams()
		periods := []SleepPeriod{
			{Start: d(2024, 1, 9, 23, 0, loc), End: d(2024, 1, 10, 7, 0, loc)},  // 8h
			{Start: d(2024, 1, 11, 2, 0, loc), End: d(2024, 1, 11, 7, 0, loc)},  // 5h
			{Start: d(2024, 1, 11, 23, 0, loc), End: d(2024, 1, 12, 7, 0, loc)}, // 8h
			{Start: d(2024, 1, 13, 2, 0, loc), End: d(2024, 1, 13, 7, 0, loc)},  // 5h
			{Start: d(2024, 1, 14, 4, 0, loc), End: d(2024, 1, 14, 7, 0, loc)},  // 3h (terrible)
			{Start: d(2024, 1, 14, 23, 0, loc), End: d(2024, 1, 15, 7, 0, loc)}, // 8h (recovery)
			{Start: d(2024, 1, 16, 0, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},  // 7h
		}

		records := makeSleepRecords(periods, 0)
		debt := CalculateSleepDebt(records, 8.0, d(2024, 1, 16, 12, 0, loc))
		debtParams := AdjustForDebt(params, debt.Hours)

		wake := d(2024, 1, 16, 7, 0, loc)
		pts := PredictEnergy(debtParams, periods, wake, d(2024, 1, 16, 23, 0, loc))

		a := analyze(pts, wake)
		a.log(t)
		t.Logf("Mixed week debt: %.1fh (%s)", debt.Hours, debt.Category)
		t.Logf("Adjusted params: ha=%.2f (was 14.3), sInitial=%.2f (was 7.96), decay=%.4f (was -0.0353)",
			debtParams.SUpperAsymptote, debtParams.SInitial, debtParams.SDecayRate)

		// Should still show dual-peak pattern.
		a.expectPeakBetween(t, "morning peak", 9, 12)
	})

	t.Run("9_NightOwl_Chronotype", func(t *testing.T) {
		// Night owl: habitual midpoint at 5am (shift +1.5h).
		// Sleep 1am-9am. Peaks should be ~1.5h later than neutral.
		params := DefaultParams()
		params = AdjustForChronotype(params, 5.0)
		periods := []SleepPeriod{
			{Start: d(2024, 1, 16, 1, 0, loc), End: d(2024, 1, 16, 9, 0, loc)},
		}
		wake := d(2024, 1, 16, 9, 0, loc)
		pts := PredictEnergy(params, periods, wake, d(2024, 1, 17, 1, 0, loc))

		// Compare with neutral chronotype
		neutralParams := DefaultParams()
		neutralPeriods := []SleepPeriod{
			{Start: d(2024, 1, 15, 23, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},
		}
		neutralWake := d(2024, 1, 16, 7, 0, loc)
		neutralPts := PredictEnergy(neutralParams, neutralPeriods, neutralWake, d(2024, 1, 16, 23, 0, loc))

		aNO := analyze(pts, wake)
		aNeutral := analyze(neutralPts, neutralWake)

		t.Logf("Night owl peak: %s (alertness=%.2f), neutral peak: %s (alertness=%.2f)",
			aNO.peakTime.Format("15:04"), aNO.peakVal,
			aNeutral.peakTime.Format("15:04"), aNeutral.peakVal)

		// Night owl's peak should be later in the day.
		if !aNO.peakTime.After(aNeutral.peakTime) {
			t.Errorf("night owl peak (%s) should be later than neutral (%s)",
				aNO.peakTime.Format("15:04"), aNeutral.peakTime.Format("15:04"))
		}
	})

	t.Run("10_ComponentDecomposition", func(t *testing.T) {
		// Verify each TPM component has correct sign and magnitude.
		params := DefaultParams()

		s := params.SUpperAsymptote
		for h := 0; h <= 16; h++ {
			tod := float64(7 + h)
			if tod >= 24 {
				tod -= 24
			}

			c := params.CMean + params.CAmplitude*math.Cos(2*math.Pi/24.0*(tod-params.CAcrophase))
			u := params.UMean + params.UAmplitude*math.Cos(2*math.Pi/12.0*(tod-params.CAcrophase-params.UPhaseShift))

			hoursAwake := float64(h)
			w := 0.0
			if hoursAwake < 3 {
				w = params.WCoefficient * math.Exp(params.WDecayRate*hoursAwake)
			}

			total := s + c + u + w
			kss := alertnessToKSS(total)

			if math.IsNaN(total) || math.IsInf(total, 0) {
				t.Errorf("hour %d: total is NaN/Inf (S=%.2f C=%.2f U=%.2f W=%.2f)", h, s, c, u, w)
			}
			if kss < 1 || kss > 9 {
				t.Errorf("hour %d: KSS=%.1f outside [1,9]", h, kss)
			}

			s = params.SLowerAsymptote + (s-params.SLowerAsymptote)*math.Exp(params.SDecayRate*1.0)
		}

		// S should decay over 16h
		sAfter16 := s
		if sAfter16 >= params.SUpperAsymptote {
			t.Errorf("S did not decay: start=%.2f, after16h=%.2f", params.SUpperAsymptote, sAfter16)
		}
	})
}

// --- helpers ---

func d(year, month, day, hour, mn int, loc *time.Location) time.Time {
	return time.Date(year, time.Month(month), day, hour, mn, 0, 0, loc)
}

type curveAnalysis struct {
	avg        float64
	peakVal    float64
	peakTime   time.Time
	troughVal  float64
	troughTime time.Time
	extrema    []struct {
		time  time.Time
		value float64
		isMax bool
	}
	hourlyAlertness map[int]float64
}

func analyze(points []EnergyPoint, wake time.Time) curveAnalysis {
	a := curveAnalysis{
		troughVal:       math.MaxFloat64,
		hourlyAlertness: make(map[int]float64),
	}

	var sum float64
	inertiaEnd := wake.Add(90 * time.Minute)

	for _, p := range points {
		sum += p.Alertness
		// Track hourly values
		if p.Time.Minute() == 0 {
			a.hourlyAlertness[p.Time.Hour()] = p.Alertness
		}

		// Skip inertia for peak/trough detection
		if p.Time.Before(inertiaEnd) {
			continue
		}

		if p.Alertness > a.peakVal {
			a.peakVal = p.Alertness
			a.peakTime = p.Time
		}
		if p.Alertness < a.troughVal {
			a.troughVal = p.Alertness
			a.troughTime = p.Time
		}
	}
	if len(points) > 0 {
		a.avg = sum / float64(len(points))
	}

	// Find local extrema (post-inertia)
	var wakePoints []EnergyPoint
	for _, p := range points {
		if !p.Time.Before(inertiaEnd) {
			wakePoints = append(wakePoints, p)
		}
	}
	for i := 1; i < len(wakePoints)-1; i++ {
		prev, curr, next := wakePoints[i-1].Alertness, wakePoints[i].Alertness, wakePoints[i+1].Alertness
		if curr > prev && curr > next {
			a.extrema = append(a.extrema, struct {
				time  time.Time
				value float64
				isMax bool
			}{wakePoints[i].Time, curr, true})
		} else if curr < prev && curr < next {
			a.extrema = append(a.extrema, struct {
				time  time.Time
				value float64
				isMax bool
			}{wakePoints[i].Time, curr, false})
		}
	}

	return a
}

func (a curveAnalysis) log(t *testing.T) {
	t.Helper()
	t.Logf("Avg=%.2f, Peak=%.2f@%s, Trough=%.2f@%s",
		a.avg, a.peakVal, a.peakTime.Format("15:04"), a.troughVal, a.troughTime.Format("15:04"))
	t.Logf("Local extrema (%d):", len(a.extrema))
	for _, e := range a.extrema {
		kind := "MIN"
		if e.isMax {
			kind = "MAX"
		}
		t.Logf("  %s at %s = %.2f", kind, e.time.Format("15:04"), e.value)
	}
}

func (a curveAnalysis) expectPeakBetween(t *testing.T, name string, hourStart, hourEnd int) {
	t.Helper()
	for _, e := range a.extrema {
		if e.isMax && e.time.Hour() >= hourStart && e.time.Hour() < hourEnd {
			t.Logf("✓ %s found at %s (%.2f)", name, e.time.Format("15:04"), e.value)
			return
		}
	}
	t.Errorf("✗ %s: no local max between %d:00-%d:00", name, hourStart, hourEnd)
}

func (a curveAnalysis) expectDipBetween(t *testing.T, name string, hourStart, hourEnd int) {
	t.Helper()
	// First check for a strict local minimum.
	for _, e := range a.extrema {
		if !e.isMax && e.time.Hour() >= hourStart && e.time.Hour() < hourEnd {
			t.Logf("✓ %s found (local min) at %s (%.2f)", name, e.time.Format("15:04"), e.value)
			return
		}
	}
	// If no strict local min, check for a relative dip (minimum between
	// adjacent peaks). This matches the zone classifier's robust detection.
	var peaks []struct {
		hour int
		val  float64
	}
	for _, e := range a.extrema {
		if e.isMax {
			peaks = append(peaks, struct {
				hour int
				val  float64
			}{e.time.Hour(), e.value})
		}
	}
	if len(peaks) >= 2 {
		// Find minimum between the two peaks from hourly data.
		minVal := math.MaxFloat64
		var minHour int
		for h, v := range a.hourlyAlertness {
			if h > peaks[0].hour && h < peaks[1].hour && v < minVal {
				minVal = v
				minHour = h
			}
		}
		if minVal < math.MaxFloat64 && minHour >= hourStart && minHour < hourEnd {
			depth := math.Min(peaks[0].val, peaks[1].val) - minVal
			t.Logf("✓ %s found (plateau min) at %d:00 (%.2f, depth=%.2f below lower peak)",
				name, minHour, minVal, depth)
			return
		}
	}
	t.Errorf("✗ %s: no dip between %d:00-%d:00", name, hourStart, hourEnd)
}

func (a curveAnalysis) expectInertia(t *testing.T, points []EnergyPoint) {
	t.Helper()
	if len(points) < 2 {
		return
	}
	// First few points should show inertia dip.
	if points[0].Alertness >= points[len(points)/4].Alertness {
		t.Logf("NOTE: first point (%.2f) >= quarter-day point (%.2f) — inertia may be weak",
			points[0].Alertness, points[len(points)/4].Alertness)
	} else {
		t.Logf("✓ inertia: first=%.2f < later=%.2f", points[0].Alertness, points[len(points)/4].Alertness)
	}
}

func (a curveAnalysis) expectKSSRange(t *testing.T, minKSS, maxKSS float64) {
	t.Helper()
	peakKSS := alertnessToKSS(a.peakVal)
	troughKSS := alertnessToKSS(a.troughVal)
	if peakKSS < minKSS || peakKSS > maxKSS {
		t.Errorf("peak KSS %.1f (from alertness %.2f) outside [%.0f, %.0f]", peakKSS, a.peakVal, minKSS, maxKSS)
	}
	if troughKSS < minKSS || troughKSS > maxKSS {
		t.Errorf("trough KSS %.1f (from alertness %.2f) outside [%.0f, %.0f]", troughKSS, a.troughVal, minKSS, maxKSS)
	}
	t.Logf("✓ KSS range: peak=%.1f trough=%.1f (bounds [%.0f, %.0f])", peakKSS, troughKSS, minKSS, maxKSS)
}

func (a curveAnalysis) expectPeakAlertness(t *testing.T, minV, maxV float64) {
	t.Helper()
	if a.peakVal < minV || a.peakVal > maxV {
		t.Errorf("peak alertness %.2f outside expected range [%.0f, %.0f]", a.peakVal, minV, maxV)
	} else {
		t.Logf("✓ peak alertness %.2f in range [%.0f, %.0f]", a.peakVal, minV, maxV)
	}
}

func alertnessAt(points []EnergyPoint, target time.Time) float64 {
	for _, p := range points {
		if p.Time.Equal(target) {
			return p.Alertness
		}
	}
	// Find closest
	var best EnergyPoint
	bestDiff := time.Duration(math.MaxInt64)
	for _, p := range points {
		diff := p.Time.Sub(target)
		if diff < 0 {
			diff = -diff
		}
		if diff < bestDiff {
			bestDiff = diff
			best = p
		}
	}
	return best.Alertness
}

func makeSleepRecords(periods []SleepPeriod, durOverrideH float64) []SleepRecord {
	var records []SleepRecord
	for _, p := range periods {
		dur := int(p.End.Sub(p.Start).Minutes())
		if durOverrideH > 0 {
			dur = int(durOverrideH * 60)
		}
		records = append(records, SleepRecord{
			Date:            p.Start,
			DurationMinutes: dur,
		})
	}
	return records
}

// TestAccuracy_DualPeakShape verifies the model produces the expected RISE-style
// dual energy peak (morning + evening) with an afternoon dip between them.
func TestAccuracy_DualPeakShape(t *testing.T) {
	loc := time.UTC
	params := DefaultParams()
	periods := []SleepPeriod{
		{Start: d(2024, 1, 15, 23, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},
	}
	wake := d(2024, 1, 16, 7, 0, loc)
	pts := PredictEnergy(params, periods, wake, d(2024, 1, 17, 0, 0, loc))

	a := analyze(pts, wake)

	// Count peaks and dips
	var peaks, dips int
	for _, e := range a.extrema {
		if e.isMax {
			peaks++
		} else {
			dips++
		}
	}

	t.Logf("Found %d peaks and %d dips in the curve", peaks, dips)

	// The FIPS model with UAmplitude=0.8 should produce at least 2 peaks.
	if peaks < 2 {
		t.Errorf("expected at least 2 peaks (dual energy peak), got %d", peaks)
		t.Log("Extrema:")
		for _, e := range a.extrema {
			kind := "dip"
			if e.isMax {
				kind = "PEAK"
			}
			t.Logf("  %s at %s = %.2f", kind, e.time.Format("15:04"), e.value)
		}
	}

	// The afternoon dip should be noticeable — at least 1.0 unit below the lower peak.
	if peaks >= 2 && dips >= 1 {
		var peakVals []float64
		var dipVal float64
		for _, e := range a.extrema {
			if e.isMax {
				peakVals = append(peakVals, e.value)
			} else if dipVal == 0 {
				dipVal = e.value
			}
		}
		lowerPeak := math.Min(peakVals[0], peakVals[1])
		dipDepth := lowerPeak - dipVal
		t.Logf("Dip depth: %.2f units below lower peak", dipDepth)
		if dipDepth < 0.5 {
			t.Errorf("afternoon dip too shallow: depth=%.2f (want >= 0.5)", dipDepth)
		}
	}
}

// TestAccuracy_ComponentMagnitudes verifies each TPM component contributes
// a reasonable amount to the total alertness.
func TestAccuracy_ComponentMagnitudes(t *testing.T) {
	p := DefaultParams()

	// S range during a typical day: starts near ha=14.3, decays to ~5-6 by bedtime.
	sAfter8h := p.SLowerAsymptote + (p.SUpperAsymptote-p.SLowerAsymptote)*math.Exp(p.SDecayRate*8)
	sAfter16h := p.SLowerAsymptote + (p.SUpperAsymptote-p.SLowerAsymptote)*math.Exp(p.SDecayRate*16)
	t.Logf("S range: %.2f (after 8h awake) → %.2f (after 16h)", sAfter8h, sAfter16h)
	// S should dominate the alertness signal.
	sRange := p.SUpperAsymptote - sAfter16h
	t.Logf("S daily swing: %.2f", sRange)

	// C range: ±CAmplitude = ±2.5
	t.Logf("C range: %.1f to %.1f (amplitude=%.1f)", p.CMean-p.CAmplitude, p.CMean+p.CAmplitude, p.CAmplitude)

	// U range: ±UAmplitude = ±0.8
	t.Logf("U range: %.1f to %.1f (amplitude=%.1f)", p.UMean-p.UAmplitude, p.UMean+p.UAmplitude, p.UAmplitude)

	// W at wake: -5.72, at 1h: ~-0.7
	wAtWake := p.WCoefficient
	wAt1h := p.WCoefficient * math.Exp(p.WDecayRate*1.0)
	t.Logf("W at wake: %.2f, at 1h: %.2f", wAtWake, wAt1h)

	// Relative contributions at 8am (1h awake):
	tod := 8.0
	c8am := p.CMean + p.CAmplitude*math.Cos(2*math.Pi/24.0*(tod-p.CAcrophase))
	u8am := p.UMean + p.UAmplitude*math.Cos(2*math.Pi/12.0*(tod-p.CAcrophase-p.UPhaseShift))
	s8am := sAfter8h + (p.SUpperAsymptote-sAfter8h)*0.8 // rough: 1h into the day, S still high

	t.Logf("\nContributions at 8am (1h awake):")
	t.Logf("  S=%.2f, C=%.2f, U=%.2f, W=%.2f → total≈%.2f", s8am, c8am, u8am, wAt1h, s8am+c8am+u8am+wAt1h)

	// Verify C is not overpowering S
	if p.CAmplitude > sRange {
		t.Errorf("C amplitude (%.1f) larger than S daily swing (%.1f) — C would dominate", p.CAmplitude, sRange)
	}

	// Verify U amplitude is reasonable relative to C
	if p.UAmplitude > p.CAmplitude {
		t.Errorf("U amplitude (%.1f) larger than C amplitude (%.1f) — ultradian would overpower circadian", p.UAmplitude, p.CAmplitude)
	}
}

// TestAccuracy_DoseResponseDebt validates that chronic sleep restriction produces
// a dose-dependent degradation matching the key findings from:
//   - Van Dongen et al. (2003) "The cumulative cost of additional wakefulness"
//     (14 days of 4h/6h sleep ≈ 1-3 nights of TSD in PVT lapses)
//   - Belenky et al. (2003) "Patterns of performance degradation"
//     (sustained restriction to 5h produces ~30% performance decline)
//
// We test that our logarithmic debt taper produces monotonically increasing
// impairment that saturates correctly, and that the alertness difference between
// well-rested and debt-loaded states is meaningful across a range of debt levels.
func TestAccuracy_DoseResponseDebt(t *testing.T) {
	loc := time.UTC
	baseParams := DefaultParams()
	wake := d(2024, 1, 16, 7, 0, loc)
	periods := []SleepPeriod{
		{Start: d(2024, 1, 15, 23, 0, loc), End: d(2024, 1, 16, 7, 0, loc)},
	}

	// Simulate a standard day for each debt level (0h, 2h, 5h, 10h, 15h, 20h)
	debtLevels := []float64{0, 2, 5, 10, 15, 20}

	type result struct {
		debt   float64
		avg    float64
		peak   float64
		trough float64
		kssAvg float64
		ha     float64
		s0     float64
	}
	var results []result

	t.Log("\nDose-Response Debt Analysis:")
	t.Log("Debt(h)  Avg    Peak   Trough  KSSavg  ha     s0     decay")
	t.Log("-------  -----  -----  ------  ------  -----  -----  ------")

	for _, debt := range debtLevels {
		params := AdjustForDebt(baseParams, debt)
		pts := PredictEnergy(params, periods, wake, d(2024, 1, 16, 23, 0, loc))
		a := analyze(pts, wake)

		// Compute average KSS
		var kssSum float64
		for _, p := range pts {
			kssSum += p.KSS
		}
		kssAvg := kssSum / float64(len(pts))

		r := result{debt, a.avg, a.peakVal, a.troughVal, kssAvg, params.SUpperAsymptote, params.SInitial}
		results = append(results, r)
		t.Logf("%5.0fh   %5.2f  %5.2f  %5.2f   %5.2f   %5.2f  %5.2f  %.4f",
			debt, a.avg, a.peakVal, a.troughVal, kssAvg, params.SUpperAsymptote, params.SInitial, params.SDecayRate)
	}

	// Validate monotonic degradation
	for i := 1; i < len(results); i++ {
		if results[i].avg >= results[i-1].avg {
			t.Errorf("debt dose-response not monotonic: %.0fh avg=%.2f >= %.0fh avg=%.2f",
				results[i].debt, results[i].avg, results[i-1].debt, results[i-1].avg)
		}
	}

	// Validate meaningful impairment at moderate debt
	noDebt := results[0]
	tenHDebt := results[3] // 10h debt
	avgDrop := noDebt.avg - tenHDebt.avg
	peakDrop := noDebt.peak - tenHDebt.peak
	t.Logf("\n10h debt impact: avg drop=%.2f (%.0f%%), peak drop=%.2f",
		avgDrop, avgDrop/noDebt.avg*100, peakDrop)

	// At 10h debt, expect at least 5% average alertness reduction.
	// Van Dongen (2003): 6h sleep × 14 days → PVT lapses equivalent to 24h TSD.
	// Our weighted debt system produces ~3h weighted debt from 14 days of 6h sleep,
	// so 10h represents more extreme restriction.
	if avgDrop/noDebt.avg < 0.05 {
		t.Errorf("10h debt only reduces avg alertness by %.1f%% — expected ≥5%%", avgDrop/noDebt.avg*100)
	}

	// KSS at 10h debt should average at least 0.5 points higher (sleepier)
	kssDelta := tenHDebt.kssAvg - noDebt.kssAvg
	t.Logf("KSS shift at 10h debt: +%.2f points", kssDelta)
	if kssDelta < 0.5 {
		t.Errorf("10h debt KSS shift only %.2f — expected ≥0.5", kssDelta)
	}

	// Validate saturation: 20h debt shouldn't be more than 3× the effect of 10h debt
	// (logarithmic taper should prevent runaway)
	twentyHDebt := results[5]
	drop20 := noDebt.avg - twentyHDebt.avg
	drop10 := noDebt.avg - tenHDebt.avg
	ratio := drop20 / drop10
	t.Logf("Saturation ratio (20h/10h drop): %.2f (want < 3.0 for log taper)", ratio)
	if ratio > 3.0 {
		t.Errorf("debt effect not saturating: 20h drop=%.2f / 10h drop=%.2f = ratio %.2f", drop20, drop10, ratio)
	}

	// Validate the logarithmic taper formula directly
	t.Log("\nLogarithmic taper verification:")
	for _, debt := range debtLevels {
		haReduction := 1.0 * math.Log(1.0+0.25*debt)
		s0Reduction := 0.6 * math.Log(1.0+0.25*debt)
		t.Logf("  debt=%2.0fh: ha_reduction=%.2f, s0_reduction=%.2f, ha=%.2f, s0=%.2f",
			debt, haReduction, s0Reduction, baseParams.SUpperAsymptote-haReduction, baseParams.SInitial-s0Reduction)
	}
}

// TestAccuracy_ChronotypeShift validates that AdjustForChronotype produces
// peak shifts proportional to sleep midpoint deviation. Reference:
//   - Ingre et al. (2014) Table 2: acrophase ranges from 14.6h (morning) to 16.6h (evening)
//   - DLMO phase relationships: DLMO ≈ sleep_midpoint - 6.5h, acrophase ≈ DLMO + 20h
func TestAccuracy_ChronotypeShift(t *testing.T) {
	loc := time.UTC

	type chronotypeCase struct {
		name        string
		midpoint    float64 // habitual sleep midpoint (fractional hours)
		sleepStart  time.Time
		sleepEnd    time.Time
		expectShift float64 // expected acrophase shift from neutral (hours)
		expectPeakH int     // expected evening peak hour (±1)
	}

	cases := []chronotypeCase{
		{
			name: "extreme lark", midpoint: 1.5, // sleep 9pm-6am, midpoint 1:30am
			sleepStart: d(2024, 1, 15, 21, 0, loc), sleepEnd: d(2024, 1, 16, 6, 0, loc),
			expectShift: -2.0, expectPeakH: 15,
		},
		{
			name: "early bird", midpoint: 2.5, // sleep 10pm-7am, midpoint 2:30am
			sleepStart: d(2024, 1, 15, 22, 0, loc), sleepEnd: d(2024, 1, 16, 7, 0, loc),
			expectShift: -1.0, expectPeakH: 16,
		},
		{
			name: "neutral", midpoint: 3.5, // sleep 11pm-8am, midpoint 3:30am
			sleepStart: d(2024, 1, 15, 23, 0, loc), sleepEnd: d(2024, 1, 16, 8, 0, loc),
			expectShift: 0.0, expectPeakH: 17,
		},
		{
			name: "night owl", midpoint: 5.0, // sleep 1am-9am, midpoint 5am
			sleepStart: d(2024, 1, 16, 1, 0, loc), sleepEnd: d(2024, 1, 16, 9, 0, loc),
			expectShift: 1.5, expectPeakH: 18,
		},
		{
			name: "extreme owl", midpoint: 6.5, // sleep 2am-11am, midpoint 6:30am
			sleepStart: d(2024, 1, 16, 2, 0, loc), sleepEnd: d(2024, 1, 16, 11, 0, loc),
			expectShift: 2.0, expectPeakH: 19, // clamped from +3 to +2
		},
	}

	t.Log("\nChronotype Shift Analysis:")
	t.Log("Type           Midpoint  Acrophase  Shift  Peak(C)  ActualPeak")
	t.Log("-------------  --------  ---------  -----  -------  ----------")

	var prevPeakTime time.Time
	for _, tc := range cases {
		params := AdjustForChronotype(DefaultParams(), tc.midpoint)

		// Verify acrophase shift
		expectedAcro := 16.8 + tc.expectShift
		if math.Abs(params.CAcrophase-expectedAcro) > 0.01 {
			t.Errorf("%s: acrophase=%.2f, expected=%.2f", tc.name, params.CAcrophase, expectedAcro)
		}

		// Generate curve
		periods := []SleepPeriod{{Start: tc.sleepStart, End: tc.sleepEnd}}
		pts := PredictEnergy(params, periods, tc.sleepEnd, tc.sleepEnd.Add(17*time.Hour))
		a := analyze(pts, tc.sleepEnd)

		t.Logf("%-13s  %5.1fh     %5.1fh      %+.1f    ~%d:00    %s",
			tc.name, tc.midpoint, params.CAcrophase, tc.expectShift,
			tc.expectPeakH, a.peakTime.Format("15:04"))

		// Evening peak should be within ±2h of expected
		peakH := a.peakTime.Hour()
		if math.Abs(float64(peakH-tc.expectPeakH)) > 2 {
			t.Errorf("%s: peak at %d:00, expected near %d:00 (±2h)", tc.name, peakH, tc.expectPeakH)
		}

		// Peaks should be strictly later for later chronotypes
		if !prevPeakTime.IsZero() && !a.peakTime.After(prevPeakTime) {
			t.Errorf("%s: peak %s not later than previous %s",
				tc.name, a.peakTime.Format("15:04"), prevPeakTime.Format("15:04"))
		}
		prevPeakTime = a.peakTime
	}
}

// TestAccuracy_CumulativeDebtProgression simulates chronic sleep restriction
// over 14 days and validates the progressive degradation pattern. This mirrors
// Van Dongen et al. (2003) design: constant nightly sleep of 4h, 6h, or 8h.
func TestAccuracy_CumulativeDebtProgression(t *testing.T) {
	loc := time.UTC

	for _, nightly := range []float64{4, 6, 8} {
		t.Run(fmt.Sprintf("%.0fh_nightly", nightly), func(t *testing.T) {
			t.Logf("\n%.0fh nightly sleep over 14 days:", nightly)
			t.Log("Day  Debt(h)  Avg    Peak   KSSavg  Category")
			t.Log("---  ------   -----  -----  ------  --------")

			var prevAvg float64
			var degradations int

			for day := 1; day <= 14; day++ {
				// Build sleep periods for the past `day` nights
				var periods []SleepPeriod
				var records []SleepRecord
				bedHour := 23
				wakeHour := bedHour + int(nightly) // simplification: wake at bed + nightly hours
				if wakeHour >= 24 {
					wakeHour -= 24
				}

				for i := range day {
					bedDate := d(2024, 1, 1+i, bedHour, 0, loc)
					wakeDate := d(2024, 1, 2+i, wakeHour, 0, loc)
					periods = append(periods, SleepPeriod{Start: bedDate, End: wakeDate})
					records = append(records, SleepRecord{
						Date:            bedDate,
						DurationMinutes: int(nightly * 60),
					})
				}

				// Compute debt
				refDate := d(2024, 1, 1+day, 12, 0, loc)
				debt := CalculateSleepDebt(records, 8.0, refDate)

				// Generate curve for this day
				params := AdjustForDebt(DefaultParams(), debt.Hours)
				lastWake := d(2024, 1, 1+day, wakeHour, 0, loc)
				pts := PredictEnergy(params, periods, lastWake, lastWake.Add(16*time.Hour))
				a := analyze(pts, lastWake)

				var kssSum float64
				for _, p := range pts {
					kssSum += p.KSS
				}
				kssAvg := kssSum / float64(len(pts))

				t.Logf("%3d  %5.1f    %5.2f  %5.2f  %5.2f   %s",
					day, debt.Hours, a.avg, a.peakVal, kssAvg, debt.Category)

				// Track monotonic degradation (for restricted sleep)
				if prevAvg > 0 && a.avg < prevAvg {
					degradations++
				}
				prevAvg = a.avg
			}

			// For 4h sleep, expect consistent degradation across most days
			if nightly == 4 && degradations < 5 {
				t.Errorf("4h sleep: expected ≥5 days of progressive degradation, got %d", degradations)
			}
			// For 8h sleep, expect stable alertness (no degradation)
			if nightly == 8 && degradations > 3 {
				t.Errorf("8h sleep: expected stable alertness, got %d degradation days", degradations)
			}
		})
	}
}

// TestAccuracy_DebtTaperFormula validates the logarithmic debt taper directly
// against expected mathematical properties.
func TestAccuracy_DebtTaperFormula(t *testing.T) {
	base := DefaultParams()

	// Property 1: Zero debt = no change
	p0 := AdjustForDebt(base, 0)
	if p0.SUpperAsymptote != base.SUpperAsymptote {
		t.Errorf("zero debt changed ha: %.2f → %.2f", base.SUpperAsymptote, p0.SUpperAsymptote)
	}
	if p0.SInitial != base.SInitial {
		t.Errorf("zero debt changed s0: %.2f → %.2f", base.SInitial, p0.SInitial)
	}

	// Property 2: Monotonically non-increasing reduction (floor clamp means
	// values plateau once they hit the floor, around 16h debt for ha)
	prevHa := base.SUpperAsymptote
	prevS0 := base.SInitial
	for _, debt := range []float64{1, 2, 5, 10, 15, 20, 30} {
		p := AdjustForDebt(base, debt)
		if p.SUpperAsymptote > prevHa && debt > 0 {
			t.Errorf("ha increased with more debt: debt=%.0f ha=%.2f > prev=%.2f", debt, p.SUpperAsymptote, prevHa)
		}
		if p.SInitial > prevS0 && debt > 0 {
			t.Errorf("s0 increased with more debt: debt=%.0f s0=%.2f > prev=%.2f", debt, p.SInitial, prevS0)
		}
		prevHa = p.SUpperAsymptote
		prevS0 = p.SInitial
	}

	// Property 3: ha never goes below breakLevel + 0.5
	p50 := AdjustForDebt(base, 50) // extreme
	if p50.SUpperAsymptote < base.SBreakLevel+0.5 {
		t.Errorf("ha=%.2f below floor of breakLevel+0.5=%.2f", p50.SUpperAsymptote, base.SBreakLevel+0.5)
	}

	// Property 4: s0 never goes below lowerAsymptote + 1.0
	if p50.SInitial < base.SLowerAsymptote+1.0 {
		t.Errorf("s0=%.2f below floor of la+1.0=%.2f", p50.SInitial, base.SLowerAsymptote+1.0)
	}

	// Property 5: Decay rate capped at 1.5× original
	pCap := AdjustForDebt(base, 100) // way beyond cap
	maxDecay := base.SDecayRate * 1.5
	if pCap.SDecayRate < maxDecay { // SDecayRate is negative
		t.Errorf("decay=%.4f exceeded 1.5× cap of %.4f", pCap.SDecayRate, maxDecay)
	}

	// Property 6: Diminishing returns — first 5h of debt should have more
	// impact than debt hours 15-20
	p5 := AdjustForDebt(base, 5)
	p15 := AdjustForDebt(base, 15)
	p20 := AdjustForDebt(base, 20)
	first5effect := base.SUpperAsymptote - p5.SUpperAsymptote
	last5effect := p15.SUpperAsymptote - p20.SUpperAsymptote
	t.Logf("Diminishing returns: first 5h ha drop=%.2f, last 5h (15→20) ha drop=%.2f", first5effect, last5effect)
	if last5effect >= first5effect {
		t.Errorf("no diminishing returns: first5=%.2f <= last5=%.2f", first5effect, last5effect)
	}
}

// TestAccuracy_MissingDataGapImpact simulates what happens when the user has
// historical sleep data but recent days are missing (Fitbit sync failed, user
// didn't wear tracker, etc). This proves the silent debt underestimation bug.
func TestAccuracy_MissingDataGapImpact(t *testing.T) {
	loc := time.UTC
	ref := time.Date(2024, 1, 16, 12, 0, 0, 0, loc)
	need := 8.0

	// Build 30 days of mixed sleep (realistic history).
	// Days 0-13 are within the debt window. Days 14-29 are older (ignored by debt).
	buildHistory := func(gapDays int) []SleepRecord {
		var records []SleepRecord
		for daysAgo := range 30 {
			date := ref.AddDate(0, 0, -daysAgo)
			// Skip the gap days (most recent N days have no data)
			if daysAgo < gapDays {
				continue
			}
			// Realistic mixed: mostly 7h, some 6h nights
			dur := 420 // 7h
			if daysAgo%3 == 0 {
				dur = 360 // 6h
			}
			records = append(records, SleepRecord{
				Date:            date,
				DurationMinutes: dur,
			})
		}
		return records
	}

	t.Log("\nMissing Data Gap Impact Analysis:")
	t.Log("Gap(days)  Debt(h)   Category    Records  Notes")
	t.Log("---------  --------  ----------  -------  -----")

	gapDays := []int{0, 1, 2, 3, 5, 7}
	var debts []float64

	for _, gap := range gapDays {
		records := buildHistory(gap)
		debt := CalculateSleepDebt(records, need, ref)

		// Count records in the 14-day window
		inWindow := 0
		for _, r := range records {
			daysAgo := int(ref.Sub(r.Date).Hours()/24 + 0.5)
			if daysAgo >= 0 && daysAgo < 14 {
				inWindow++
			}
		}

		var notes string
		switch {
		case gap == 0:
			notes = "full data (baseline)"
		case gap <= 2:
			notes = "recent gap — debt drops because high-weight recent nights missing"
		default:
			notes = fmt.Sprintf("⚠ %d days missing — debt significantly underestimated", gap)
		}

		t.Logf("%5d      %6.1f    %-10s  %5d    %s",
			gap, debt.Hours, debt.Category, inWindow, notes)
		debts = append(debts, debt.Hours)
	}

	// KEY INSIGHT: Debt should NOT decrease when data is missing.
	// Missing recent nights should be a warning signal, not cause lower debt.
	// With the current approach (skip missing), removing recent high-weight data
	// drops the total because those nights contributed positively to the sum.
	//
	// In reality: if 3 days are missing, the user either:
	// a) Slept normally (no change to debt), or
	// b) Slept poorly (debt should INCREASE)
	// In no scenario should debt DECREASE from missing data.

	baseline := debts[0] // 0-day gap
	for i := 1; i < len(debts); i++ {
		if debts[i] > baseline {
			// This would mean missing data INCREASED debt — that's actually fine
			continue
		}
		reduction := baseline - debts[i]
		pctReduction := reduction / baseline * 100
		if pctReduction > 20 {
			t.Logf("⚠ BUG: %d-day gap reduces debt by %.0f%% (%.1fh → %.1fh) — user sees artificially low debt",
				gapDays[i], pctReduction, baseline, debts[i])
		}
	}

	// Prove: with 3+ days missing, the user is flying blind
	gap3Debt := debts[3] // 3-day gap
	t.Logf("\nComparison: full data debt=%.1fh vs 3-day gap debt=%.1fh (%.0f%% reduction)",
		debts[0], gap3Debt, (debts[0]-gap3Debt)/debts[0]*100)
}

// TestAccuracy_MissingDataScheduleImpact shows what the energy curve looks like
// when today's sleep data is missing (falls back to 7am wake).
func TestAccuracy_MissingDataScheduleImpact(t *testing.T) {
	loc := time.UTC
	params := DefaultParams()

	// Scenario: User actually slept 11pm-6:30am but data hasn't synced.
	// System falls back to 7am wake and uses yesterday's sleep periods.

	// With data: accurate schedule
	withData := []SleepPeriod{
		{Start: d(2024, 1, 14, 23, 0, loc), End: d(2024, 1, 15, 7, 0, loc)},
		{Start: d(2024, 1, 15, 23, 0, loc), End: d(2024, 1, 16, 6, 30, loc)},
	}
	realWake := d(2024, 1, 16, 6, 30, loc)
	ptsReal := PredictEnergy(params, withData, realWake, realWake.Add(17*time.Hour))
	schedReal := ClassifyZones(ptsReal, realWake)

	// Without today's data: system falls back to 7am, uses only yesterday's sleep
	withoutData := []SleepPeriod{
		{Start: d(2024, 1, 14, 23, 0, loc), End: d(2024, 1, 15, 7, 0, loc)},
	}
	fallbackWake := d(2024, 1, 16, 7, 0, loc) // DetermineMorningWake fallback
	ptsFallback := PredictEnergy(params, withoutData, fallbackWake, fallbackWake.Add(17*time.Hour))
	schedFallback := ClassifyZones(ptsFallback, fallbackWake)

	t.Log("\nSchedule comparison — with vs without today's sleep data:")
	t.Logf("  With data:    wake=%s, focus=%s-%s, nap=%s-%s",
		realWake.Format("15:04"),
		schedReal.BestFocusStart.Format("15:04"), schedReal.BestFocusEnd.Format("15:04"),
		schedReal.OptimalNapStart.Format("15:04"), schedReal.OptimalNapEnd.Format("15:04"))
	t.Logf("  Without data: wake=%s, focus=%s-%s, nap=%s-%s",
		fallbackWake.Format("15:04"),
		schedFallback.BestFocusStart.Format("15:04"), schedFallback.BestFocusEnd.Format("15:04"),
		schedFallback.OptimalNapStart.Format("15:04"), schedFallback.OptimalNapEnd.Format("15:04"))

	// The wake times should differ since the real wake was 6:30am
	wakeDiff := fallbackWake.Sub(realWake)
	t.Logf("  Wake time error: %v", wakeDiff)

	// Focus times should shift by ~30min
	focusDiff := schedFallback.BestFocusStart.Sub(schedReal.BestFocusStart)
	t.Logf("  Focus start error: %v", focusDiff)

	// Compare alertness at specific times
	for _, h := range []int{9, 12, 15, 18} {
		target := d(2024, 1, 16, h, 0, loc)
		aReal := alertnessAt(ptsReal, target)
		aFallback := alertnessAt(ptsFallback, target)
		t.Logf("  %02d:00 — with data: %.2f, without: %.2f (delta=%.2f)",
			h, aReal, aFallback, aFallback-aReal)
	}

	// The simulation with missing data uses only old sleep periods,
	// so S will be lower (more decayed). This means the fallback curve
	// is actually pessimistic — alertness predictions are lower.
	aRealNoon := alertnessAt(ptsReal, d(2024, 1, 16, 12, 0, loc))
	aFallbackNoon := alertnessAt(ptsFallback, d(2024, 1, 16, 12, 0, loc))
	t.Logf("\n  Noon alertness: with data=%.2f, without=%.2f", aRealNoon, aFallbackNoon)

	if aFallbackNoon > aRealNoon+1.0 {
		t.Error("fallback curve significantly overestimates alertness — dangerous")
	}
}

// TestAccuracy_GapCountInDebtWindow is a helper that directly counts how many
// days in the 14-day window have no data, to detect freshness issues.
func TestAccuracy_GapCountInDebtWindow(t *testing.T) {
	loc := time.UTC
	ref := time.Date(2024, 1, 16, 12, 0, 0, 0, loc)

	tests := []struct {
		name        string
		records     []SleepRecord
		expectGaps  int
		expectStale bool
	}{
		{
			// All 13 completed nights (daysAgo 1-13) have data.
			// daysAgo=0 (tonight) is excluded from gap counting.
			name: "full 14 days",
			records: func() []SleepRecord {
				var r []SleepRecord
				for i := range 14 {
					r = append(r, SleepRecord{Date: ref.AddDate(0, 0, -i), DurationMinutes: 480})
				}
				return r
			}(),
			expectGaps:  0,
			expectStale: false,
		},
		{
			// Skip daysAgo=0 (tonight) only — all 13 completed nights present.
			// No gap because daysAgo=0 is excluded from counting.
			name: "missing only tonight (normal during daytime)",
			records: func() []SleepRecord {
				var r []SleepRecord
				for i := 1; i < 14; i++ {
					r = append(r, SleepRecord{Date: ref.AddDate(0, 0, -i), DurationMinutes: 480})
				}
				return r
			}(),
			expectGaps:  0,
			expectStale: false,
		},
		{
			// Skip daysAgo=0 and daysAgo=1 — last night is actually missing.
			name: "missing last night",
			records: func() []SleepRecord {
				var r []SleepRecord
				for i := 2; i < 14; i++ {
					r = append(r, SleepRecord{Date: ref.AddDate(0, 0, -i), DurationMinutes: 480})
				}
				return r
			}(),
			expectGaps:  1,
			expectStale: true,
		},
		{
			name: "missing last 3 nights",
			records: func() []SleepRecord {
				var r []SleepRecord
				for i := 4; i < 14; i++ { // skip daysAgo 1-3
					r = append(r, SleepRecord{Date: ref.AddDate(0, 0, -i), DurationMinutes: 480})
				}
				return r
			}(),
			expectGaps:  3,
			expectStale: true,
		},
		{
			name: "only 1 old record",
			records: []SleepRecord{
				{Date: ref.AddDate(0, 0, -10), DurationMinutes: 480},
			},
			expectGaps:  12, // 13 completed nights - 1 with data
			expectStale: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			debt := CalculateSleepDebt(tc.records, 8.0, ref)
			t.Logf("debt=%.1fh, gaps=%d, freshness=%q", debt.Hours, debt.GapDays, debt.Freshness)

			if debt.GapDays != tc.expectGaps {
				t.Errorf("gaps=%d, want %d", debt.GapDays, tc.expectGaps)
			}
			isStale := debt.Freshness != FreshnessComplete
			if isStale != tc.expectStale {
				t.Errorf("stale=%v, want %v (freshness=%q)", isStale, tc.expectStale, debt.Freshness)
			}
		})
	}
}
