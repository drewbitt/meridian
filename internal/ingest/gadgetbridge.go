package ingest

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // SQLite driver for Gadgetbridge databases
)

var errNoSleepTable = errors.New("no SLEEP_SESSION table")

// Gadgetbridge activity type constants.
const (
	gbActivitySleep      = 2
	gbActivityDeepSleep  = 4
	gbActivityLightSleep = 5
	gbActivityREMSleep   = 6
	// Activity intensity threshold for detecting sleep from raw data.
	gbSleepIntensityMax = 50
)

// ParseGadgetbridge reads a Gadgetbridge SQLite export and extracts sleep records.
func ParseGadgetbridge(dbPath string) ([]SleepRecord, error) {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("%w: open gadgetbridge db: %w", ErrInvalidFile, err)
	}
	defer db.Close()

	// Try the structured sleep sessions table first (newer Gadgetbridge).
	records, err := parseGBSleepSessions(db)
	if err == nil && len(records) > 0 {
		return records, nil
	}

	// Fall back to activity samples.
	return parseGBActivitySamples(db)
}

func parseGBSleepSessions(db *sql.DB) ([]SleepRecord, error) {
	// Check if the table exists.
	var tableName string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='SLEEP_SESSION'`).Scan(&tableName)
	if err != nil {
		return nil, errNoSleepTable
	}

	rows, err := db.Query(`
		SELECT
			TIMESTAMP_START,
			TIMESTAMP_END,
			COALESCE(DEEP_SLEEP_MINUTES, 0),
			COALESCE(REM_SLEEP_MINUTES, 0),
			COALESCE(LIGHT_SLEEP_MINUTES, 0),
			COALESCE(AWAKE_MINUTES, 0)
		FROM SLEEP_SESSION
		ORDER BY TIMESTAMP_START
	`)
	if err != nil {
		return nil, fmt.Errorf("query sleep sessions: %w", err)
	}
	defer rows.Close()
	var records []SleepRecord
	for rows.Next() {
		var startTS, endTS int64
		var deep, rem, light, awake int
		if err := rows.Scan(&startTS, &endTS, &deep, &rem, &light, &awake); err != nil {
			continue
		}

		start := time.Unix(startTS, 0)
		end := time.Unix(endTS, 0)
		records = append(records, SleepRecord{
			Date:            dateOnly(start),
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceGadgetbridge,
			DurationMinutes: int(end.Sub(start).Minutes()),
			DeepMinutes:     deep,
			REMMinutes:      rem,
			LightMinutes:    light,
			AwakeMinutes:    awake,
		})
	}

	return records, nil
}

func parseGBActivitySamples(db *sql.DB) ([]SleepRecord, error) {
	// Query raw activity samples and detect sleep periods by low intensity.
	rows, err := db.Query(`
		SELECT TIMESTAMP, RAW_INTENSITY, RAW_KIND
		FROM MI_BAND_ACTIVITY_SAMPLE
		WHERE RAW_KIND IN (2, 4, 5, 6) OR RAW_INTENSITY <= ?
		ORDER BY TIMESTAMP
	`, gbSleepIntensityMax)
	if err != nil {
		// Try generic table name.
		rows, err = db.Query(`
			SELECT TIMESTAMP, RAW_INTENSITY, RAW_KIND
			FROM HUAMI_EXTENDED_ACTIVITY_SAMPLE
			WHERE RAW_KIND IN (2, 4, 5, 6) OR RAW_INTENSITY <= ?
			ORDER BY TIMESTAMP
		`, gbSleepIntensityMax)
		if err != nil {
			return nil, fmt.Errorf("query activity samples: %w", err)
		}
	}
	defer rows.Close()

	type sample struct {
		timestamp int64
		intensity int
		kind      int
	}
	var samples []sample
	for rows.Next() {
		var s sample
		if err := rows.Scan(&s.timestamp, &s.intensity, &s.kind); err != nil {
			continue
		}
		samples = append(samples, s)
	}

	if len(samples) == 0 {
		return nil, nil
	}

	// Detect contiguous sleep periods (gaps > 30 min split into separate records).
	var records []SleepRecord
	periodStart := samples[0].timestamp
	periodEnd := samples[0].timestamp
	var deepMins, remMins, lightMins int

	switch samples[0].kind {
	case gbActivityDeepSleep:
		deepMins++
	case gbActivityREMSleep:
		remMins++
	case gbActivityLightSleep, gbActivitySleep:
		lightMins++
	}

	flush := func() {
		if periodEnd-periodStart < 30*60 { // Ignore periods < 30 min
			return
		}
		start := time.Unix(periodStart, 0)
		end := time.Unix(periodEnd, 0)
		records = append(records, SleepRecord{
			Date:            dateOnly(start),
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceGadgetbridge,
			DurationMinutes: int(end.Sub(start).Minutes()),
			DeepMinutes:     deepMins,
			REMMinutes:      remMins,
			LightMinutes:    lightMins,
		})
		deepMins, remMins, lightMins = 0, 0, 0
	}

	for i := 1; i < len(samples); i++ {
		s := samples[i]
		gap := s.timestamp - periodEnd
		if gap > 30*60 { // 30-minute gap
			flush()
			periodStart = s.timestamp
		}
		periodEnd = s.timestamp

		// Accumulate stage minutes (each sample is ~1 min).
		switch s.kind {
		case gbActivityDeepSleep:
			deepMins++
		case gbActivityREMSleep:
			remMins++
		case gbActivityLightSleep, gbActivitySleep:
			lightMins++
		}
	}
	flush()

	return records, nil
}
