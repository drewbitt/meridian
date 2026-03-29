package engine

import (
	"math"
	"sort"
	"time"
)

// DebtCategory classifies sleep debt severity.
type DebtCategory string

const (
	DebtNone     DebtCategory = "none"     // < 1h
	DebtLow      DebtCategory = "low"      // < 5h
	DebtModerate DebtCategory = "moderate" // < 10h
	DebtHigh     DebtCategory = "high"     // < 20h
	DebtSevere   DebtCategory = "severe"   // >= 20h
)

// SleepDebt holds the calculated sleep debt and its severity category.
type SleepDebt struct {
	Hours    float64      `json:"hours"`
	Category DebtCategory `json:"category"`
}

const debtWindowDays = 14

// CalculateSleepDebt computes a 14-day weighted rolling sleep debt.
// sleepNeedHours is the user's configured sleep need (typically 8.0).
// records should contain sleep records for the past 14 days.
// referenceDate is the date to calculate debt for (typically today).
func CalculateSleepDebt(records []SleepRecord, sleepNeedHours float64, referenceDate time.Time) SleepDebt {
	// Build a map of date → total sleep hours for the past 14 nights.
	// "Night i=0" is the most recent night (last night relative to referenceDate).
	dailySleep := make(map[int]float64)

	for _, r := range records {
		// Which night does this record belong to?
		// A sleep record's "night" is determined by its date field.
		nightDate := r.Date.Truncate(24 * time.Hour)
		refDate := referenceDate.Truncate(24 * time.Hour)
		daysAgo := int(refDate.Sub(nightDate).Hours() / 24)
		if daysAgo >= 0 && daysAgo < debtWindowDays {
			dailySleep[daysAgo] += float64(r.DurationMinutes) / 60.0
		}
	}

	// Generate recency weights.
	// Night 0 (last night) = 0.15, remaining 0.85 spread with exponential decay.
	weights := recencyWeights(debtWindowDays)

	var totalDeficit float64
	var totalWeight float64

	for i := 0; i < debtWindowDays; i++ {
		actual := dailySleep[i] // 0 if no data
		deficit := math.Max(0, sleepNeedHours-actual)
		totalDeficit += deficit * weights[i]
		totalWeight += weights[i]
	}

	debtHours := 0.0
	if totalWeight > 0 {
		debtHours = totalDeficit / totalWeight
	}
	debtHours = math.Round(debtHours*10) / 10

	return SleepDebt{
		Hours:    debtHours,
		Category: categorize(debtHours),
	}
}

// recencyWeights generates exponentially decaying weights for n days.
// Index 0 = most recent night (weight 0.15).
func recencyWeights(n int) []float64 {
	if n <= 0 {
		return nil
	}
	weights := make([]float64, n)
	weights[0] = 0.15

	// Distribute remaining 0.85 with exponential decay over days 1..n-1.
	if n > 1 {
		remaining := 0.85
		// Use decay factor so that each subsequent night has ~70% the weight of the prior.
		decayFactor := 0.7
		rawWeights := make([]float64, n-1)
		var rawSum float64
		for i := 0; i < n-1; i++ {
			rawWeights[i] = math.Pow(decayFactor, float64(i))
			rawSum += rawWeights[i]
		}
		for i := 0; i < n-1; i++ {
			weights[i+1] = remaining * rawWeights[i] / rawSum
		}
	}

	return weights
}

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

// SleepRecordsByDate returns records sorted by date ascending.
func SleepRecordsByDate(records []SleepRecord) []SleepRecord {
	sorted := make([]SleepRecord, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Date.Before(sorted[j].Date)
	})
	return sorted
}
