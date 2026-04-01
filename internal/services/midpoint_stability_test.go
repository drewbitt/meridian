package services

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
)

// Monte Carlo validation of chronotype estimation parameters.
//
// Tests that our midpoint window (14 days), minimum nights (5), and
// clamp (±2h) produce accurate CAcrophase estimates across diverse
// user archetypes, outlier rates, and edge cases.
//
// Methodology: For each condition, generate synthetic sleep histories
// with a known true midpoint, compute the estimated midpoint, measure
// the absolute circular error. Repeat N trials and report percentiles.
//
// References:
//   - Aili et al. 2017 (PMC5181612): 5-7 nights minimum for stable estimates
//   - Menghini et al. 2022 (PMC10104388): 5 nights for ICC≥0.8
//   - Roenneberg 2007: within-person SD ~0.5-1.0h (regular), ~1.5-2.0h (irregular)
//   - Fekedulegn 2020 (PMC7191872): 7 days minimum, 14 preferred

const mcTrials = 2000 // trials per condition; balances speed vs precision

// sleepProfile defines a synthetic user's sleep characteristics.
type sleepProfile struct {
	name    string
	trueMid float64 // true midpoint (hours, 0=midnight)
	midSD   float64 // within-person SD of midpoint (hours)
	durMean float64 // mean sleep duration (hours)
	durSD   float64 // SD of duration (hours)
}

// Reference profiles spanning the chronotype spectrum.
// SD values from Roenneberg 2007, HCHS/SOL (PMC7969471).
var profiles = []sleepProfile{
	{"early_bird", 1.0, 0.5, 7.5, 0.5},
	{"regular_3am", 3.0, 0.7, 8.0, 0.5},
	{"regular_4am", 4.0, 0.7, 8.0, 0.5},
	{"night_owl", 6.0, 1.0, 8.5, 0.8},
	{"irregular", 3.0, 1.5, 7.5, 1.0},
	{"midnight_crosser", 0.0, 1.0, 8.0, 0.7},
}

// mcResult holds aggregated simulation statistics.
type mcResult struct {
	n      int
	okRate float64 // fraction of trials that returned ok
	median float64 // median absolute error (hours)
	p90    float64 // 90th percentile error
	p95    float64 // 95th percentile error
}

func TestMidpointStability_MinimumNights(t *testing.T) {
	nightCounts := []int{3, 4, 5, 6, 7, 10, 14}

	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			t.Logf("true=%.1fh  sd=%.1fh  dur=%.1f±%.1fh", p.trueMid, p.midSD, p.durMean, p.durSD)
			for _, n := range nightCounts {
				r := runMC(p, n, 0, mcTrials)
				t.Logf("  n=%2d: ok=%.0f%%  median=%4.0fm  p90=%4.0fm  p95=%4.0fm",
					n, r.okRate*100, r.median*60, r.p90*60, r.p95*60)
			}
		})
	}
}

func TestMidpointStability_OutlierResistance(t *testing.T) {
	p := sleepProfile{"baseline", 3.5, 0.7, 8.0, 0.5}
	for _, outlierRate := range []float64{0, 0.1, 0.2, 0.3} {
		t.Run(fmt.Sprintf("outlier_%d%%", int(outlierRate*100)), func(t *testing.T) {
			for _, n := range []int{5, 7, 10, 14} {
				r := runMC(p, n, outlierRate, mcTrials)
				t.Logf("  n=%2d: ok=%.0f%%  median=%4.0fm  p90=%4.0fm",
					n, r.okRate*100, r.median*60, r.p90*60)
			}
		})
	}
}

func TestMidpointStability_CAcrophaseError(t *testing.T) {
	// What matters: does midpoint error produce meaningful CAcrophase error?
	p := sleepProfile{"moderate_owl", 4.0, 1.0, 8.0, 0.5}
	trueAcro := 16.8 + min(2.0, max(-2.0, p.trueMid-3.5))

	for _, n := range []int{5, 7, 10, 14} {
		rng := rand.New(rand.NewPCG(77, 0)) //nolint:gosec // deterministic seed for reproducibility
		var acroErrs []float64

		for range mcTrials {
			periods := syntheticNights(rng, p, n, 0)
			now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
			mid, ok := habitualSleepMidpoint(periods, now, time.UTC)
			if !ok {
				continue
			}
			params := engine.AdjustForChronotype(engine.DefaultParams(), mid)
			acroErrs = append(acroErrs, math.Abs(params.CAcrophase-trueAcro))
		}

		if len(acroErrs) == 0 {
			t.Logf("n=%2d: insufficient data", n)
			continue
		}
		slices.Sort(acroErrs)
		median := percentile(acroErrs, 0.5)
		p90 := percentile(acroErrs, 0.9)
		under30 := 0
		for _, e := range acroErrs {
			if e < 0.5 {
				under30++
			}
		}
		t.Logf("n=%2d: median=%2.0fm  p90=%2.0fm  <30min=%d%%",
			n, median*60, p90*60, under30*100/len(acroErrs))
	}
}

func TestClampEffect_ExtremeChronotypes(t *testing.T) {
	for _, mid := range []float64{0.5, 1.5, 2.5, 3.5, 4.5, 5.5, 6.5, 7.5} {
		params := engine.AdjustForChronotype(engine.DefaultParams(), mid)
		shift := params.CAcrophase - 16.8
		clamped := math.Abs(mid-3.5) > 2.0
		t.Logf("mid=%4.1fh  shift=%+5.1fh  acro=%5.2fh  clamped=%v",
			mid, shift, params.CAcrophase, clamped)
		if params.CAcrophase < 14.8 || params.CAcrophase > 18.8 {
			t.Errorf("CAcrophase %.2f outside [14.8, 18.8]", params.CAcrophase)
		}
	}
}

// --- core simulation ---

func runMC(p sleepProfile, nights int, outlierRate float64, trials int) mcResult {
	rng := rand.New(rand.NewPCG(42, 0)) //nolint:gosec // deterministic seed for reproducibility
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	var errs []float64
	ok := 0

	for range trials {
		periods := syntheticNights(rng, p, nights, outlierRate)
		mid, valid := habitualSleepMidpoint(periods, now, time.UTC)
		if !valid {
			continue
		}
		ok++
		errs = append(errs, math.Abs(circDiff(mid, p.trueMid)))
	}

	if len(errs) == 0 {
		return mcResult{n: nights, okRate: 0}
	}
	slices.Sort(errs)
	return mcResult{
		n:      nights,
		okRate: float64(ok) / float64(trials),
		median: percentile(errs, 0.5),
		p90:    percentile(errs, 0.9),
		p95:    percentile(errs, 0.95),
	}
}

// syntheticNights generates n sleep periods centered on the profile's true
// midpoint with Gaussian jitter. Outlier nights shift midpoint 3-5h later
// with shorter duration.
func syntheticNights(rng *rand.Rand, p sleepProfile, n int, outlierRate float64) []engine.SleepPeriod {
	baseDay := time.Date(2024, 6, 14, 0, 0, 0, 0, time.UTC)
	periods := make([]engine.SleepPeriod, 0, n)

	for i := range n {
		mid := p.trueMid + rng.NormFloat64()*p.midSD
		dur := p.durMean + rng.NormFloat64()*p.durSD

		if outlierRate > 0 && rng.Float64() < outlierRate {
			mid += 3.0 + rng.Float64()*2.0
			dur = 4.0 + rng.Float64()*2.0
		}

		dur = max(3.0, min(14.0, dur))
		half := dur / 2.0

		day := baseDay.AddDate(0, 0, -i)
		bed := day.Add(time.Duration((mid - half) * float64(time.Hour)))
		wake := day.Add(time.Duration((mid + half) * float64(time.Hour)))
		periods = append(periods, engine.SleepPeriod{Start: bed, End: wake})
	}
	return periods
}

// circDiff returns the shortest signed difference on a 24h circle.
func circDiff(a, b float64) float64 {
	d := math.Mod(a-b+36, 24) - 12 // map to [-12, 12)
	return d
}

func percentile(sorted []float64, p float64) float64 {
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}
