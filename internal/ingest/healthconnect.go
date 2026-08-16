package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

var errParseTime = errors.New("cannot parse time")

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
	hcStageAwake      = 1
	hcStageSleeping   = 2
	hcStageOutOfBed   = 3
	hcStageLight      = 4
	hcStageDeep       = 5
	hcStageREM        = 6
	hcStageAwakeInBed = 7
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
		if !validSleepInterval(start, end) {
			continue
		}

		rec := SleepRecord{
			Date:            SleepNightDate(start),
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceHealthConnect,
			DurationMinutes: int(end.Sub(start).Minutes()),
			IsNap:           LikelyNap(start, end),
		}

		// Aggregate stage durations. Health Connect requires stages to be
		// positive, sequential, non-overlapping, and contained by the parent
		// session. Treat malformed third-party exports defensively so a bad
		// interval cannot create negative or inflated sleep-stage totals.
		lastStageEnd := start
		for _, stage := range session.Stages {
			stageStart, err := parseHCTime(stage.StartTime)
			if err != nil {
				continue
			}
			stageEnd, err := parseHCTime(stage.EndTime)
			if err != nil {
				continue
			}
			if stageStart.Before(start) || stageEnd.After(end) ||
				stageStart.Before(lastStageEnd) || !stageEnd.After(stageStart) {
				continue
			}
			mins := int(stageEnd.Sub(stageStart).Minutes())
			if mins <= 0 {
				continue
			}
			lastStageEnd = stageEnd

			switch stage.Stage {
			case hcStageDeep:
				rec.DeepMinutes += mins
			case hcStageREM:
				rec.REMMinutes += mins
			case hcStageLight, hcStageSleeping:
				rec.LightMinutes += mins
			case hcStageAwake, hcStageOutOfBed, hcStageAwakeInBed:
				rec.AwakeMinutes += mins
			}
		}
		// A session spans time in bed and can include awake/out-of-bed stages.
		// When classified sleep stages are available, use their total for
		// sleep debt rather than charging the entire session as asleep.
		if asleep := rec.DeepMinutes + rec.REMMinutes + rec.LightMinutes; asleep > 0 {
			rec.DurationMinutes = asleep
		}

		records = append(records, rec)
	}

	return records, nil
}

func parseHCTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05",
	}
	t, err := parseTimeLayouts(s, formats)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s", errParseTime, s)
	}
	return t, nil
}
