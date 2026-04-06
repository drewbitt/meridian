package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var (
	errFitbitAPI = errors.New("fitbit API error")
	// ErrTokenRevoked indicates the Fitbit refresh token has been permanently invalidated.
	// Code should use errors.Is(err, ErrTokenRevoked) to check for this condition.
	ErrTokenRevoked = errors.New("fitbit token permanently invalid, re-authorization required")

	// ErrRateLimited indicates the Fitbit API rate limit was exceeded.
	ErrRateLimited = errors.New("fitbit API rate limited")
	// ErrSleepPending indicates sleep data is still being classified by Fitbit.
	ErrSleepPending = errors.New("fitbit sleep data still being classified")
)

// fitbitInvalidGrantError wraps an oauth2.RetrieveError and implements
// Is(ErrTokenRevoked) so errors.Is catches it, plus As(*oauth2.RetrieveError)
// so the underlying error is accessible — all from a single chain.
type fitbitInvalidGrantError struct {
	orig error // *oauth2.RetrieveError
}

func (e *fitbitInvalidGrantError) Error() string { return e.orig.Error() }
func (e *fitbitInvalidGrantError) Unwrap() error { return e.orig }

func (e *fitbitInvalidGrantError) Is(target error) bool {
	return target == ErrTokenRevoked
}

func (e *fitbitInvalidGrantError) As(target any) bool {
	re, ok := target.(*oauth2.RetrieveError)
	if !ok {
		return false
	}
	if origRE, ok := e.orig.(*oauth2.RetrieveError); ok {
		*re = *origRE
		return true
	}
	return false
}

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
	Meta  *fitbitSleepMeta `json:"meta,omitempty"`
}

type fitbitSleepMeta struct {
	State         string `json:"state"`         // "pending" when data is being classified
	RetryDuration int    `json:"retryDuration"` // milliseconds to wait before retrying
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

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("fitbit API returned 429: %w", ErrRateLimited)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fitbit API returned %d: %s: %w", resp.StatusCode, body, errFitbitAPI)
	}

	var sleepResp fitbitSleepResponse
	if err := json.NewDecoder(resp.Body).Decode(&sleepResp); err != nil {
		return nil, fmt.Errorf("decode fitbit response: %w", err)
	}

	// Fitbit returns meta.state="pending" while sleep data is still being
	// classified (e.g. right after wake-up). If there are no sleep records
	// and data is pending, signal the caller to retry.
	if len(sleepResp.Sleep) == 0 && sleepResp.Meta != nil && sleepResp.Meta.State == "pending" {
		return nil, fmt.Errorf("sleep data pending (retry in %dms): %w", sleepResp.Meta.RetryDuration, ErrSleepPending)
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
// Returns ErrTokenRevoked if the refresh token is permanently invalid
// (e.g. revoked, expired, or user deauthorized the app).
func RefreshFitbitToken(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (*oauth2.Token, error) {
	src := cfg.TokenSource(ctx, token)
	newToken, err := src.Token()
	if err != nil {
		if isFitbitInvalidGrant(err) {
			return nil, fmt.Errorf("refresh fitbit token: %w", &fitbitInvalidGrantError{orig: err})
		}
		return nil, fmt.Errorf("refresh fitbit token: %w", err)
	}
	return newToken, nil
}

// isFitbitInvalidGrant checks whether the OAuth2 error is a Fitbit invalid_grant,
// which indicates the refresh token is permanently unusable (revoked, expired,
// or the user deauthorized the application). The user must re-authenticate.
//
// Fitbit returns errors as {"errors":[{"errorType":"invalid_grant"}]} rather than
// the RFC 6749 standard {"error":"invalid_grant"}, so re.ErrorCode may be empty.
// We check the structured ErrorCode first, then fall back to body inspection.
func isFitbitInvalidGrant(err error) bool {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.ErrorCode == "invalid_grant" {
			return true
		}
		return strings.Contains(string(re.Body), "invalid_grant")
	}
	return strings.Contains(err.Error(), "invalid_grant")
}
