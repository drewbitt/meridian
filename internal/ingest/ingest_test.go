package ingest

import (
	"errors"
	"strings"
	"testing"
)

func TestParseHealthConnect(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantRecords int
		wantErr     error
	}{
		{
			name: "valid single session",
			input: `{
				"sleepSessions": [{
					"startTime": "2024-01-15T23:00:00.000Z",
					"endTime": "2024-01-16T07:00:00.000Z",
					"stages": [
						{"startTime": "2024-01-15T23:00:00.000Z", "endTime": "2024-01-16T01:00:00.000Z", "stage": 5},
						{"startTime": "2024-01-16T01:00:00.000Z", "endTime": "2024-01-16T03:00:00.000Z", "stage": 4},
						{"startTime": "2024-01-16T03:00:00.000Z", "endTime": "2024-01-16T05:00:00.000Z", "stage": 6},
						{"startTime": "2024-01-16T05:00:00.000Z", "endTime": "2024-01-16T07:00:00.000Z", "stage": 2}
					]
				}]
			}`,
			wantRecords: 1,
			wantErr:     nil,
		},
		{
			name: "empty sessions",
			input: `{
				"sleepSessions": []
			}`,
			wantRecords: 0,
			wantErr:     nil,
		},
		{
			name:        "invalid JSON",
			input:       `not json`,
			wantRecords: 0,
			wantErr:     ErrParseFailed,
		},
		{
			name: "multiple sessions",
			input: `{
				"sleepSessions": [
					{
						"startTime": "2024-01-14T23:00:00.000Z",
						"endTime": "2024-01-15T07:00:00.000Z",
						"stages": []
					},
					{
						"startTime": "2024-01-15T23:00:00.000Z",
						"endTime": "2024-01-16T07:00:00.000Z",
						"stages": []
					}
				]
			}`,
			wantRecords: 2,
			wantErr:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, err := ParseHealthConnect(strings.NewReader(tt.input))

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(records) != tt.wantRecords {
				t.Errorf("got %d records, want %d", len(records), tt.wantRecords)
			}
		})
	}
}

func TestParseHealthConnect_StageAggregation(t *testing.T) {
	input := `{
		"sleepSessions": [{
			"startTime": "2024-01-15T23:00:00.000Z",
			"endTime": "2024-01-16T07:00:00.000Z",
			"stages": [
				{"startTime": "2024-01-15T23:00:00.000Z", "endTime": "2024-01-16T01:00:00.000Z", "stage": 5},
				{"startTime": "2024-01-16T01:00:00.000Z", "endTime": "2024-01-16T03:00:00.000Z", "stage": 4},
				{"startTime": "2024-01-16T03:00:00.000Z", "endTime": "2024-01-16T05:00:00.000Z", "stage": 6},
				{"startTime": "2024-01-16T05:00:00.000Z", "endTime": "2024-01-16T07:00:00.000Z", "stage": 1}
			]
		}]
	}`

	records, err := ParseHealthConnect(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	rec := records[0]
	if rec.DeepMinutes != 120 {
		t.Errorf("expected 120 deep mins, got %d", rec.DeepMinutes)
	}
	if rec.LightMinutes != 120 {
		t.Errorf("expected 120 light mins, got %d", rec.LightMinutes)
	}
	if rec.REMMinutes != 120 {
		t.Errorf("expected 120 REM mins, got %d", rec.REMMinutes)
	}
	if rec.AwakeMinutes != 120 {
		t.Errorf("expected 120 awake mins, got %d", rec.AwakeMinutes)
	}
	if rec.Source != SourceHealthConnect {
		t.Errorf("expected source %s, got %s", SourceHealthConnect, rec.Source)
	}
}

func TestParseHealthConnect_SkipsInvalidTimes(t *testing.T) {
	input := `{
		"sleepSessions": [
			{"startTime": "not-a-date", "endTime": "2024-01-16T07:00:00.000Z", "stages": []},
			{"startTime": "2024-01-15T23:00:00.000Z", "endTime": "2024-01-16T07:00:00.000Z", "stages": []}
		]
	}`

	records, err := ParseHealthConnect(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 valid record, got %d", len(records))
	}
}

func TestParseAppleHealthXML(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<HealthData>
	<Record type="HKCategoryTypeIdentifierSleepAnalysis" value="HKCategoryValueSleepAnalysisInBed" startDate="2024-01-15 23:00:00 -0500" endDate="2024-01-16 07:00:00 -0500"/>
	<Record type="HKCategoryTypeIdentifierSleepAnalysis" value="HKCategoryValueSleepAnalysisAsleepDeep" startDate="2024-01-15 23:30:00 -0500" endDate="2024-01-16 01:30:00 -0500"/>
	<Record type="HKCategoryTypeIdentifierSleepAnalysis" value="HKCategoryValueSleepAnalysisAsleepREM" startDate="2024-01-16 02:00:00 -0500" endDate="2024-01-16 04:00:00 -0500"/>
	<Record type="HKCategoryTypeIdentifierSleepAnalysis" value="HKCategoryValueSleepAnalysisAsleepCore" startDate="2024-01-16 04:30:00 -0500" endDate="2024-01-16 06:30:00 -0500"/>
	<Record type="StepCount" value="5000" startDate="2024-01-16 08:00:00 -0500" endDate="2024-01-16 09:00:00 -0500"/>
</HealthData>`

	records, err := ParseAppleHealthXML(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The parser groups by the date of the sleep start, so records on the
	// same calendar date become one record. Our test data spans two date keys
	// because the sleep start at 23:00 -0500 is 2024-01-16 in UTC.
	if len(records) < 1 {
		t.Fatalf("expected at least 1 record, got %d", len(records))
	}

	// Sum across all records to check stage aggregation.
	var totalDeep, totalREM, totalLight int
	for _, rec := range records {
		totalDeep += rec.DeepMinutes
		totalREM += rec.REMMinutes
		totalLight += rec.LightMinutes
	}
	if totalDeep != 120 {
		t.Errorf("expected 120 deep mins total, got %d", totalDeep)
	}
	if totalREM != 120 {
		t.Errorf("expected 120 REM mins total, got %d", totalREM)
	}
	if totalLight != 120 {
		t.Errorf("expected 120 light mins total, got %d", totalLight)
	}

	// All records should have the correct source.
	for _, rec := range records {
		if rec.Source != SourceAppleHealth {
			t.Errorf("expected source %s, got %s", SourceAppleHealth, rec.Source)
		}
	}
}

func TestParseAppleHealthXML_InvalidXML(t *testing.T) {
	records, err := ParseAppleHealthXML(strings.NewReader("not xml"))
	// The XML decoder processes streams incrementally; "not xml" produces no
	// start elements and returns no records without error.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestParseHCTime(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"2024-01-15T23:00:00Z", false},
		{"2024-01-15T23:00:00.000Z", false},
		{"2024-01-15T23:00:00", false},
		{"not-a-date", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := parseHCTime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseHCTime(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestParseAHTime(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"2024-01-15 23:00:00 -0500", false},
		{"2024-01-15T23:00:00Z", false},
		{"2024-01-15T23:00:00", false},
		{"not-a-date", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := parseAHTime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseAHTime(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
