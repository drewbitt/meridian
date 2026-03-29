package ingest

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	_ "modernc.org/sqlite"
)

func openTestdata(t *testing.T, name string) io.Reader {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open testdata/%s: %v", name, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func createTestDBConn(t *testing.T, setup func(db *sql.DB)) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	setup(db)
	return db
}

func createTestDBPath(t *testing.T, setup func(db *sql.DB)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gb.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup(db)
	return path
}

// ===== Health Connect =====

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
		},
		{
			name:        "empty sessions",
			input:       `{"sleepSessions": []}`,
			wantRecords: 0,
		},
		{
			name:        "invalid JSON",
			input:       `not json`,
			wantErr:     ErrParseFailed,
			wantRecords: 0,
		},
		{
			name: "multiple sessions",
			input: `{
				"sleepSessions": [
					{"startTime": "2024-01-14T23:00:00.000Z", "endTime": "2024-01-15T07:00:00.000Z", "stages": []},
					{"startTime": "2024-01-15T23:00:00.000Z", "endTime": "2024-01-16T07:00:00.000Z", "stages": []}
				]
			}`,
			wantRecords: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, err := ParseHealthConnect(strings.NewReader(tt.input))
			if tt.wantErr != nil {
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

func TestParseHealthConnect_AllStageTypes(t *testing.T) {
	r := openTestdata(t, "healthconnect_all_stages.json")
	records, err := ParseHealthConnect(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(records))
	}

	rec := records[0]
	if rec.Source != SourceHealthConnect {
		t.Errorf("expected source %s, got %s", SourceHealthConnect, rec.Source)
	}
	if rec.DurationMinutes != 480 {
		t.Errorf("expected 480 total minutes, got %d", rec.DurationMinutes)
	}

	// Session 1 stages:
	//   Awake(1):     15 + 15 = 30min
	//   Light(4):     75 + 90 = 165min
	//   Deep(5):      90 + 45 = 135min
	//   Sleeping(2):  45min -> light
	//   REM(6):       90min
	//   OutOfBed(3):  15min -> awake
	if rec.DeepMinutes != 135 {
		t.Errorf("session 1 deep: got %d, want 135", rec.DeepMinutes)
	}
	if rec.REMMinutes != 90 {
		t.Errorf("session 1 rem: got %d, want 90", rec.REMMinutes)
	}
	if rec.LightMinutes != 210 {
		t.Errorf("session 1 light: got %d, want 210", rec.LightMinutes)
	}
	if rec.AwakeMinutes != 45 {
		t.Errorf("session 1 awake: got %d, want 45", rec.AwakeMinutes)
	}

	rec2 := records[1]
	// Session 2 stages:
	//   Deep(5):  120min
	//   Light(4): 90 + 150 = 240min
	//   REM(6):   120min
	if rec2.DeepMinutes != 120 {
		t.Errorf("session 2 deep: got %d, want 120", rec2.DeepMinutes)
	}
	if rec2.REMMinutes != 120 {
		t.Errorf("session 2 rem: got %d, want 120", rec2.REMMinutes)
	}
	if rec2.LightMinutes != 240 {
		t.Errorf("session 2 light: got %d, want 240", rec2.LightMinutes)
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

func TestParseHealthConnect_SleepNightDate(t *testing.T) {
	input := `{
		"sleepSessions": [{
			"startTime": "2024-03-11T01:30:00+01:00",
			"endTime": "2024-03-11T08:00:00+01:00",
			"stages": []
		}]
	}`

	records, err := ParseHealthConnect(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	loc := records[0].Date.Location()
	wantDate := time.Date(2024, 3, 10, 0, 0, 0, 0, loc)
	if !records[0].Date.Equal(wantDate) {
		t.Errorf("date: got %v, want %v", records[0].Date, wantDate)
	}
}

// ===== Apple Health =====

func TestParseAppleHealthXML_iOS16(t *testing.T) {
	r := openTestdata(t, "applehealth_realistic.xml")
	records, err := ParseAppleHealthXML(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 night, got %d", len(records))
	}

	rec := records[0]
	if rec.Source != SourceAppleHealth {
		t.Errorf("expected source %s, got %s", SourceAppleHealth, rec.Source)
	}

	// Stages from testdata (all on dateKey 2024-03-11 in -0500):
	//   InBed:  00:30-08:00 = not counted
	//   Awake:  00:30-00:45 = 15min
	//   Deep:   00:45-02:45 = 120min
	//   REM:    03:00-04:30 = 90min
	//   Core:   04:45-06:15 = 90min
	//   Awake:  06:15-06:30 = 15min
	//   Core:   06:30-07:30 = 60min
	if rec.DeepMinutes != 120 {
		t.Errorf("deep: got %d, want 120", rec.DeepMinutes)
	}
	if rec.REMMinutes != 90 {
		t.Errorf("rem: got %d, want 90", rec.REMMinutes)
	}
	if rec.LightMinutes != 150 {
		t.Errorf("light: got %d, want 150", rec.LightMinutes)
	}
	if rec.AwakeMinutes != 30 {
		t.Errorf("awake: got %d, want 30", rec.AwakeMinutes)
	}

	wantStart, _ := time.Parse("2006-01-02 15:04:05 -0700", "2024-03-11 00:30:00 -0500")
	wantEnd, _ := time.Parse("2006-01-02 15:04:05 -0700", "2024-03-11 08:00:00 -0500")
	if !rec.SleepStart.Equal(wantStart) {
		t.Errorf("sleep start: got %v, want %v", rec.SleepStart, wantStart)
	}
	if !rec.SleepEnd.Equal(wantEnd) {
		t.Errorf("sleep end: got %v, want %v", rec.SleepEnd, wantEnd)
	}
}

func TestParseAppleHealthXML_iOS15Legacy(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<HealthData locale="en_US">
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Clock" value="HKCategoryValueSleepAnalysisInBed" startDate="2024-03-11 00:15:00 -0500" endDate="2024-03-11 07:30:00 -0500"/>
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Clock" value="HKCategoryValueSleepAnalysisAsleep" startDate="2024-03-11 00:30:00 -0500" endDate="2024-03-11 07:15:00 -0500"/>
</HealthData>`

	records, err := ParseAppleHealthXML(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	rec := records[0]
	// Asleep (pre-iOS 16) maps to light: 00:30-07:15 = 405min
	if rec.LightMinutes != 405 {
		t.Errorf("light: got %d, want 405", rec.LightMinutes)
	}
	if rec.Source != SourceAppleHealth {
		t.Errorf("expected source %s, got %s", SourceAppleHealth, rec.Source)
	}
}

func TestParseAppleHealthXML_AsleepUnspecified(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<HealthData locale="en_US">
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Apple Watch" value="HKCategoryValueSleepAnalysisInBed" startDate="2024-03-11 00:00:00 -0500" endDate="2024-03-11 07:00:00 -0500"/>
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Apple Watch" value="HKCategoryValueSleepAnalysisAsleepUnspecified" startDate="2024-03-11 00:15:00 -0500" endDate="2024-03-11 06:45:00 -0500"/>
</HealthData>`

	records, err := ParseAppleHealthXML(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	rec := records[0]
	// AsleepUnspecified maps to light: 00:15-06:45 = 390min
	if rec.LightMinutes != 390 {
		t.Errorf("light: got %d, want 390", rec.LightMinutes)
	}
}

func TestParseAppleHealthXML_CrossMidnight(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<HealthData locale="en_US">
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Apple Watch" value="HKCategoryValueSleepAnalysisInBed" startDate="2024-03-10 23:00:00 -0500" endDate="2024-03-11 07:00:00 -0500"/>
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Apple Watch" value="HKCategoryValueSleepAnalysisAsleepDeep" startDate="2024-03-10 23:15:00 -0500" endDate="2024-03-11 01:00:00 -0500"/>
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Apple Watch" value="HKCategoryValueSleepAnalysisAsleepREM" startDate="2024-03-11 01:15:00 -0500" endDate="2024-03-11 03:00:00 -0500"/>
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Apple Watch" value="HKCategoryValueSleepAnalysisAsleepCore" startDate="2024-03-11 03:15:00 -0500" endDate="2024-03-11 06:45:00 -0500"/>
</HealthData>`

	records, err := ParseAppleHealthXML(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 night (cross-midnight grouped), got %d", len(records))
	}

	rec := records[0]
	// Deep: 23:15-01:00 = 105min
	if rec.DeepMinutes != 105 {
		t.Errorf("deep: got %d, want 105", rec.DeepMinutes)
	}
	// REM: 01:15-03:00 = 105min
	if rec.REMMinutes != 105 {
		t.Errorf("rem: got %d, want 105", rec.REMMinutes)
	}
	// Core: 03:15-06:45 = 210min
	if rec.LightMinutes != 210 {
		t.Errorf("light: got %d, want 210", rec.LightMinutes)
	}

	wantStart, _ := time.Parse("2006-01-02 15:04:05 -0700", "2024-03-10 23:00:00 -0500")
	wantEnd, _ := time.Parse("2006-01-02 15:04:05 -0700", "2024-03-11 07:00:00 -0500")
	if !rec.SleepStart.Equal(wantStart) {
		t.Errorf("sleep start: got %v, want %v", rec.SleepStart, wantStart)
	}
	if !rec.SleepEnd.Equal(wantEnd) {
		t.Errorf("sleep end: got %v, want %v", rec.SleepEnd, wantEnd)
	}

	wantDate := time.Date(2024, 3, 10, 0, 0, 0, 0, wantStart.Location())
	if !rec.Date.Equal(wantDate) {
		t.Errorf("date: got %v, want %v", rec.Date, wantDate)
	}
}

func TestParseAppleHealthXML_NoSleepRecords(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<HealthData locale="en_US">
  <Record type="HKQuantityTypeIdentifierStepCount" sourceName="iPhone" unit="count" value="5000" startDate="2024-03-11 08:00:00 -0500" endDate="2024-03-11 09:00:00 -0500"/>
  <Record type="HKQuantityTypeIdentifierHeartRate" sourceName="Apple Watch" unit="count/min" value="62" startDate="2024-03-11 06:00:00 -0500" endDate="2024-03-11 06:00:00 -0500"/>
</HealthData>`

	records, err := ParseAppleHealthXML(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestParseAppleHealthXML_InvalidXML(t *testing.T) {
	records, err := ParseAppleHealthXML(strings.NewReader("not xml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestParseAppleHealthXML_TruncatedXML(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<HealthData locale="en_US">
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Apple Watch" value="HKCategoryValueSleepAnalysisAsleepDeep" startDate="2024-03-11 00:00:00 -0500" endDate="`
	_, err := ParseAppleHealthXML(strings.NewReader(input))
	if err == nil {
		t.Error("expected error from truncated XML")
	}
}

func createAppleHealthZip(t *testing.T, xmlContent string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "apple_health_export.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	wf, err := w.Create("apple_health_export/export.xml")
	if err != nil {
		t.Fatal(err)
	}
	wf.Write([]byte(xmlContent))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseAppleHealthZip(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<HealthData locale="en_US">
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Apple Watch" value="HKCategoryValueSleepAnalysisInBed" startDate="2024-03-11 00:30:00 -0500" endDate="2024-03-11 07:00:00 -0500"/>
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Apple Watch" value="HKCategoryValueSleepAnalysisAsleepCore" startDate="2024-03-11 01:00:00 -0500" endDate="2024-03-11 06:30:00 -0500"/>
</HealthData>`

	zipPath := createAppleHealthZip(t, xmlContent)
	records, err := ParseAppleHealthZip(zipPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].LightMinutes != 330 {
		t.Errorf("light: got %d, want 330", records[0].LightMinutes)
	}
}

func TestParseAppleHealthZip_NoExportXML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	wf, err := w.Create("other_file.txt")
	if err != nil {
		t.Fatal(err)
	}
	wf.Write([]byte("not export.xml"))
	w.Close()
	f.Close()

	_, err = ParseAppleHealthZip(path)
	if !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("expected ErrInvalidFile, got %v", err)
	}
}

func TestParseAppleHealthFile(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<HealthData>
  <Record type="HKCategoryTypeIdentifierSleepAnalysis" value="HKCategoryValueSleepAnalysisAsleepCore" startDate="2024-03-11 01:00:00 -0500" endDate="2024-03-11 06:00:00 -0500"/>
</HealthData>`

	t.Run("xml file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "export.xml")
		os.WriteFile(path, []byte(xmlContent), 0644)
		records, err := ParseAppleHealthFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("expected 1 record, got %d", len(records))
		}
	})

	t.Run("zip file", func(t *testing.T) {
		zipPath := createAppleHealthZip(t, xmlContent)
		records, err := ParseAppleHealthFile(zipPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("expected 1 record, got %d", len(records))
		}
	})
}

// ===== Gadgetbridge =====

func TestParseGadgetbridge_SleepSessions(t *testing.T) {
	db := createTestDBConn(t, func(db *sql.DB) {
		db.Exec(`CREATE TABLE SLEEP_SESSION (
			TIMESTAMP_START INTEGER,
			TIMESTAMP_END INTEGER,
			DEEP_SLEEP_MINUTES INTEGER,
			REM_SLEEP_MINUTES INTEGER,
			LIGHT_SLEEP_MINUTES INTEGER,
			AWAKE_MINUTES INTEGER
		)`)
		db.Exec(`INSERT INTO SLEEP_SESSION VALUES (1710108000, 1710136800, 95, 72, 210, 35)`)
		db.Exec(`INSERT INTO SLEEP_SESSION VALUES (1710194400, 1710223200, 80, 60, 240, 20)`)
	})

	records, err := parseGBSleepSessions(db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	rec := records[0]
	if rec.Source != SourceGadgetbridge {
		t.Errorf("expected source %s, got %s", SourceGadgetbridge, rec.Source)
	}
	if rec.DeepMinutes != 95 {
		t.Errorf("deep: got %d, want 95", rec.DeepMinutes)
	}
	if rec.REMMinutes != 72 {
		t.Errorf("rem: got %d, want 72", rec.REMMinutes)
	}
	if rec.LightMinutes != 210 {
		t.Errorf("light: got %d, want 210", rec.LightMinutes)
	}
	if rec.AwakeMinutes != 35 {
		t.Errorf("awake: got %d, want 35", rec.AwakeMinutes)
	}
}

func TestParseGadgetbridge_ActivitySamples(t *testing.T) {
	// 40 samples at 1-min intervals: 20 deep + 20 light
	db := createTestDBConn(t, func(db *sql.DB) {
		db.Exec(`CREATE TABLE MI_BAND_ACTIVITY_SAMPLE (
			TIMESTAMP INTEGER,
			RAW_INTENSITY INTEGER,
			RAW_KIND INTEGER
		)`)
		baseTS := int64(1710108000)
		for i := 0; i < 20; i++ {
			db.Exec(`INSERT INTO MI_BAND_ACTIVITY_SAMPLE VALUES (?, 10, 4)`, baseTS+int64(i*60))
		}
		for i := 0; i < 20; i++ {
			db.Exec(`INSERT INTO MI_BAND_ACTIVITY_SAMPLE VALUES (?, 20, 5)`, baseTS+int64((20+i)*60))
		}
	})

	records, err := parseGBActivitySamples(db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	rec := records[0]
	if rec.DeepMinutes != 20 {
		t.Errorf("deep: got %d, want 20", rec.DeepMinutes)
	}
	if rec.LightMinutes != 20 {
		t.Errorf("light: got %d, want 20", rec.LightMinutes)
	}
	if rec.Source != SourceGadgetbridge {
		t.Errorf("expected source %s, got %s", SourceGadgetbridge, rec.Source)
	}
}

func TestParseGadgetbridge_GapSplitting(t *testing.T) {
	// Period 1: 35 deep samples (35 min, kept)
	// Gap: 45 min (> 30 min threshold)
	// Period 2: 35 light samples (35 min, kept)
	db := createTestDBConn(t, func(db *sql.DB) {
		db.Exec(`CREATE TABLE MI_BAND_ACTIVITY_SAMPLE (
			TIMESTAMP INTEGER,
			RAW_INTENSITY INTEGER,
			RAW_KIND INTEGER
		)`)
		baseTS := int64(1710108000)
		for i := 0; i < 35; i++ {
			db.Exec(`INSERT INTO MI_BAND_ACTIVITY_SAMPLE VALUES (?, 10, 4)`, baseTS+int64(i*60))
		}
		period2Start := baseTS + int64(35*60) + int64(45*60)
		for i := 0; i < 35; i++ {
			db.Exec(`INSERT INTO MI_BAND_ACTIVITY_SAMPLE VALUES (?, 15, 5)`, period2Start+int64(i*60))
		}
	})

	records, err := parseGBActivitySamples(db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records (split by gap), got %d", len(records))
	}
	// Period 1: 35 deep samples (all counted)
	// Gap: 45 min (> 30 min threshold)
	// Period 2: 35 light samples (all counted)
	if records[0].DeepMinutes != 35 {
		t.Errorf("period 1 deep: got %d, want 35", records[0].DeepMinutes)
	}
	if records[1].LightMinutes != 35 {
		t.Errorf("period 2 light: got %d, want 35", records[1].LightMinutes)
	}
}

func TestParseGadgetbridge_ShortPeriodsFiltered(t *testing.T) {
	// 15 samples (15 min) < 30 min threshold, should be ignored
	db := createTestDBConn(t, func(db *sql.DB) {
		db.Exec(`CREATE TABLE MI_BAND_ACTIVITY_SAMPLE (
			TIMESTAMP INTEGER,
			RAW_INTENSITY INTEGER,
			RAW_KIND INTEGER
		)`)
		baseTS := int64(1710108000)
		for i := 0; i < 15; i++ {
			db.Exec(`INSERT INTO MI_BAND_ACTIVITY_SAMPLE VALUES (?, 10, 4)`, baseTS+int64(i*60))
		}
	})

	records, err := parseGBActivitySamples(db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records (period < 30 min), got %d", len(records))
	}
}

func TestParseGadgetbridge_SleepSessionsPreferred(t *testing.T) {
	// When SLEEP_SESSION table exists with data, ParseGadgetbridge uses it
	// and ignores MI_BAND_ACTIVITY_SAMPLE
	dbPath := createTestDBPath(t, func(db *sql.DB) {
		db.Exec(`CREATE TABLE SLEEP_SESSION (
			TIMESTAMP_START INTEGER,
			TIMESTAMP_END INTEGER,
			DEEP_SLEEP_MINUTES INTEGER,
			REM_SLEEP_MINUTES INTEGER,
			LIGHT_SLEEP_MINUTES INTEGER,
			AWAKE_MINUTES INTEGER
		)`)
		db.Exec(`INSERT INTO SLEEP_SESSION VALUES (1710108000, 1710136800, 90, 60, 200, 30)`)
		db.Exec(`CREATE TABLE MI_BAND_ACTIVITY_SAMPLE (
			TIMESTAMP INTEGER,
			RAW_INTENSITY INTEGER,
			RAW_KIND INTEGER
		)`)
		for i := 0; i < 40; i++ {
			db.Exec(`INSERT INTO MI_BAND_ACTIVITY_SAMPLE VALUES (?, 10, 4)`, 1710108000+int64(i*60))
		}
	})

	records, err := ParseGadgetbridge(dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].DeepMinutes != 90 {
		t.Errorf("deep from SLEEP_SESSION: got %d, want 90", records[0].DeepMinutes)
	}
}

func TestParseGadgetbridge_InvalidPath(t *testing.T) {
	_, err := ParseGadgetbridge("/nonexistent/path.db")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

// ===== Fitbit =====

func TestFetchFitbitSleep(t *testing.T) {
	response := fitbitSleepResponse{
		Sleep: []fitbitSleepLog{
			{
				DateOfSleep: "2024-03-11",
				StartTime:   "2024-03-10T23:15:00.000",
				EndTime:     "2024-03-11T06:45:00.000",
				Duration:    27000000,
				IsMainSleep: true,
				Levels: fitbitSleepLevel{
					Summary: map[string]fitbitStageSummary{
						"deep":  {Minutes: 85},
						"rem":   {Minutes: 95},
						"light": {Minutes: 195},
						"wake":  {Minutes: 25},
					},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/sleep/date/") {
			t.Errorf("unexpected URL: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	orig := fitbitBaseURL
	fitbitBaseURL = srv.URL
	defer func() { fitbitBaseURL = orig }()

	records, err := FetchFitbitSleep(t.Context(), &oauth2.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(time.Hour),
	}, time.Date(2024, 3, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	rec := records[0]
	if rec.Source != SourceFitbit {
		t.Errorf("expected source %s, got %s", SourceFitbit, rec.Source)
	}
	if rec.DeepMinutes != 85 {
		t.Errorf("deep: got %d, want 85", rec.DeepMinutes)
	}
	if rec.REMMinutes != 95 {
		t.Errorf("rem: got %d, want 95", rec.REMMinutes)
	}
	if rec.LightMinutes != 195 {
		t.Errorf("light: got %d, want 195", rec.LightMinutes)
	}
	if rec.AwakeMinutes != 25 {
		t.Errorf("awake: got %d, want 25", rec.AwakeMinutes)
	}
	if rec.DurationMinutes != 450 {
		t.Errorf("duration: got %d, want 450", rec.DurationMinutes)
	}
}

func TestFetchFitbitSleep_NonMainSleep(t *testing.T) {
	response := fitbitSleepResponse{
		Sleep: []fitbitSleepLog{
			{
				DateOfSleep: "2024-03-11",
				StartTime:   "2024-03-11T14:00:00.000",
				EndTime:     "2024-03-11T14:30:00.000",
				Duration:    1800000,
				IsMainSleep: false,
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	orig := fitbitBaseURL
	fitbitBaseURL = srv.URL
	defer func() { fitbitBaseURL = orig }()

	records, err := FetchFitbitSleep(t.Context(), &oauth2.Token{
		AccessToken: "test",
		Expiry:      time.Now().Add(time.Hour),
	}, time.Date(2024, 3, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records (non-main sleep filtered), got %d", len(records))
	}
}

func TestFetchFitbitSleep_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"errors":[{"errorType":"expired_token"}]}`))
	}))
	defer srv.Close()

	orig := fitbitBaseURL
	fitbitBaseURL = srv.URL
	defer func() { fitbitBaseURL = orig }()

	_, err := FetchFitbitSleep(t.Context(), &oauth2.Token{
		AccessToken: "expired",
		Expiry:      time.Now().Add(time.Hour),
	}, time.Date(2024, 3, 11, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Error("expected error for 401 response")
	}
}

// ===== Time Parsers =====

func TestParseHCTime(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"2024-01-15T23:00:00Z", false},
		{"2024-01-15T23:00:00.000Z", false},
		{"2024-01-15T23:00:00", false},
		{"2024-01-15T23:00:00+01:00", false},
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
