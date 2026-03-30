package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
)

var errFitbitCredentials = errors.New("fitbit credentials missing")

// Per-user mutex to prevent concurrent token refreshes from invalidating
// each other (Fitbit revokes old refresh tokens on use).
var (
	syncMu    sync.Mutex
	syncLocks = make(map[string]*sync.Mutex)
)

func userSyncLock(userID string) *sync.Mutex {
	syncMu.Lock()
	defer syncMu.Unlock()
	mu, ok := syncLocks[userID]
	if !ok {
		mu = &sync.Mutex{}
		syncLocks[userID] = mu
	}
	return mu
}

// SyncFitbitUser syncs Fitbit sleep data for a single user's settings record
// over the given date range. It handles token refresh and upserts results.
func SyncFitbitUser(app core.App, s *core.Record, start, end time.Time) error {
	userID := s.GetString("user")

	// Serialize per-user to prevent concurrent token refresh races.
	mu := userSyncLock(userID)
	mu.Lock()
	defer mu.Unlock()

	// Re-fetch settings to get the latest token (another goroutine may have refreshed it).
	fresh, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err == nil {
		s = fresh
	}

	cfg := fitbitConfigFromSettings(app, s)
	if cfg == nil {
		return fmt.Errorf("%w for user %s", errFitbitCredentials, userID)
	}

	token := &oauth2.Token{
		AccessToken:  s.GetString("fitbit_access_token"),
		RefreshToken: s.GetString("fitbit_refresh_token"),
		Expiry:       s.GetDateTime("fitbit_token_expiry").Time(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Refresh token if expired.
	if token.Expiry.Before(time.Now()) {
		newToken, err := ingest.RefreshFitbitToken(ctx, cfg, token)
		if err != nil {
			return fmt.Errorf("token refresh: %w", err)
		}
		s.Set("fitbit_access_token", newToken.AccessToken)
		s.Set("fitbit_refresh_token", newToken.RefreshToken)
		s.Set("fitbit_token_expiry", newToken.Expiry)
		if err := app.Save(s); err != nil {
			return fmt.Errorf("save refreshed token: %w", err)
		}
		token = newToken
	}

	// Fetch user's timezone from Fitbit profile.
	loc, err := ingest.FetchFitbitTimezone(ctx, cfg, token)
	if err != nil {
		slog.Warn("could not fetch fitbit timezone, falling back to UTC", "user_id", userID, "error", err)
		loc = time.UTC
	}

	// Auto-populate the user's timezone setting from Fitbit profile if not yet configured.
	if s.GetString("timezone") == "" && loc != time.UTC {
		s.Set("timezone", loc.String())
	}

	// Use range endpoint for multi-day fetches, single-day for 1-day syncs.
	var records []ingest.SleepRecord
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		records, err = ingest.FetchFitbitSleep(ctx, cfg, token, start, loc)
	} else {
		records, err = ingest.FetchFitbitSleepRange(ctx, cfg, token, start, end, loc)
	}
	if err != nil {
		return fmt.Errorf("fetch sleep: %w", err)
	}

	for _, rec := range records {
		if _, err := UpsertSleepRecord(app, userID, rec); err != nil {
			slog.Error("failed to save fitbit record", "user_id", userID, "error", err)
		}
	}

	// Update last sync timestamp.
	s.Set("fitbit_last_sync", time.Now())
	if err := app.Save(s); err != nil {
		slog.Warn("failed to update fitbit_last_sync", "user_id", userID, "error", err)
	}

	return nil
}

func fitbitConfigFromSettings(app core.App, s *core.Record) *oauth2.Config {
	clientID := s.GetString("fitbit_client_id")
	clientSecret := s.GetString("fitbit_client_secret")
	if clientID == "" || clientSecret == "" {
		return nil
	}
	siteURL := s.GetString("site_url")
	if siteURL == "" {
		siteURL = app.Settings().Meta.AppURL
	}
	return ingest.NewFitbitOAuthConfig(clientID, clientSecret, strings.TrimRight(siteURL, "/")+"/auth/fitbit/callback")
}
