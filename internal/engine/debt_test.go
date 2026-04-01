package engine

import (
	"math"
	"testing"
	"time"
)

func TestCalculateSleepDebt_FullSleep(t *testing.T) {
	t.Parallel()
	// 14 nights of 8h sleep with 8h need → no debt.
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	var records []SleepRecord
	for i := range 14 {
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
	t.Parallel()
	// 14 nights of 6h sleep with 8h need → 2h deficit every night.
	// Cumulative: 2 × Σ(0.85^i, i=0..13) = 2 × 6.35 ≈ 12.7h
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	var records []SleepRecord
	for i := range 14 {
		date := ref.AddDate(0, 0, -i)
		records = append(records, SleepRecord{
			Date:            date,
			DurationMinutes: 360, // 6h
		})
	}

	debt := CalculateSleepDebt(records, 8.0, ref)

	// Cumulative debt should be significantly higher than per-night deficit.
	// Sum of 2 × 0.85^i for i=0..13 ≈ 12.7
	if debt.Hours < 10 || debt.Hours > 15 {
		t.Errorf("Expected ~12.7h cumulative debt for 14 nights of 6h, got %.1f", debt.Hours)
	}
	if debt.Category != DebtHigh {
		t.Errorf("Expected category 'high', got '%s'", debt.Category)
	}
}

func TestCalculateSleepDebt_RecentBadNight(t *testing.T) {
	t.Parallel()
	// 13 nights of 8h, last night only 4h → only last night has deficit.
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

	// Only night 0 has deficit: 4h × 0.85^0 = 4.0h
	if debt.Hours != 4.0 {
		t.Errorf("Expected 4.0h debt from one bad night, got %.1f", debt.Hours)
	}
	if debt.Category != DebtLow {
		t.Errorf("Expected category 'low', got '%s'", debt.Category)
	}
}

func TestCalculateSleepDebt_NoRecords(t *testing.T) {
	t.Parallel()
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	debt := CalculateSleepDebt(nil, 8.0, ref)

	// No data → no debt (we don't assume missing nights are 0h sleep).
	if debt.Hours != 0 {
		t.Errorf("Expected 0 debt with no records, got %.1f", debt.Hours)
	}
	if debt.Category != DebtNone {
		t.Errorf("Expected category 'none', got '%s'", debt.Category)
	}
}

func TestCalculateSleepDebt_SingleNight_FullSleep(t *testing.T) {
	t.Parallel()
	// 1 night of 8h with 8h need → no debt.
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	records := []SleepRecord{{
		Date:            ref,
		DurationMinutes: 480,
	}}

	debt := CalculateSleepDebt(records, 8.0, ref)

	if debt.Hours != 0 {
		t.Errorf("Expected 0 debt for single full night, got %.1f", debt.Hours)
	}
}

func TestCalculateSleepDebt_SingleNight_ShortSleep(t *testing.T) {
	t.Parallel()
	// 1 night of 5h with 8h need → 3h deficit × 0.85^0 = 3.0h
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	records := []SleepRecord{{
		Date:            ref,
		DurationMinutes: 300,
	}}

	debt := CalculateSleepDebt(records, 8.0, ref)

	if debt.Hours != 3.0 {
		t.Errorf("Expected 3h debt for single 5h night, got %.1f", debt.Hours)
	}
}

func TestCalculateSleepDebt_TwoNights(t *testing.T) {
	t.Parallel()
	// Night 0: 6h (deficit=2), Night 1: 7h (deficit=1). Need: 8h.
	// Cumulative: 2×0.85^0 + 1×0.85^1 = 2.0 + 0.85 = 2.85 → rounds to 2.9
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	records := []SleepRecord{
		{Date: ref, DurationMinutes: 360},
		{Date: ref.AddDate(0, 0, -1), DurationMinutes: 420},
	}

	debt := CalculateSleepDebt(records, 8.0, ref)

	expected := 2.0*1.0 + 1.0*0.85
	expected = math.Round(expected*10) / 10
	if debt.Hours != expected {
		t.Errorf("Expected %.1fh debt for two short nights, got %.1f", expected, debt.Hours)
	}
}

func TestCalculateSleepDebt_GapInData(t *testing.T) {
	t.Parallel()
	// Night 0: 8h (no deficit), Night 5: 4h (4h deficit).
	// Cumulative: 0 + 4×0.85^5 = 4×0.4437 = 1.775 → rounds to 1.8
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	records := []SleepRecord{
		{Date: ref, DurationMinutes: 480},
		{Date: ref.AddDate(0, 0, -5), DurationMinutes: 240},
	}

	debt := CalculateSleepDebt(records, 8.0, ref)

	expected := 4.0 * math.Pow(0.85, 5)
	expected = math.Round(expected*10) / 10
	if debt.Hours != expected {
		t.Errorf("Expected %.1fh debt from old bad night, got %.1f", expected, debt.Hours)
	}
}

func TestCalculateSleepDebt_Categories(t *testing.T) {
	t.Parallel()
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

func TestCalculateSleepDebt_TimezoneAheadOfUTC(t *testing.T) {
	t.Parallel()
	// Bug: record dates stored in UTC get wrong calendar day for UTC+ users.
	loc := time.FixedZone("AEST", 10*60*60) // UTC+10

	// Reference: Jan 17 10am AEST
	referenceDate := time.Date(2024, 1, 17, 10, 0, 0, 0, loc)

	// Record A: sleep starting midnight Jan 17 AEST = 14:00 UTC Jan 16
	// Locally this is daysAgo=0. In UTC calendar it's Jan 16 → daysAgo=1 (bug).
	recA := SleepRecord{
		Date:            time.Date(2024, 1, 16, 14, 0, 0, 0, time.UTC),
		DurationMinutes: 240, // 4h → 4h deficit
	}

	// Record B: sleep starting midnight Jan 16 AEST = 14:00 UTC Jan 15
	// Locally this is daysAgo=1. In UTC calendar it's Jan 15 → daysAgo=2 (bug).
	recB := SleepRecord{
		Date:            time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC),
		DurationMinutes: 480, // 8h → 0h deficit
	}

	debt := CalculateSleepDebt([]SleepRecord{recA, recB}, 8.0, referenceDate)

	// Correct (local dates): daysAgo 0 → 4h deficit × λ^0 = 4.0, daysAgo 1 → 0h deficit.
	wantDebt := 4.0 * 1 // = 4.0
	wantDebt = math.Round(wantDebt*10) / 10

	// Bug (UTC dates): daysAgo 1 → 4h deficit × λ^1 = 3.4
	bugDebt := 4.0 * 0.85
	bugDebt = math.Round(bugDebt*10) / 10

	t.Logf("want=%.1f (local dates), bug=%.1f (UTC dates), got=%.1f", wantDebt, bugDebt, debt.Hours)
	if debt.Hours == bugDebt && debt.Hours != wantDebt {
		t.Errorf("UTC+10 user: got %.1f (bug value), want %.1f — record dates not converted to local timezone",
			debt.Hours, wantDebt)
	}
}

func TestCalculateSleepDebt_DSTSpringForward(t *testing.T) {
	t.Parallel()
	// Use America/New_York: DST spring forward on second Sunday of March.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("timezone America/New_York not available")
	}

	// 2024 spring forward: March 10 at 2am.
	refDate := time.Date(2024, 3, 11, 10, 0, 0, 0, loc) // Monday morning

	// Record from night of March 9 (before DST). March 9 → March 11 = daysAgo=2.
	// DST spring forward means the period between March 9 00:00 EST and
	// March 11 00:00 EDT is 47h (not 48h), but rounding still gives daysAgo=2.
	rec := SleepRecord{
		Date:            time.Date(2024, 3, 9, 23, 0, 0, 0, loc),
		DurationMinutes: 360, // 6h → 2h deficit
	}

	debt := CalculateSleepDebt([]SleepRecord{rec}, 8.0, refDate)

	// Single record at daysAgo=2: 2h × 0.85^2 ≈ 1.445 → rounds to 1.4
	expected := 2.0 * 0.85 * 0.85
	expected = math.Round(expected*10) / 10
	if debt.Hours != expected {
		t.Errorf("DST spring forward: expected %.1fh debt, got %.1f", expected, debt.Hours)
	}
}

func TestCalculateSleepDebt_CumulativeAccumulation(t *testing.T) {
	t.Parallel()
	// Verify that debt accumulates properly over multiple nights.
	// 7 nights of 6h sleep (2h deficit each) should produce more debt
	// than 1 night of 6h sleep.
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// 1 night
	rec1 := []SleepRecord{{Date: ref, DurationMinutes: 360}}
	debt1 := CalculateSleepDebt(rec1, 8.0, ref)

	// 7 nights
	var rec7 []SleepRecord
	for i := range 7 {
		rec7 = append(rec7, SleepRecord{Date: ref.AddDate(0, 0, -i), DurationMinutes: 360})
	}
	debt7 := CalculateSleepDebt(rec7, 8.0, ref)

	// 14 nights
	var rec14 []SleepRecord
	for i := range 14 {
		rec14 = append(rec14, SleepRecord{Date: ref.AddDate(0, 0, -i), DurationMinutes: 360})
	}
	debt14 := CalculateSleepDebt(rec14, 8.0, ref)

	t.Logf("1 night: %.1fh, 7 nights: %.1fh, 14 nights: %.1fh", debt1.Hours, debt7.Hours, debt14.Hours)

	if debt7.Hours <= debt1.Hours {
		t.Errorf("7 nights (%.1f) should have more debt than 1 night (%.1f)", debt7.Hours, debt1.Hours)
	}
	if debt14.Hours <= debt7.Hours {
		t.Errorf("14 nights (%.1f) should have more debt than 7 nights (%.1f)", debt14.Hours, debt7.Hours)
	}
	// Cumulative ratio: 14 nights should have at least 2× the debt of 1 night
	if debt14.Hours < 2*debt1.Hours {
		t.Errorf("14 nights (%.1f) should have ≥2× the debt of 1 night (%.1f)", debt14.Hours, debt1.Hours)
	}
}

func TestCalculateSleepDebt_LastNightMissing(t *testing.T) {
	t.Parallel()
	ref := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// All 13 completed nights have data (daysAgo 1-13), but daysAgo=1
	// (last night) is missing.
	var records []SleepRecord
	for i := 2; i < 14; i++ { // skip daysAgo=0 (tonight) and daysAgo=1 (last night)
		records = append(records, SleepRecord{
			Date:            ref.AddDate(0, 0, -i),
			DurationMinutes: 480,
		})
	}
	debt := CalculateSleepDebt(records, 8.0, ref)
	if !debt.LastNightMissing {
		t.Error("expected LastNightMissing=true when daysAgo=1 has no data")
	}
	if debt.Freshness != FreshnessRecent {
		t.Errorf("expected FreshnessRecent, got %s", debt.Freshness)
	}

	// Now add last night's data (daysAgo=1) — LastNightMissing should become false.
	records = append(records, SleepRecord{
		Date:            ref.AddDate(0, 0, -1), // daysAgo=1
		DurationMinutes: 480,
	})
	debt = CalculateSleepDebt(records, 8.0, ref)
	if debt.LastNightMissing {
		t.Error("expected LastNightMissing=false when daysAgo=1 has data")
	}
	if debt.Freshness != FreshnessComplete {
		t.Errorf("expected FreshnessComplete, got %s", debt.Freshness)
	}

	// daysAgo=0 (tonight) missing should NOT affect freshness or LastNightMissing.
	// It's normal to not have tonight's data during the day.
	if debt.GapDays != 0 {
		t.Errorf("expected 0 gap days when all completed nights have data, got %d", debt.GapDays)
	}
}

func TestCalculateSleepDebt_ManualEntry4AM(t *testing.T) {
	t.Parallel()
	// Reproduces the exact user bug: entering 4am-8:18am sleep manually.
	// With SleepNightDate, 4am March 30 → date March 29 (last night).
	// Reference date is March 30 morning. daysAgo=1 = March 29 = has data.
	// The dashboard should NOT show "data pending for last night."
	est, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("EST timezone not available")
	}

	ref := time.Date(2026, 3, 30, 9, 0, 0, 0, est) // 9am today

	// 13 completed nights of data (daysAgo 1-13), each dated correctly.
	var records []SleepRecord
	for i := 1; i <= 13; i++ {
		nightDate := time.Date(2026, 3, 30-i, 0, 0, 0, 0, est)
		records = append(records, SleepRecord{
			Date:            nightDate,
			DurationMinutes: 258, // 4h18m
		})
	}

	debt := CalculateSleepDebt(records, 8.0, ref)

	// Last night (March 29 = daysAgo=1) has data → not missing.
	if debt.LastNightMissing {
		t.Error("expected LastNightMissing=false — user entered last night's sleep")
	}
	// All 13 completed nights have data → FreshnessComplete.
	if debt.Freshness != FreshnessComplete {
		t.Errorf("expected FreshnessComplete, got %s (gaps=%d)", debt.Freshness, debt.GapDays)
	}
	if debt.GapDays != 0 {
		t.Errorf("expected 0 gap days, got %d", debt.GapDays)
	}
	t.Logf("debt=%.1fh, freshness=%s, lastNightMissing=%v",
		debt.Hours, debt.Freshness, debt.LastNightMissing)
}
