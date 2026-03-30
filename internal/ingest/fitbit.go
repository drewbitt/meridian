package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

var errFitbitAPI = errors.New("fitbit API error")

// NewFitbitOAuthConfig creates a per-user Fitbit OAuth2 config.
func NewFitbitOAuthConfig(clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{"sleep", "profile"},
		Endpoint: oauth2.Endpoint{ //nolint:gosec // OAuth URLs, not credentials
			AuthURL:  "https://www.fitbit.com/oauth2/authorize",
			TokenURL: "https://api.fitbit.com/oauth2/token",
		},
		RedirectURL: redirectURL,
	}
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

var fitbitBaseURL = "https://api.fitbit.com/1.2"

// fitbitProfileResponse maps the subset of the Fitbit profile we need.
type fitbitProfileResponse struct {
	User struct {
		Timezone string `json:"timezone"` // IANA, e.g. "America/New_York"
	} `json:"user"`
}

// FetchFitbitTimezone returns the IANA timezone from the user's Fitbit profile.
func FetchFitbitTimezone(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (*time.Location, error) {
	client := cfg.Client(ctx, token)

	resp, err := client.Get("https://api.fitbit.com/1/user/-/profile.json")
	if err != nil {
		return nil, fmt.Errorf("fitbit profile request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fitbit profile returned %d: %s: %w", resp.StatusCode, body, errFitbitAPI)
	}

	var profile fitbitProfileResponse
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("decode fitbit profile: %w", err)
	}

	loc, err := time.LoadLocation(profile.User.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid fitbit timezone %q: %w", profile.User.Timezone, err)
	}
	return loc, nil
}

// FetchFitbitSleep retrieves sleep data for a given date from the Fitbit API.
// loc is the user's Fitbit profile timezone — Fitbit returns times without
// offsets, so we need it to interpret them correctly. Use FetchFitbitTimezone
// to obtain this.
func FetchFitbitSleep(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token, date time.Time, loc *time.Location) ([]SleepRecord, error) {
	client := cfg.Client(ctx, token)
	dateStr := date.Format("2006-01-02")
	url := fmt.Sprintf("%s/user/-/sleep/date/%s.json", fitbitBaseURL, dateStr)
	return fetchFitbitSleepURL(client, url, loc)
}

// FetchFitbitSleepRange retrieves sleep data for a date range (max 100 days per
// Fitbit API limits) using the range endpoint — a single API call.
func FetchFitbitSleepRange(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token, start, end time.Time, loc *time.Location) ([]SleepRecord, error) {
	client := cfg.Client(ctx, token)
	url := fmt.Sprintf("%s/user/-/sleep/date/%s/%s.json", fitbitBaseURL, start.Format("2006-01-02"), end.Format("2006-01-02"))
	return fetchFitbitSleepURL(client, url, loc)
}

func fetchFitbitSleepURL(client *http.Client, url string, loc *time.Location) ([]SleepRecord, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fitbit API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fitbit API returned %d: %s: %w", resp.StatusCode, body, errFitbitAPI)
	}

	var sleepResp fitbitSleepResponse
	if err := json.NewDecoder(resp.Body).Decode(&sleepResp); err != nil {
		return nil, fmt.Errorf("decode fitbit response: %w", err)
	}

	return parseFitbitSleepLogs(sleepResp.Sleep, loc), nil
}

func parseFitbitSleepLogs(logs []fitbitSleepLog, loc *time.Location) []SleepRecord {
	var records []SleepRecord
	for _, sl := range logs {
		if !sl.IsMainSleep {
			continue
		}

		start, err := time.ParseInLocation("2006-01-02T15:04:05.000", sl.StartTime, loc)
		if err != nil {
			continue
		}
		end, err := time.ParseInLocation("2006-01-02T15:04:05.000", sl.EndTime, loc)
		if err != nil {
			continue
		}
		sleepDate, err := time.ParseInLocation("2006-01-02", sl.DateOfSleep, loc)
		if err != nil {
			continue
		}

		rec := SleepRecord{
			Date:            sleepDate,
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceFitbit,
			DurationMinutes: int(sl.Duration / 60000),
		}

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
	return records
}

// RefreshFitbitToken refreshes an expired Fitbit OAuth2 token.
func RefreshFitbitToken(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (*oauth2.Token, error) {
	src := cfg.TokenSource(ctx, token)
	newToken, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh fitbit token: %w", err)
	}
	return newToken, nil
}
