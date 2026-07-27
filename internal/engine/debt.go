package engine

import (
	"math"
	"slices"
	"time"
)

// DebtCategory classifies sleep debt severity.
type DebtCategory string

// DebtCategory values classify sleep debt severity.
const (
	DebtNone     DebtCategory = "none"     // < 1h
	DebtLow      DebtCategory = "low"      // < 5h
	DebtModerate DebtCategory = "moderate" // < 10h
	DebtHigh     DebtCategory = "high"     // < 20h
	DebtSevere   DebtCategory = "severe"   // >= 20h
)

// SleepDebt holds the calculated sleep debt, its severity category,
// and data quality information.
type SleepDebt struct {
	Hours            float64       `json:"hours"`
	Category         DebtCategory  `json:"category"`
	GapDays          int           `json:"gap_days"`           // days with no data in the 14-day window
	Freshness        DataFreshness `json:"freshness"`          // data completeness category
	LastNightMissing bool          `json:"last_night_missing"` // true when the most recent sleep day has no main sleep
	ObservedNights   int           `json:"observed_nights"`    // main-sleep nights directly observed
	EstimatedNights  int           `json:"estimated_nights"`   // missing nights filled from the user's median
	NapCreditHours   float64       `json:"nap_credit_hours"`   // recency-weighted nap sleep subtracted from debt
	IsEstimate       bool          `json:"is_estimate"`        // true when missing nights were imputed
	IsLowerBound     bool          `json:"is_lower_bound"`     // true when gaps were not safe to impute
}

const (
	debtWindowDays       = 14
	minNightsToEstimate  = 7
	maxStableDurationMAD = 1.0
	debtDecay            = 0.85
)

// CalculateSleepDebt computes a 14-day cumulative weighted sleep debt.
// sleepNeedHours is the user's configured sleep need (typically 8.0).
// records should contain sleep records for the past 14 sleep days.
// referenceDate is the date to calculate debt for (typically today).
//
// The calculation uses exponential decay weights (λ=0.85 per day) to sum
// each main-sleep deficit. Broken main-sleep fragments ending on the same
// local date are summed. Naps are never treated as short nights; their sleep
// duration is instead subtracted as a recency-weighted debt credit.
//
// Missing nights are not treated as zero sleep. Once seven main-sleep nights
// are available, missing nights are estimated from the user's median only
// when durations are reasonably stable (median absolute deviation <= 1h), or
// when no more than two nights are missing. Otherwise Hours remains an
// observed lower bound. These are product uncertainty policies informed by
// sensitivity tests, not claims that seven nights biologically identify debt.
//
// Example: 14 nights of 6h sleep (2h nightly deficit) produces ~12.7 weighted
// hours under this product policy. That value is a planning index, not a
// clinically validated measurement of impairment or recovery time.
func CalculateSleepDebt(records []SleepRecord, sleepNeedHours float64, referenceDate time.Time) SleepDebt {
	if sleepNeedHours <= 0 {
		sleepNeedHours = 8
	}

	mainSleep := make(map[int]float64)
	napSleep := make(map[int]float64)
	for _, r := range records {
		if r.DurationMinutes <= 0 {
			continue
		}
		anchor := r.SleepEnd
		if anchor.IsZero() {
			anchor = r.Date
		}
		daysAgo := calendarDaysBetween(anchor, referenceDate)
		if daysAgo < 0 || daysAgo >= debtWindowDays {
			continue
		}
		hours := float64(r.DurationMinutes) / 60
		if r.IsNap {
			napSleep[daysAgo] += hours
		} else {
			mainSleep[daysAgo] += hours
		}
	}

	observed := len(mainSleep)
	gaps := debtWindowDays - observed

	var observedDurations []float64
	for _, hours := range mainSleep {
		observedDurations = append(observedDurations, hours)
	}
	medianHours := 0.0
	durationMAD := 0.0
	if observed > 0 {
		slices.Sort(observedDurations)
		mid := len(observedDurations) / 2
		medianHours = observedDurations[mid]
		if len(observedDurations)%2 == 0 {
			medianHours = (observedDurations[mid-1] + observedDurations[mid]) / 2
		}

		deviations := make([]float64, len(observedDurations))
		for i, duration := range observedDurations {
			deviations[i] = math.Abs(duration - medianHours)
		}
		slices.Sort(deviations)
		durationMAD = deviations[len(deviations)/2]
		if len(deviations)%2 == 0 {
			durationMAD = (deviations[len(deviations)/2-1] + durationMAD) / 2
		}
	}
	estimateMissing := observed >= minNightsToEstimate &&
		gaps > 0 &&
		(gaps <= 2 || durationMAD <= maxStableDurationMAD)

	var totalDebt float64
	for daysAgo := range debtWindowDays {
		hours, ok := mainSleep[daysAgo]
		if !ok {
			if !estimateMissing {
				continue
			}
			hours = medianHours
		}
		// Sleep above need repays accumulated debt, matching the same signed
		// nightly balance used for shortfalls. The final total is clamped at 0.
		balance := sleepNeedHours - hours
		totalDebt += balance * math.Pow(debtDecay, float64(daysAgo))
	}

	var napCredit float64
	for daysAgo, hours := range napSleep {
		napCredit += hours * math.Pow(debtDecay, float64(daysAgo))
	}
	totalDebt = math.Max(0, totalDebt-napCredit)
	totalDebt = roundTenth(totalDebt)
	napCredit = roundTenth(napCredit)

	var freshness DataFreshness
	switch {
	case observed < minNightsToEstimate:
		freshness = FreshnessInsufficient
	case gaps > 0 && !estimateMissing:
		freshness = FreshnessInsufficient
	case gaps == 0:
		freshness = FreshnessComplete
	case gaps <= 2:
		freshness = FreshnessRecent
	case gaps <= 6:
		freshness = FreshnessStale
	default:
		freshness = FreshnessInsufficient
	}

	_, hasLastNight := mainSleep[0]
	estimatedNights := 0
	if estimateMissing {
		estimatedNights = gaps
	}
	return SleepDebt{
		Hours:            totalDebt,
		Category:         categorize(totalDebt),
		GapDays:          gaps,
		Freshness:        freshness,
		LastNightMissing: !hasLastNight,
		ObservedNights:   observed,
		EstimatedNights:  estimatedNights,
		NapCreditHours:   napCredit,
		IsEstimate:       estimateMissing,
		IsLowerBound:     gaps > 0 && !estimateMissing,
	}
}

// calendarDaysBetween compares local calendar dates, so DST changes do not
// turn one sleep day into 23 or 25 hours.
func calendarDaysBetween(earlier, later time.Time) int {
	loc := later.Location()
	eY, eM, eD := earlier.In(loc).Date()
	lY, lM, lD := later.In(loc).Date()
	eUTC := time.Date(eY, eM, eD, 0, 0, 0, 0, time.UTC)
	lUTC := time.Date(lY, lM, lD, 0, 0, 0, 0, time.UTC)
	return int(lUTC.Sub(eUTC) / (24 * time.Hour))
}

func roundTenth(value float64) float64 {
	return math.Round(value*10) / 10
}

// DataFreshness describes how complete the sleep data is within the debt window.
type DataFreshness string

const (
	// FreshnessComplete indicates all 14 sleep days have main-sleep data.
	FreshnessComplete DataFreshness = "complete"
	// FreshnessRecent indicates 1-2 nights are estimated.
	FreshnessRecent DataFreshness = "recent"
	// FreshnessStale indicates 3-6 nights are estimated.
	FreshnessStale DataFreshness = "stale"
	// FreshnessInsufficient indicates fewer than 7 observed nights, an unstable
	// history with a material gap, or 7+ estimates.
	FreshnessInsufficient DataFreshness = "insufficient"
)

func categorize(hours float64) DebtCategory {
	switch {
	case hours < 1:
		return DebtNone
	case hours < 5:
		return DebtLow
	case hours < 10:
		return DebtModerate
	case hours < 20:
		return DebtHigh
	default:
		return DebtSevere
	}
}
