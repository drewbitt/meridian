package engine

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

// TestModelValidation_ParameterSensitivity is a numerical sensitivity study,
// not an external accuracy validation. It guards model invariants and records
// how strongly the visible peak/dip story depends on U amplitude across 72
// ordinary sleep/debt/phase combinations per parameter value.
func TestModelValidation_ParameterSensitivity(t *testing.T) {
	t.Parallel()

	type result struct {
		scenarios      int
		actionableDual int
		boundaryPeaks  int
		maxDipKSS      float64
		meanDipKSS     float64
		meanPeakHour   float64
	}

	amplitudes := []float64{0, 0.3, 0.5, 0.8, 1.0, 1.5}
	results := make(map[float64]result)
	loc := time.UTC

	for _, amplitude := range amplitudes {
		var aggregate result
		for _, wakeHour := range []int{5, 7, 9} {
			for _, sleepHours := range []int{4, 6, 8, 10} {
				for _, phaseShift := range []float64{-2, 0, 2} {
					for _, debt := range []float64{0, 10} {
						wake := time.Date(2026, 6, 15, wakeHour, 0, 0, 0, loc)
						period := SleepPeriod{
							Start: wake.Add(-time.Duration(sleepHours) * time.Hour),
							End:   wake,
						}
						params := DefaultParams()
						params.UAmplitude = amplitude
						params.CAcrophase += phaseShift
						params = AdjustForDebt(params, debt)
						points := PredictEnergy(params, []SleepPeriod{period}, wake, wake.Add(17*time.Hour))

						assertFiniteCurve(t, points)
						peakHour, atBoundary := validationPeakHour(points, wake)
						dipKSS := validationStrongestDipKSS(points, wake)
						schedule := ClassifyZones(points, wake)

						aggregate.scenarios++
						aggregate.meanPeakHour += peakHour
						aggregate.meanDipKSS += dipKSS
						aggregate.maxDipKSS = math.Max(aggregate.maxDipKSS, dipKSS)
						if atBoundary {
							aggregate.boundaryPeaks++
						}
						if schedule.EnergyPattern == PatternTwoPeak {
							aggregate.actionableDual++
						}
					}
				}
			}
		}
		aggregate.meanPeakHour /= float64(aggregate.scenarios)
		aggregate.meanDipKSS /= float64(aggregate.scenarios)
		results[amplitude] = aggregate
		t.Logf(
			"Ua=%.1f scenarios=%d two-peak=%d (%.0f%%) raw dip KSS mean=%.2f max=%.2f mean peak=%.1fh after wake boundary=%d",
			amplitude,
			aggregate.scenarios,
			aggregate.actionableDual,
			100*float64(aggregate.actionableDual)/float64(aggregate.scenarios),
			aggregate.meanDipKSS,
			aggregate.maxDipKSS,
			aggregate.meanPeakHour,
			aggregate.boundaryPeaks,
		)
	}

	published := results[0.5]
	if published.actionableDual > 0 {
		t.Errorf("published Ua=0.5 unexpectedly marketed %d/%d scenarios as actionable two-peak days",
			published.actionableDual, published.scenarios)
	}
	if results[0.8].maxDipKSS >= minimumDisplayedKSSProminence {
		t.Errorf("historical Ua=0.8 now crosses the display policy unexpectedly: max dip %.2f KSS",
			results[0.8].maxDipKSS)
	}
	if results[1.5].actionableDual == 0 {
		t.Error("sensitivity harness failed to detect any two-peak shapes even at Ua=1.5")
	}
}

func TestModelValidation_MonotonicResponseInvariants(t *testing.T) {
	t.Parallel()
	loc := time.UTC

	for _, wakeHour := range []int{5, 7, 9, 13, 17} {
		wake := time.Date(2026, 6, 15, wakeHour, 0, 0, 0, loc)
		var previousKSS float64
		for i, debt := range []float64{0, 5, 10, 20} {
			period := SleepPeriod{Start: wake.Add(-8 * time.Hour), End: wake}
			points := PredictEnergy(
				AdjustForDebt(DefaultParams(), debt),
				[]SleepPeriod{period},
				wake,
				wake.Add(17*time.Hour),
			)
			meanKSS := validationMeanKSS(points)
			if i > 0 && meanKSS <= previousKSS {
				t.Errorf("wake %02d:00: debt %.0fh mean KSS %.2f did not exceed previous %.2f",
					wakeHour, debt, meanKSS, previousKSS)
			}
			previousKSS = meanKSS
		}
	}

	wake := time.Date(2026, 6, 15, 7, 0, 0, 0, loc)
	var previousKSS float64
	for i, sleepHours := range []int{4, 6, 8, 10, 12} {
		period := SleepPeriod{
			Start: wake.Add(-time.Duration(sleepHours) * time.Hour),
			End:   wake,
		}
		points := PredictEnergy(DefaultParams(), []SleepPeriod{period}, wake, wake.Add(17*time.Hour))
		meanKSS := validationMeanKSS(points)
		if i > 0 && meanKSS > previousKSS+0.05 {
			t.Errorf("%dh sleep mean KSS %.2f is materially sleepier than shorter sleep %.2f",
				sleepHours, meanKSS, previousKSS)
		}
		previousKSS = meanKSS
	}
}

func TestModelValidation_ChronotypePhaseSensitivity(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	wake := time.Date(2026, 6, 15, 7, 0, 0, 0, loc)
	period := SleepPeriod{Start: wake.Add(-8 * time.Hour), End: wake}

	var peakHours []float64
	for _, shift := range []float64{-2, -1, 0, 1, 2} {
		params := DefaultParams()
		params.CAcrophase += shift
		points := PredictEnergy(params, []SleepPeriod{period}, wake, wake.Add(17*time.Hour))
		peakHour, _ := validationPeakHour(points, wake)
		peakHours = append(peakHours, peakHour)
		t.Logf("phase shift=%+.0fh -> modeled peak %.2fh after wake", shift, peakHour)
	}
	for i := 1; i < len(peakHours); i++ {
		if peakHours[i] <= peakHours[i-1] {
			t.Errorf("later circadian phase did not move peak later: %.2f then %.2f",
				peakHours[i-1], peakHours[i])
		}
	}
	totalShift := peakHours[len(peakHours)-1] - peakHours[0]
	if totalShift < 2.5 || totalShift > 5.5 {
		t.Errorf("±2h phase range moved peak by %.2fh; expected a broadly comparable 2.5-5.5h", totalShift)
	}
}

// TestModelValidation_DebtImputationSensitivity measures estimation error
// against complete synthetic 14-night histories. It establishes why five
// nights can support a labeled estimate but not a high-confidence one.
func TestModelValidation_DebtImputationSensitivity(t *testing.T) {
	t.Parallel()

	profiles := []struct {
		name string
		mean float64
		sd   float64
	}{
		{name: "regular", mean: 7, sd: 0.5},
		{name: "irregular", mean: 7, sd: 1.5},
	}

	const trials = 5000
	for profileIndex, profile := range profiles {
		var p90ByObserved []float64
		for _, observed := range []int{3, 5, 7, 10, 14} {
			// Deterministic simulation; cryptographic randomness is neither
			// needed nor desirable for a reproducible sensitivity test.
			rng := rand.New(rand.NewPCG(uint64(100+profileIndex), uint64(observed))) //nolint:gosec // reproducible test PRNG
			errors := make([]float64, 0, trials)
			var signedError float64
			for range trials {
				durations := make([]float64, debtWindowDays)
				for i := range durations {
					durations[i] = math.Max(2, math.Min(12, profile.mean+rng.NormFloat64()*profile.sd))
				}
				truth := validationDebtFromDurations(durations, 8)

				// Simulate a recent sync gap: only the older N sleep days are
				// available, which is the most consequential common pattern
				// because recent days carry the highest weight.
				available := slices.Clone(durations[debtWindowDays-observed:])
				slices.Sort(available)
				median := available[len(available)/2]
				if len(available)%2 == 0 {
					median = (available[len(available)/2-1] + median) / 2
				}
				estimatedDurations := slices.Clone(durations)
				for i := 0; i < debtWindowDays-observed; i++ {
					estimatedDurations[i] = median
				}
				estimate := validationDebtFromDurations(estimatedDurations, 8)
				errors = append(errors, math.Abs(estimate-truth))
				signedError += estimate - truth
			}
			slices.Sort(errors)
			p50 := errors[len(errors)/2]
			p90 := errors[int(float64(len(errors)-1)*0.9)]
			p90ByObserved = append(p90ByObserved, p90)
			t.Logf("%s observed=%2d/14 debt MAE median=%.2fh p90=%.2fh bias=%+.2fh",
				profile.name, observed, p50, p90, signedError/trials)
		}

		for i := 1; i < len(p90ByObserved); i++ {
			if p90ByObserved[i] > p90ByObserved[i-1]+0.15 {
				t.Errorf("%s: more observed nights materially worsened p90 error: %.2f -> %.2f",
					profile.name, p90ByObserved[i-1], p90ByObserved[i])
			}
		}
	}

	// A behavior change during the missing interval is not recoverable from
	// the older median. Keep this explicit so imputed debt is never described
	// as observed truth.
	oldNormalRecentRestricted := []float64{
		5, 5, 5, 5, 5, 5, 5, // missing recent week
		8, 8, 8, 8, 8, 8, 8, // observed older week
	}
	truth := validationDebtFromDurations(oldNormalRecentRestricted, 8)
	estimated := validationDebtFromDurations(
		[]float64{8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8},
		8,
	)
	t.Logf("non-stationary failure case: true debt %.1fh, median-imputed %.1fh", truth, estimated)
	if estimated >= truth {
		t.Error("failure-case fixture no longer demonstrates imputation underestimation")
	}
}

func assertFiniteCurve(t *testing.T, points []EnergyPoint) {
	t.Helper()
	if len(points) == 0 {
		t.Fatal("model returned no points")
	}
	for _, point := range points {
		if math.IsNaN(point.Alertness) || math.IsInf(point.Alertness, 0) {
			t.Fatalf("non-finite alertness at %s", point.Time)
		}
		if point.KSS < 1 || point.KSS > 9 {
			t.Fatalf("KSS %.2f outside [1,9] at %s", point.KSS, point.Time)
		}
	}
}

func validationMeanKSS(points []EnergyPoint) float64 {
	var sum float64
	for _, point := range points {
		sum += point.KSS
	}
	return sum / float64(len(points))
}

func validationDebtFromDurations(durations []float64, need float64) float64 {
	var debt float64
	for daysAgo, duration := range durations {
		debt += (need - duration) * math.Pow(debtDecay, float64(daysAgo))
	}
	return math.Max(0, debt)
}

func validationPeakHour(points []EnergyPoint, wake time.Time) (float64, bool) {
	start := wake.Add(90 * time.Minute)
	end := wake.Add(14 * time.Hour)
	peakIndex := -1
	for i, point := range points {
		if point.Time.Before(start) || point.Time.After(end) {
			continue
		}
		if peakIndex < 0 || point.Alertness > points[peakIndex].Alertness {
			peakIndex = i
		}
	}
	if peakIndex < 0 {
		return 0, true
	}
	hour := points[peakIndex].Time.Sub(wake).Hours()
	atBoundary := points[peakIndex].Time.Sub(start) < 30*time.Minute ||
		end.Sub(points[peakIndex].Time) < 30*time.Minute
	return hour, atBoundary
}

func validationStrongestDipKSS(points []EnergyPoint, wake time.Time) float64 {
	var indexes []int
	for i, point := range points {
		if !point.Time.Before(wake.Add(90*time.Minute)) &&
			point.Time.Before(wake.Add(14*time.Hour)) {
			indexes = append(indexes, i)
		}
	}
	smoothed := smoothAlertness(points, indexes, 3)
	var maxima []int
	for i := 1; i < len(smoothed)-1; i++ {
		if smoothed[i] > smoothed[i-1] && smoothed[i] >= smoothed[i+1] {
			maxima = append(maxima, i)
		}
	}

	best := 0.0
	for i := 0; i < len(maxima); i++ {
		for j := i + 1; j < len(maxima); j++ {
			if points[indexes[maxima[j]]].Time.Sub(points[indexes[maxima[i]]].Time) < 2*time.Hour {
				continue
			}
			minimum := math.MaxFloat64
			for k := maxima[i] + 1; k < maxima[j]; k++ {
				minimum = math.Min(minimum, smoothed[k])
			}
			if minimum == math.MaxFloat64 {
				continue
			}
			prominence := math.Min(smoothed[maxima[i]]-minimum, smoothed[maxima[j]]-minimum)
			best = math.Max(best, prominence*0.6)
		}
	}
	return best
}
