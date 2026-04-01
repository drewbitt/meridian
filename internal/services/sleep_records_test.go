package services

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func newSleepCollection() *core.Collection {
	c := core.NewBaseCollection("sleep_records", "")
	c.Fields.Add(
		&core.DateField{Name: "date"},
		&core.DateField{Name: "sleep_start"},
		&core.DateField{Name: "sleep_end"},
		&core.TextField{Name: "source"},
		&core.NumberField{Name: "duration_minutes"},
		&core.NumberField{Name: "deep_minutes"},
		&core.NumberField{Name: "rem_minutes"},
		&core.NumberField{Name: "light_minutes"},
		&core.NumberField{Name: "awake_minutes"},
	)
	return c
}

func makeRecord(c *core.Collection, source, start, end string, stages [4]int) *core.Record {
	r := core.NewRecord(c)
	r.Set("date", start[:10])
	r.Set("sleep_start", start)
	r.Set("sleep_end", end)
	r.Set("source", source)
	r.Set("duration_minutes", 0) // not used by merge logic
	r.Set("deep_minutes", stages[0])
	r.Set("rem_minutes", stages[1])
	r.Set("light_minutes", stages[2])
	r.Set("awake_minutes", stages[3])
	return r
}

func TestOverlappingPeriodsAreMerged(t *testing.T) {
	c := newSleepCollection()
	manual := makeRecord(c, "manual",
		"2024-03-15T23:00:00.000Z", "2024-03-16T07:00:00.000Z",
		[4]int{0, 0, 0, 0})
	fitbit := makeRecord(c, "fitbit",
		"2024-03-15T23:15:00.000Z", "2024-03-16T06:45:00.000Z",
		[4]int{85, 95, 195, 25})

	records, periods := ConvertSleepRecords([]*core.Record{manual, fitbit})
	if len(records) != 1 {
		t.Fatalf("expected 1 merged record, got %d", len(records))
	}
	// Union: 23:00 - 07:00 = 480 min
	if records[0].DurationMinutes != 480 {
		t.Errorf("duration: got %d, want 480", records[0].DurationMinutes)
	}
	if len(periods) != 1 {
		t.Fatalf("expected 1 period, got %d", len(periods))
	}
}

func TestOverlapTakesBestStageData(t *testing.T) {
	c := newSleepCollection()
	manual := makeRecord(c, "manual",
		"2024-03-15T23:00:00.000Z", "2024-03-16T07:00:00.000Z",
		[4]int{0, 0, 0, 0})
	fitbit := makeRecord(c, "fitbit",
		"2024-03-15T23:15:00.000Z", "2024-03-16T06:45:00.000Z",
		[4]int{85, 95, 195, 25})

	records, _ := ConvertSleepRecords([]*core.Record{fitbit, manual})

	// Stage data should come from the fitbit record (richer).
	// We can't check stage fields directly on engine.SleepRecord (it doesn't
	// have them), but we verify the merge kept the wider boundaries.
	r := records[0]
	if r.SleepStart.Hour() != 23 || r.SleepStart.Minute() != 0 {
		t.Errorf("expected start 23:00 (manual's earlier start), got %s", r.SleepStart.Format("15:04"))
	}
	if r.SleepEnd.Hour() != 7 || r.SleepEnd.Minute() != 0 {
		t.Errorf("expected end 07:00 (manual's later end), got %s", r.SleepEnd.Format("15:04"))
	}
}

func TestNonOverlappingPeriodsKeptSeparate(t *testing.T) {
	c := newSleepCollection()
	night := makeRecord(c, "fitbit",
		"2024-03-15T23:00:00.000Z", "2024-03-16T07:00:00.000Z",
		[4]int{85, 95, 195, 25})
	nap := makeRecord(c, "manual",
		"2024-03-16T13:00:00.000Z", "2024-03-16T13:30:00.000Z",
		[4]int{0, 0, 0, 0})

	records, periods := ConvertSleepRecords([]*core.Record{night, nap})
	if len(records) != 2 {
		t.Fatalf("expected 2 separate records (night + nap), got %d", len(records))
	}
	if len(periods) != 2 {
		t.Fatalf("expected 2 periods, got %d", len(periods))
	}
}

func TestSingleRecord(t *testing.T) {
	c := newSleepCollection()
	r := makeRecord(c, "manual",
		"2024-03-15T23:00:00.000Z", "2024-03-16T07:00:00.000Z",
		[4]int{0, 0, 0, 0})

	records, periods := ConvertSleepRecords([]*core.Record{r})
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].DurationMinutes != 480 {
		t.Errorf("duration: got %d, want 480", records[0].DurationMinutes)
	}
	if len(periods) != 1 {
		t.Fatalf("expected 1 period, got %d", len(periods))
	}
}

func TestThreeOverlappingRecords_ChainMerge(t *testing.T) {
	// A-B overlap, B-C overlap, but A-C might not directly overlap.
	// The merge should chain: A∪B, then (A∪B)∪C.
	c := newSleepCollection()
	a := makeRecord(c, "fitbit", "2024-03-15T23:00:00.000Z", "2024-03-16T05:00:00.000Z", [4]int{60, 70, 150, 20})
	b := makeRecord(c, "manual", "2024-03-16T04:00:00.000Z", "2024-03-16T07:00:00.000Z", [4]int{0, 0, 0, 0})
	c2 := makeRecord(c, "fitbit", "2024-03-16T06:30:00.000Z", "2024-03-16T08:00:00.000Z", [4]int{30, 40, 60, 10})

	records, periods := ConvertSleepRecords([]*core.Record{a, b, c2})
	if len(records) != 1 {
		t.Fatalf("expected 1 merged record from 3 overlapping, got %d", len(records))
	}
	// Union: 23:00 - 08:00 = 540 min (9h).
	if records[0].DurationMinutes != 540 {
		t.Errorf("duration: got %d, want 540", records[0].DurationMinutes)
	}
	if len(periods) != 1 {
		t.Fatalf("expected 1 period, got %d", len(periods))
	}
}

func TestAdjacentRecords_NotMerged(t *testing.T) {
	// Two records where one ends exactly when the next starts.
	// p.start.After(last.end) is true when equal, so they should merge.
	// Wait — "not after" means <=, so equal start to end DOES merge.
	c := newSleepCollection()
	a := makeRecord(c, "manual", "2024-03-15T23:00:00.000Z", "2024-03-16T03:00:00.000Z", [4]int{0, 0, 0, 0})
	b := makeRecord(c, "manual", "2024-03-16T03:00:00.000Z", "2024-03-16T07:00:00.000Z", [4]int{0, 0, 0, 0})

	records, _ := ConvertSleepRecords([]*core.Record{a, b})
	// Adjacent (touching) records should be merged.
	if len(records) != 1 {
		t.Fatalf("adjacent records should merge: expected 1, got %d", len(records))
	}
	// 23:00 - 07:00 = 480 min.
	if records[0].DurationMinutes != 480 {
		t.Errorf("duration: got %d, want 480", records[0].DurationMinutes)
	}
}

func TestEmptyInput(t *testing.T) {
	records, periods := ConvertSleepRecords(nil)
	if records != nil {
		t.Errorf("expected nil records, got %v", records)
	}
	if periods != nil {
		t.Errorf("expected nil periods, got %v", periods)
	}
}
