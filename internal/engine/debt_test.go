package engine

import (
	"testing"
	"time"
)

func TestCalculateSleepDebt_FullSleep(t *testing.T) {
	// 14 nights of 8h sleep with 8h need → no debt.
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	var records []SleepRecord
	for i := 0; i < 14; i++ {
		date := ref.AddDate(0, 0, -i)
		records = append(records, SleepRecord{
			Date:            date,
			DurationMinutes: 480, // 8h
		})
	}

	debt := CalculateSleepDebt(records, 8.0, ref)

	if debt.Hours != 0 {
		t.Errorf("Expected 0 debt, got %.1f", debt.Hours)
	}
	if debt.Category != DebtNone {
		t.Errorf("Expected category 'none', got '%s'", debt.Category)
	}
}

func TestCalculateSleepDebt_ConsistentShortSleep(t *testing.T) {
	// 14 nights of 6h sleep with 8h need → 2h deficit every night.
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	var records []SleepRecord
	for i := 0; i < 14; i++ {
		date := ref.AddDate(0, 0, -i)
		records = append(records, SleepRecord{
			Date:            date,
			DurationMinutes: 360, // 6h
		})
	}

	debt := CalculateSleepDebt(records, 8.0, ref)

	// Every night is 2h short, so weighted average should be ~2h.
	if debt.Hours < 1.5 || debt.Hours > 2.5 {
		t.Errorf("Expected ~2h debt, got %.1f", debt.Hours)
	}
	if debt.Category != DebtLow {
		t.Errorf("Expected category 'low', got '%s'", debt.Category)
	}
}

func TestCalculateSleepDebt_RecentBadNight(t *testing.T) {
	// 13 nights of 8h, last night only 4h → recent weight should dominate.
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	var records []SleepRecord

	// Last night: 4h
	records = append(records, SleepRecord{
		Date:            ref,
		DurationMinutes: 240,
	})

	// Prior 13 nights: 8h
	for i := 1; i < 14; i++ {
		date := ref.AddDate(0, 0, -i)
		records = append(records, SleepRecord{
			Date:            date,
			DurationMinutes: 480,
		})
	}

	debt := CalculateSleepDebt(records, 8.0, ref)

	// Last night has weight 0.15 with 4h deficit, others have 0.
	// Debt should be positive but not huge.
	if debt.Hours <= 0 {
		t.Error("Expected positive debt after one bad night")
	}
	if debt.Hours > 4.0 {
		t.Errorf("Debt seems too high for one bad night: %.1f", debt.Hours)
	}
}

func TestCalculateSleepDebt_NoRecords(t *testing.T) {
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	debt := CalculateSleepDebt(nil, 8.0, ref)

	// No data means maximum sleep need assumed as deficit.
	if debt.Hours < 7.0 {
		t.Errorf("Expected high debt with no records, got %.1f", debt.Hours)
	}
}

func TestCalculateSleepDebt_Categories(t *testing.T) {
	tests := []struct {
		hours    float64
		expected DebtCategory
	}{
		{0.5, DebtNone},
		{3.0, DebtLow},
		{7.0, DebtModerate},
		{15.0, DebtHigh},
		{25.0, DebtSevere},
	}

	for _, tt := range tests {
		got := categorize(tt.hours)
		if got != tt.expected {
			t.Errorf("categorize(%.1f) = %s, want %s", tt.hours, got, tt.expected)
		}
	}
}

func TestRecencyWeights(t *testing.T) {
	weights := recencyWeights(14)

	if len(weights) != 14 {
		t.Fatalf("Expected 14 weights, got %d", len(weights))
	}

	// First weight should be 0.15.
	if weights[0] != 0.15 {
		t.Errorf("Expected first weight 0.15, got %f", weights[0])
	}

	// Weights should sum to ~1.0.
	var sum float64
	for _, w := range weights {
		sum += w
	}
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("Expected weights to sum to ~1.0, got %f", sum)
	}

	// Weights should be monotonically decreasing after index 0.
	for i := 2; i < len(weights); i++ {
		if weights[i] > weights[i-1] {
			t.Errorf("Expected decreasing weights: w[%d]=%.4f > w[%d]=%.4f", i, weights[i], i-1, weights[i-1])
		}
	}
}
