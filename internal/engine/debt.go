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
	// Use calendar date arithmetic (Year/Month/Day) instead of
	// Truncate(24h) which rounds to UTC midnight and misaligns for
	// non-UTC timezones.
	dailySleep := make(map[int]float64)

	refY, refM, refD := referenceDate.Date()
	refDate := time.Date(refY, refM, refD, 0, 0, 0, 0, referenceDate.Location())

	for _, r := range records {
		rY, rM, rD := r.Date.Date()
		nightDate := time.Date(rY, rM, rD, 0, 0, 0, 0, referenceDate.Location())
		daysAgo := int(refDate.Sub(nightDate).Hours()/24 + 0.5) // round to nearest day
		if daysAgo >= 0 && daysAgo < debtWindowDays {
			dailySleep[daysAgo] += float64(r.DurationMinutes) / 60.0
		}
	}

	// Generate recency weights.
	// Night 0 (last night) = 0.15, remaining 0.85 spread with exponential decay.
	weights := recencyWeights(debtWindowDays)

	var totalDeficit float64
	var totalWeight float64

	for i := range debtWindowDays {
		actual, hasData := dailySleep[i]
		if !hasData {
			continue // skip nights with no data — only weight days we know about
		}
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

	if n > 1 {
		remaining := 0.85
		decayFactor := 0.7
		sum := 0.0
		for i := range n - 1 {
			w := math.Pow(decayFactor, float64(i))
			weights[1+i] = w
			sum += w
		}
		scale := remaining / sum
		for i := 1; i < n; i++ {
			weights[i] *= scale
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
