package engine

import (
	"math"
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
	LastNightMissing bool          `json:"last_night_missing"` // true when daysAgo=1 (last completed night) has no data
}

const debtWindowDays = 14

// CalculateSleepDebt computes a 14-day cumulative weighted sleep debt.
// sleepNeedHours is the user's configured sleep need (typically 8.0).
// records should contain sleep records for the past 14 days.
// referenceDate is the date to calculate debt for (typically today).
//
// The calculation uses exponential decay weights (λ=0.85 per day) to sum
// each night's deficit. Unlike a weighted average (which would always return
// the per-night deficit for uniform restriction), this cumulative approach
// naturally accumulates debt over consecutive nights of insufficient sleep.
//
// Example: 14 nights of 6h sleep (2h nightly deficit) produces ~12.7h of
// cumulative debt, matching RISE's reported behavior and Van Dongen et al.
// (2003) finding that chronic restriction produces dose-dependent impairment.
func CalculateSleepDebt(records []SleepRecord, sleepNeedHours float64, referenceDate time.Time) SleepDebt {
	// Build a map of date → total sleep hours for the past 14 nights.
	// Use calendar date arithmetic (Year/Month/Day) instead of
	// Truncate(24h) which rounds to UTC midnight and misaligns for
	// non-UTC timezones.
	dailySleep := make(map[int]float64)

	refY, refM, refD := referenceDate.Date()
	refDate := time.Date(refY, refM, refD, 0, 0, 0, 0, referenceDate.Location())

	for _, r := range records {
		// Convert record date to user's local timezone before extracting calendar date.
		// Records from PocketBase are in UTC; without this conversion, a sleep at
		// midnight AEST (14:00 UTC) would be assigned to the previous UTC calendar day.
		localRecordDate := r.Date.In(referenceDate.Location())
		rY, rM, rD := localRecordDate.Date()
		nightDate := time.Date(rY, rM, rD, 0, 0, 0, 0, referenceDate.Location())
		daysAgo := int(refDate.Sub(nightDate).Hours()/24 + 0.5) // round to nearest day
		if daysAgo >= 0 && daysAgo < debtWindowDays {
			dailySleep[daysAgo] += float64(r.DurationMinutes) / 60.0
		}
	}

	// Cumulative weighted debt: sum each night's deficit × decay weight.
	// Weight_i = λ^i where λ = 0.85, so recent nights contribute more.
	// The sum naturally accumulates — 14 days of identical deficits produce
	// ~6.3× the single-night deficit (vs 1.0× with a weighted average).
	const lambda = 0.85
	var totalDebt float64
	var hasAnyData bool
	daysWithData := 0

	// Include daysAgo=0: this typically represents last night's sleep dated to
	// today (e.g., went to bed at 1am, record dated today). Excluding it would
	// drop the most recent complete night for most users. The gap counting below
	// does exclude daysAgo=0 because a missing record for "tonight" is normal
	// during the day — but debt summation should use it when present.
	for i := range debtWindowDays {
		actual, hasData := dailySleep[i]
		if !hasData {
			continue // skip nights with no data
		}
		hasAnyData = true
		daysWithData++
		deficit := math.Max(0, sleepNeedHours-actual)
		weight := math.Pow(lambda, float64(i))
		totalDebt += deficit * weight
	}

	// "Last night" is daysAgo=1 (the most recent completed night).
	// daysAgo=0 is the current/upcoming night which typically has no data
	// yet during the day — it should not count as a gap or trigger warnings.
	_, hasLastNight := dailySleep[1]

	if !hasAnyData {
		return SleepDebt{
			Hours: 0, Category: DebtNone,
			GapDays: debtWindowDays - 1, Freshness: FreshnessInsufficient,
			LastNightMissing: true,
		}
	}

	totalDebt = math.Round(totalDebt*10) / 10

	// Exclude daysAgo=0 from gap counting: the current night hasn't
	// completed yet, so missing data for "tonight" is normal.
	completedNights := 0
	for i := 1; i < debtWindowDays; i++ {
		if _, ok := dailySleep[i]; ok {
			completedNights++
		}
	}
	gapDays := (debtWindowDays - 1) - completedNights // 13 completed nights max
	var freshness DataFreshness
	switch {
	case gapDays == 0:
		freshness = FreshnessComplete
	case gapDays <= 2:
		freshness = FreshnessRecent
	case gapDays <= 6:
		freshness = FreshnessStale
	default:
		freshness = FreshnessInsufficient
	}

	return SleepDebt{
		Hours:            totalDebt,
		Category:         categorize(totalDebt),
		GapDays:          gapDays,
		Freshness:        freshness,
		LastNightMissing: !hasLastNight,
	}
}

// DataFreshness describes how complete the sleep data is within the debt window.
type DataFreshness string

const (
	// FreshnessComplete indicates all completed nights have sleep data.
	FreshnessComplete DataFreshness = "complete"
	// FreshnessRecent indicates 1-2 nights are missing.
	FreshnessRecent DataFreshness = "recent"
	// FreshnessStale indicates 3-6 nights are missing (sync failure or no tracker).
	FreshnessStale DataFreshness = "stale"
	// FreshnessInsufficient indicates 7+ nights are missing (new user or extended gap).
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
