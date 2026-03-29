package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

// FitbitConfig holds OAuth2 configuration for Fitbit.
var FitbitOAuthConfig = &oauth2.Config{
	Scopes: []string{"sleep"},
	Endpoint: oauth2.Endpoint{
		AuthURL:  "https://www.fitbit.com/oauth2/authorize",
		TokenURL: "https://api.fitbit.com/oauth2/token",
	},
}

// fitbitSleepResponse maps the Fitbit sleep API response.
type fitbitSleepResponse struct {
	Sleep []fitbitSleepLog `json:"sleep"`
}

type fitbitSleepLog struct {
	DateOfSleep string           `json:"dateOfSleep"`
	StartTime   string           `json:"startTime"`
	EndTime     string           `json:"endTime"`
	Duration    int64            `json:"duration"` // milliseconds
	Levels      fitbitSleepLevel `json:"levels"`
	IsMainSleep bool             `json:"isMainSleep"`
}

type fitbitSleepLevel struct {
	Summary map[string]fitbitStageSummary `json:"summary"`
}

type fitbitStageSummary struct {
	Minutes int `json:"minutes"`
}

// FetchFitbitSleep retrieves sleep data for a given date from the Fitbit API.
func FetchFitbitSleep(ctx context.Context, token *oauth2.Token, date time.Time) ([]SleepRecord, error) {
	client := FitbitOAuthConfig.Client(ctx, token)

	dateStr := date.Format("2006-01-02")
	url := fmt.Sprintf("https://api.fitbit.com/1.2/user/-/sleep/date/%s.json", dateStr)

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fitbit API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fitbit API returned %d: %s", resp.StatusCode, body)
	}

	var sleepResp fitbitSleepResponse
	if err := json.NewDecoder(resp.Body).Decode(&sleepResp); err != nil {
		return nil, fmt.Errorf("decode fitbit response: %w", err)
	}

	var records []SleepRecord
	for _, sl := range sleepResp.Sleep {
		if !sl.IsMainSleep {
			continue
		}

		start, err := time.Parse("2006-01-02T15:04:05.000", sl.StartTime)
		if err != nil {
			continue
		}
		end, err := time.Parse("2006-01-02T15:04:05.000", sl.EndTime)
		if err != nil {
			continue
		}
		sleepDate, _ := time.Parse("2006-01-02", sl.DateOfSleep)

		rec := SleepRecord{
			Date:            sleepDate,
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceFitbit,
			DurationMinutes: int(sl.Duration / 60000),
		}

		// Extract stage data if available.
		if summary := sl.Levels.Summary; summary != nil {
			if v, ok := summary["deep"]; ok {
				rec.DeepMinutes = v.Minutes
			}
			if v, ok := summary["rem"]; ok {
				rec.REMMinutes = v.Minutes
			}
			if v, ok := summary["light"]; ok {
				rec.LightMinutes = v.Minutes
			}
			if v, ok := summary["wake"]; ok {
				rec.AwakeMinutes = v.Minutes
			}
		}

		records = append(records, rec)
	}

	return records, nil
}

// RefreshFitbitToken refreshes an expired Fitbit OAuth2 token.
func RefreshFitbitToken(ctx context.Context, token *oauth2.Token) (*oauth2.Token, error) {
	src := FitbitOAuthConfig.TokenSource(ctx, token)
	newToken, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh fitbit token: %w", err)
	}
	return newToken, nil
}
