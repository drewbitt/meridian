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
	gbActivityDeepSleep  = 4
	gbActivityLightSleep = 5
	gbHuamiSleep         = 120
	gbHuamiDeepSleep     = 121
	gbHuamiREMSleep      = 122
)

// ParseGadgetbridge reads a Gadgetbridge SQLite export and extracts sleep records.
func ParseGadgetbridge(dbPath string) ([]SleepRecord, error) {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("%w: open gadgetbridge db: %w", ErrInvalidFile, err)
	}
	defer db.Close()

	// Prefer current, device-specific summary tables when available.
	records, err := parseGBXiaomiSleepTimes(db)
	if err == nil && len(records) > 0 {
		return records, nil
	}

	// Retain compatibility with third-party/older exports that expose a
	// generic summary table.
	records, err = parseGBSleepSessions(db)
	if err == nil && len(records) > 0 {
		return records, nil
	}

	// Fall back to activity samples.
	return parseGBActivitySamples(db)
}

func parseGBXiaomiSleepTimes(db *sql.DB) ([]SleepRecord, error) {
	rows, err := db.Query(`
		SELECT
			TIMESTAMP,
			WAKEUP_TIME,
			COALESCE(TOTAL_DURATION, 0),
			COALESCE(DEEP_SLEEP_DURATION, 0),
			COALESCE(REM_SLEEP_DURATION, 0),
			COALESCE(LIGHT_SLEEP_DURATION, 0),
			COALESCE(AWAKE_DURATION, 0)
		FROM XIAOMI_SLEEP_TIME_SAMPLE
		ORDER BY TIMESTAMP
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []SleepRecord
	for rows.Next() {
		var startMS, endMS int64
		var total, deep, rem, light, awake int
		if err := rows.Scan(&startMS, &endMS, &total, &deep, &rem, &light, &awake); err != nil {
			continue
		}
		start := time.UnixMilli(startMS)
		end := time.UnixMilli(endMS)
		if !validSleepInterval(start, end) {
			continue
		}
		if total <= 0 {
			total = int(end.Sub(start).Minutes())
		}
		records = append(records, SleepRecord{
			Date:            SleepNightDate(start),
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceGadgetbridge,
			DurationMinutes: total,
			DeepMinutes:     max(deep, 0),
			REMMinutes:      max(rem, 0),
			LightMinutes:    max(light, 0),
			AwakeMinutes:    max(awake, 0),
			IsNap:           LikelyNap(start, end),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Xiaomi sleep sessions: %w", err)
	}
	return records, nil
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
		if !validSleepInterval(start, end) {
			continue
		}
		records = append(records, SleepRecord{
			Date:            SleepNightDate(start),
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceGadgetbridge,
			DurationMinutes: int(end.Sub(start).Minutes()),
			DeepMinutes:     deep,
			REMMinutes:      rem,
			LightMinutes:    light,
			AwakeMinutes:    awake,
			IsNap:           LikelyNap(start, end),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sleep sessions: %w", err)
	}

	return records, nil
}

func parseGBActivitySamples(db *sql.DB) ([]SleepRecord, error) {
	// Legacy Mi Band tables use raw kinds 4 (deep) and 5 (light). Do not
	// infer sleep from low intensity: that also includes sedentary daytime
	// samples and can create all-day false sleep periods.
	rows, err := db.Query(`
		SELECT TIMESTAMP, RAW_INTENSITY, RAW_KIND
		FROM MI_BAND_ACTIVITY_SAMPLE
		WHERE RAW_KIND IN (?, ?)
		ORDER BY TIMESTAMP
	`, gbActivityDeepSleep, gbActivityLightSleep)
	if err != nil {
		// Huami Extended stores sleep as kind 120. Gadgetbridge derives deep
		// and REM in memory from the corresponding confidence columns, so
		// reproduce that normalization while reading the exported database.
		rows, err = db.Query(`
			SELECT
				TIMESTAMP,
				RAW_INTENSITY,
				CASE
					WHEN (REM_SLEEP & 127) > 55 THEN ?
					WHEN (DEEP_SLEEP & 127) > 42 THEN ?
					ELSE RAW_KIND
				END
			FROM HUAMI_EXTENDED_ACTIVITY_SAMPLE
			WHERE RAW_KIND = ?
			ORDER BY TIMESTAMP
		`, gbHuamiREMSleep, gbHuamiDeepSleep, gbHuamiSleep)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activity samples: %w", err)
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
	case gbHuamiDeepSleep:
		deepMins++
	case gbHuamiREMSleep:
		remMins++
	case gbActivityLightSleep, gbHuamiSleep:
		lightMins++
	}

	flush := func() {
		if periodEnd-periodStart < 30*60 { // Ignore periods < 30 min
			return
		}
		start := time.Unix(periodStart, 0)
		end := time.Unix(periodEnd, 0)
		records = append(records, SleepRecord{
			Date:            SleepNightDate(start),
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceGadgetbridge,
			DurationMinutes: int(end.Sub(start).Minutes()),
			DeepMinutes:     deepMins,
			REMMinutes:      remMins,
			LightMinutes:    lightMins,
			IsNap:           LikelyNap(start, end),
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
		case gbHuamiDeepSleep:
			deepMins++
		case gbHuamiREMSleep:
			remMins++
		case gbActivityLightSleep, gbHuamiSleep:
			lightMins++
		}
	}
	flush()

	return records, nil
}
