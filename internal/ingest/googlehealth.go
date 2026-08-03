package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	googleHealthSleepPageSize = 25
	googleHealthSleepPath     = "/users/me/dataTypes/sleep/dataPoints:reconcile"
	googleOAuthAuthURL        = "https://accounts.google.com/o/oauth2/v2/auth"
	googleOAuthTokenURL       = "https://oauth2.googleapis.com/token" //nolint:gosec // Public OAuth endpoint, not a credential.

	// GoogleHealthSleepReadonlyScope is the only user-data permission Meridian
	// requests. Keep the consent-screen configuration and callback validation
	// aligned with this value.
	GoogleHealthSleepReadonlyScope = "https://www.googleapis.com/auth/googlehealth.sleep.readonly"
)

var (
	googleHealthBaseURL = "https://health.googleapis.com/v4"

	// ErrReauthorizationRequired indicates that Google rejected the stored
	// credentials and the user must connect Google Health again.
	ErrReauthorizationRequired = errors.New("google health reauthorization required")

	// ErrRateLimited indicates that the Google Health API rate limit was exceeded.
	ErrRateLimited = errors.New("google health API rate limited")

	// ErrSleepPending indicates Google Health detected sleep but has not
	// finished processing it yet.
	ErrSleepPending = errors.New("google health sleep data still being processed")

	errInvalidGoogleHealthSleepRange = errors.New("google health sleep range ends before it starts")
	errGoogleHealthAPIStatus         = errors.New("google health API request failed")
	errRepeatedGoogleHealthPageToken = errors.New("google health repeated sleep page token")
)

// NewGoogleHealthOAuthConfig creates a Google OAuth2 configuration with the
// single read-only scope Meridian needs.
func NewGoogleHealthOAuthConfig(clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			GoogleHealthSleepReadonlyScope,
		},
		Endpoint: oauth2.Endpoint{
			AuthURL:  googleOAuthAuthURL,
			TokenURL: googleOAuthTokenURL,
		},
	}
}

type googleHealthSleepResponse struct {
	// The API used reconciledDataPoints during preview and now documents
	// dataPoints. Accept both while the v4 surface settles.
	DataPoints           []googleHealthDataPoint `json:"dataPoints"`
	ReconciledDataPoints []googleHealthDataPoint `json:"reconciledDataPoints"`
	NextPageToken        string                  `json:"nextPageToken"`
}

type googleHealthDataPoint struct {
	Name          string            `json:"name"`
	DataPointName string            `json:"dataPointName"`
	Sleep         googleHealthSleep `json:"sleep"`
}

type googleHealthSleep struct {
	Interval googleHealthSleepInterval `json:"interval"`
	Metadata googleHealthSleepMetadata `json:"metadata"`
	Summary  googleHealthSleepSummary  `json:"summary"`
}

type googleHealthSleepInterval struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

type googleHealthSleepMetadata struct {
	Nap          *bool  `json:"nap"`
	Main         *bool  `json:"main"`
	Processed    *bool  `json:"processed"`
	StagesStatus string `json:"stagesStatus"`
}

type googleHealthSleepSummary struct {
	MinutesAsleep string                     `json:"minutesAsleep"`
	MinutesAwake  string                     `json:"minutesAwake"`
	StagesSummary []googleHealthStageSummary `json:"stagesSummary"`
}

type googleHealthStageSummary struct {
	Type    string `json:"type"`
	Minutes string `json:"minutes"`
}

// FetchGoogleHealthSleep retrieves the reconciled sleep stream over an
// inclusive range of local calendar dates. Google caps sleep pages at 25, so
// the function follows every nextPageToken before returning.
func FetchGoogleHealthSleep(
	ctx context.Context,
	client *http.Client,
	start, end time.Time,
	loc *time.Location,
) ([]SleepRecord, bool, error) {
	if loc == nil {
		loc = time.UTC
	}
	if end.Before(start) {
		return nil, false, errInvalidGoogleHealthSleepRange
	}

	startDate := DateOnly(start.In(loc))
	endExclusive := DateOnly(end.In(loc)).AddDate(0, 0, 1)
	filter := fmt.Sprintf(
		`sleep.interval.civil_end_time >= %q AND sleep.interval.civil_end_time < %q`,
		startDate.Format(time.DateOnly),
		endExclusive.Format(time.DateOnly),
	)

	var records []SleepRecord
	pending := false
	pageToken := ""
	seenTokens := make(map[string]struct{})
	for {
		query := url.Values{
			"dataSourceFamily": {"users/me/dataSourceFamilies/all-sources"},
			"filter":           {filter},
			"pageSize":         {strconv.Itoa(googleHealthSleepPageSize)},
		}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}

		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			googleHealthBaseURL+googleHealthSleepPath+"?"+query.Encode(),
			http.NoBody,
		)
		if err != nil {
			return nil, false, fmt.Errorf("create google health sleep request: %w", err)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, false, fmt.Errorf("google health sleep request: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, false, fmt.Errorf("read google health sleep response: %w", readErr)
		}
		if closeErr != nil {
			return nil, false, fmt.Errorf("close google health sleep response: %w", closeErr)
		}
		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusUnauthorized:
			return nil, false, fmt.Errorf(
				"google health API returned %d: %s: %w",
				resp.StatusCode,
				body,
				ErrReauthorizationRequired,
			)
		case http.StatusForbidden:
			if bytesContainFold(body, "ACCESS_TOKEN_SCOPE_INSUFFICIENT") ||
				bytesContainFold(body, "insufficient authentication scopes") {
				return nil, false, fmt.Errorf(
					"google health API returned %d: %s: %w",
					resp.StatusCode,
					body,
					ErrReauthorizationRequired,
				)
			}
			return nil, false, fmt.Errorf(
				"google health API returned %d: %s: %w",
				resp.StatusCode,
				body,
				errGoogleHealthAPIStatus,
			)
		case http.StatusTooManyRequests:
			return nil, false, fmt.Errorf(
				"google health API returned %d: %w",
				resp.StatusCode,
				ErrRateLimited,
			)
		default:
			return nil, false, fmt.Errorf(
				"google health API returned %d: %s: %w",
				resp.StatusCode,
				body,
				errGoogleHealthAPIStatus,
			)
		}

		var page googleHealthSleepResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, false, fmt.Errorf("decode google health sleep response: %w", err)
		}
		points := page.DataPoints
		if len(points) == 0 {
			points = page.ReconciledDataPoints
		}
		pageRecords, pagePending := parseGoogleHealthSleepPoints(points, loc)
		records = append(records, pageRecords...)
		pending = pending || pagePending

		if page.NextPageToken == "" {
			return records, pending, nil
		}
		if _, duplicate := seenTokens[page.NextPageToken]; duplicate {
			return nil, false, errRepeatedGoogleHealthPageToken
		}
		seenTokens[page.NextPageToken] = struct{}{}
		pageToken = page.NextPageToken
	}
}

func bytesContainFold(value []byte, substring string) bool {
	return strings.Contains(strings.ToLower(string(value)), strings.ToLower(substring))
}

func parseGoogleHealthSleepPoints(
	points []googleHealthDataPoint,
	loc *time.Location,
) ([]SleepRecord, bool) {
	if loc == nil {
		loc = time.UTC
	}

	records := make([]SleepRecord, 0, len(points))
	pending := false
	for _, point := range points {
		if point.Sleep.Metadata.Processed != nil && !*point.Sleep.Metadata.Processed {
			pending = true
			continue
		}
		start, err := time.Parse(time.RFC3339Nano, point.Sleep.Interval.StartTime)
		if err != nil {
			continue
		}
		end, err := time.Parse(time.RFC3339Nano, point.Sleep.Interval.EndTime)
		if err != nil || !validSleepInterval(start, end) {
			continue
		}

		deep, rem, light, awake := 0, 0, 0, parseGoogleHealthMinutes(point.Sleep.Summary.MinutesAwake)
		for _, stage := range point.Sleep.Summary.StagesSummary {
			minutes := parseGoogleHealthMinutes(stage.Minutes)
			switch strings.ToUpper(stage.Type) {
			case "DEEP":
				deep = minutes
			case "REM":
				rem = minutes
			case "LIGHT":
				light = minutes
			case "AWAKE":
				awake = minutes
			}
		}

		spanMinutes := int(end.Sub(start).Minutes())
		durationMinutes := parseGoogleHealthMinutes(point.Sleep.Summary.MinutesAsleep)
		if durationMinutes <= 0 {
			durationMinutes = deep + rem + light
		}
		if durationMinutes <= 0 && awake < spanMinutes {
			durationMinutes = spanMinutes - awake
		}
		if durationMinutes <= 0 || durationMinutes > spanMinutes {
			durationMinutes = spanMinutes
		}

		isNap, napExplicit := googleHealthNap(point.Sleep.Metadata, start.In(loc), end.In(loc))
		records = append(records, SleepRecord{
			Date:            SleepNightDate(start.In(loc)),
			SleepStart:      start,
			SleepEnd:        end,
			Source:          SourceGoogleHealth,
			DurationMinutes: durationMinutes,
			DeepMinutes:     deep,
			REMMinutes:      rem,
			LightMinutes:    light,
			AwakeMinutes:    awake,
			IsNap:           isNap,
			NapExplicit:     napExplicit,
		})
	}
	return records, pending
}

func googleHealthNap(
	metadata googleHealthSleepMetadata,
	start, end time.Time,
) (isNap, explicit bool) {
	if metadata.Nap != nil {
		return *metadata.Nap, true
	}
	// The current protobuf exposes nap, but an official preview example used
	// main. Supporting it here costs nothing and protects existing preview data.
	if metadata.Main != nil {
		return !*metadata.Main, true
	}
	if metadata.StagesStatus == "REJECTED_NAP" {
		return true, true
	}
	return LikelyNap(start, end), false
}

func parseGoogleHealthMinutes(value string) int {
	minutes, err := strconv.Atoi(value)
	if err != nil || minutes < 0 {
		return 0
	}
	return minutes
}
