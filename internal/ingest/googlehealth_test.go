package ingest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestNewGoogleHealthOAuthConfig(t *testing.T) {
	t.Parallel()

	cfg := NewGoogleHealthOAuthConfig("client", "secret", "http://localhost/callback")
	if cfg.Endpoint.AuthURL != googleOAuthAuthURL {
		t.Errorf("auth URL = %q, want %q", cfg.Endpoint.AuthURL, googleOAuthAuthURL)
	}
	if cfg.Endpoint.TokenURL != googleOAuthTokenURL {
		t.Errorf("token URL = %q, want %q", cfg.Endpoint.TokenURL, googleOAuthTokenURL)
	}
	if cfg.RedirectURL != "http://localhost/callback" {
		t.Errorf("redirect URL = %q", cfg.RedirectURL)
	}
	if len(cfg.Scopes) != 1 ||
		cfg.Scopes[0] != GoogleHealthSleepReadonlyScope {
		t.Fatalf("scopes = %q, want only sleep.readonly", cfg.Scopes)
	}
}

func TestFetchGoogleHealthSleep_ReconcilesAndPaginates(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", req.Method)
		}
		if req.URL.Path != "/v4/users/me/dataTypes/sleep/dataPoints:reconcile" {
			t.Errorf("path = %q", req.URL.Path)
		}
		query := req.URL.Query()
		if query.Get("dataSourceFamily") != "users/me/dataSourceFamilies/all-sources" {
			t.Errorf("dataSourceFamily = %q", query.Get("dataSourceFamily"))
		}
		if query.Get("pageSize") != "25" {
			t.Errorf("pageSize = %q", query.Get("pageSize"))
		}
		wantFilter := `sleep.interval.civil_end_time >= "2026-07-23" AND sleep.interval.civil_end_time < "2026-07-26"`
		if query.Get("filter") != wantFilter {
			t.Errorf("filter = %q, want %q", query.Get("filter"), wantFilter)
		}

		switch calls {
		case 1:
			if query.Get("pageToken") != "" {
				t.Errorf("first page token = %q", query.Get("pageToken"))
			}
			return jsonResponse(http.StatusOK, `{
				"dataPoints": [{
					"dataPointName": "users/me/dataTypes/sleep/dataPoints/night",
					"sleep": {
						"interval": {
							"startTime": "2026-07-24T03:00:00Z",
							"endTime": "2026-07-24T11:00:00Z"
						},
						"metadata": {"nap": false, "stagesStatus": "SUCCEEDED"},
						"summary": {
							"minutesAsleep": "430",
							"minutesAwake": "50",
							"stagesSummary": [
								{"type": "DEEP", "minutes": "100"},
								{"type": "REM", "minutes": "90"},
								{"type": "LIGHT", "minutes": "240"},
								{"type": "AWAKE", "minutes": "50"}
							]
						}
					}
				}],
				"nextPageToken": "page-two"
			}`), nil
		case 2:
			if query.Get("pageToken") != "page-two" {
				t.Errorf("second page token = %q", query.Get("pageToken"))
			}
			// Accept the preview response key and metadata.main while early v4
			// clients and captured fixtures still contain them.
			return jsonResponse(http.StatusOK, `{
				"reconciledDataPoints": [{
					"sleep": {
						"interval": {
							"startTime": "2026-07-24T18:00:00Z",
							"endTime": "2026-07-24T19:00:00Z"
						},
						"metadata": {"main": false},
						"summary": {"minutesAsleep": "50", "minutesAwake": "10"}
					}
				}]
			}`), nil
		default:
			t.Fatalf("unexpected request %d", calls)
			return nil, nil
		}
	})}

	records, pending, err := FetchGoogleHealthSleep(
		context.Background(),
		client,
		time.Date(2026, 7, 23, 12, 0, 0, 0, loc),
		time.Date(2026, 7, 25, 12, 0, 0, 0, loc),
		loc,
	)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("completed fixture reported pending")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}

	night := records[0]
	if night.Source != SourceGoogleHealth {
		t.Errorf("source = %q", night.Source)
	}
	if got := night.Date.Format(time.DateOnly); got != "2026-07-23" {
		t.Errorf("night date = %s, want 2026-07-23", got)
	}
	if night.DurationMinutes != 430 ||
		night.DeepMinutes != 100 ||
		night.REMMinutes != 90 ||
		night.LightMinutes != 240 ||
		night.AwakeMinutes != 50 {
		t.Errorf("night summary = %+v", night)
	}
	if night.IsNap || !night.NapExplicit {
		t.Errorf("night nap classification = isNap:%t explicit:%t", night.IsNap, night.NapExplicit)
	}

	nap := records[1]
	if !nap.IsNap || !nap.NapExplicit {
		t.Errorf("nap classification = isNap:%t explicit:%t", nap.IsNap, nap.NapExplicit)
	}
	if nap.DurationMinutes != 50 {
		t.Errorf("nap duration = %d, want 50", nap.DurationMinutes)
	}
}

func TestFetchGoogleHealthSleep_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{"reauthorization", http.StatusUnauthorized, `{"error":"invalid token"}`, ErrReauthorizationRequired},
		{
			"missing scope",
			http.StatusForbidden,
			`{"error":{"status":"PERMISSION_DENIED","details":[{"reason":"ACCESS_TOKEN_SCOPE_INSUFFICIENT"}]}}`,
			ErrReauthorizationRequired,
		},
		{"rate limit", http.StatusTooManyRequests, `{"error":"quota"}`, ErrRateLimited},
		{"server error", http.StatusInternalServerError, `{"error":"unavailable"}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(tt.status, tt.body), nil
			})}
			_, _, err := FetchGoogleHealthSleep(
				context.Background(),
				client,
				time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
				time.UTC,
			)
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, tt.wantErr)
			}
		})
	}
}

func TestParseGoogleHealthSleepPoints_FallbacksAndValidation(t *testing.T) {
	t.Parallel()

	points := []googleHealthDataPoint{
		{
			Sleep: googleHealthSleep{
				Interval: googleHealthSleepInterval{
					StartTime: "2026-07-24T14:00:00-04:00",
					EndTime:   "2026-07-24T15:30:00-04:00",
				},
				Summary: googleHealthSleepSummary{
					MinutesAsleep: "not-a-number",
					MinutesAwake:  "15",
				},
			},
		},
		{
			Sleep: googleHealthSleep{
				Interval: googleHealthSleepInterval{
					StartTime: "bad",
					EndTime:   "2026-07-24T15:30:00-04:00",
				},
			},
		},
		{
			Sleep: googleHealthSleep{
				Interval: googleHealthSleepInterval{
					StartTime: "2026-07-24T15:30:00-04:00",
					EndTime:   "2026-07-24T14:00:00-04:00",
				},
			},
		},
	}

	records, pending := parseGoogleHealthSleepPoints(points, time.FixedZone("EDT", -4*60*60))
	if pending {
		t.Fatal("fixture without an explicit processing state reported pending")
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].DurationMinutes != 75 {
		t.Errorf("fallback asleep duration = %d, want 75", records[0].DurationMinutes)
	}
	if !records[0].IsNap || records[0].NapExplicit {
		t.Errorf(
			"heuristic nap classification = isNap:%t explicit:%t",
			records[0].IsNap,
			records[0].NapExplicit,
		)
	}
}

func TestFetchGoogleHealthSleep_RejectsBadRangesAndRepeatedTokens(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"nextPageToken":"same"}`), nil
	})}
	start := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	if _, _, err := FetchGoogleHealthSleep(
		context.Background(),
		client,
		start,
		start.AddDate(0, 0, -1),
		time.UTC,
	); err == nil {
		t.Fatal("expected reversed range error")
	}

	if _, _, err := FetchGoogleHealthSleep(
		context.Background(),
		client,
		start,
		start,
		time.UTC,
	); err == nil || !strings.Contains(err.Error(), "repeated sleep page token") {
		t.Fatalf("repeated token error = %v", err)
	}
}

func TestParseGoogleHealthSleepPoints_SkipsUnprocessedSleep(t *testing.T) {
	t.Parallel()

	processed := false
	points := []googleHealthDataPoint{{
		Sleep: googleHealthSleep{
			Interval: googleHealthSleepInterval{
				StartTime: "2026-07-24T03:00:00Z",
				EndTime:   "2026-07-24T11:00:00Z",
			},
			Metadata: googleHealthSleepMetadata{Processed: &processed},
		},
	}}

	records, pending := parseGoogleHealthSleepPoints(points, time.UTC)
	if len(records) != 0 {
		t.Fatalf("unprocessed records = %d, want 0", len(records))
	}
	if !pending {
		t.Fatal("unprocessed Google Health sleep was not marked pending")
	}
}
