package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// healthConnectExport represents the top-level structure of a Health Connect export.
type healthConnectExport struct {
	SleepSessions []healthConnectSleepSession `json:"sleepSessions"`
}

type healthConnectSleepSession struct {
	StartTime string                    `json:"startTime"`
	EndTime   string                    `json:"endTime"`
	Stages    []healthConnectSleepStage `json:"stages"`
}

type healthConnectSleepStage struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	Stage     int    `json:"stage"`
}

// Health Connect sleep stage constants.
const (
	hcStageAwake    = 1
	hcStageSleeping = 2
	hcStageOutOfBed = 3
	hcStageLight    = 4
	hcStageDeep     = 5
	hcStageREM      = 6
)

// ParseHealthConnect parses an Android Health Connect JSON export.
func ParseHealthConnect(r io.Reader) ([]SleepRecord, error) {
	var export healthConnectExport
	if err := json.NewDecoder(r).Decode(&export); err != nil {
		return nil, fmt.Errorf("%w: decode health connect JSON: %w", ErrParseFailed, err)
	}

	var records []SleepRecord
	for _, session := range export.SleepSessions {
		start, err := parseHCTime(session.StartTime)
		if err != nil {
			continue
		}
		end, err := parseHCTime(session.EndTime)
		if err != nil {
			continue
		}

		rec := SleepRecord{
			Date:            dateOnly(start),
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceHealthConnect,
			DurationMinutes: int(end.Sub(start).Minutes()),
		}

		// Aggregate stage durations.
		for _, stage := range session.Stages {
			stageStart, err := parseHCTime(stage.StartTime)
			if err != nil {
				continue
			}
			stageEnd, err := parseHCTime(stage.EndTime)
			if err != nil {
				continue
			}
			mins := int(stageEnd.Sub(stageStart).Minutes())

			switch stage.Stage {
			case hcStageDeep:
				rec.DeepMinutes += mins
			case hcStageREM:
				rec.REMMinutes += mins
			case hcStageLight, hcStageSleeping:
				rec.LightMinutes += mins
			case hcStageAwake, hcStageOutOfBed:
				rec.AwakeMinutes += mins
			}
		}

		records = append(records, rec)
	}

	return records, nil
}

func parseHCTime(s string) (time.Time, error) {
	// Health Connect exports use ISO 8601.
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time: %s", s)
}
