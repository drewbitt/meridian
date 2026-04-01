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
var syncLocks sync.Map // map[string]*sync.Mutex

func userSyncLock(userID string) *sync.Mutex {
	val, _ := syncLocks.LoadOrStore(userID, &sync.Mutex{})
	return val.(*sync.Mutex)
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

	cfg := FitbitConfigFromSettings(app, s)
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

	// Refresh token proactively before expiry. The 5-minute buffer prevents
	// the token from expiring mid-request, which would cause the oauth2 client
	// to silently refresh (rotating the refresh token) without persisting
	// the new token — leading to permanent disconnect on next sync.
	if token.Expiry.Before(time.Now().Add(5 * time.Minute)) {
		newToken, err := ingest.RefreshFitbitToken(ctx, cfg, token)
		if err != nil && errors.Is(err, ingest.ErrTokenRevoked) {
			// Retry once: Fitbit has a 2-minute idempotency window where
			// identical refresh requests return the same response. This
			// handles the case where the first request reached Fitbit but
			// the response was lost (e.g., network timeout). The old refresh
			// token is already rotated, but within the window Fitbit will
			// re-issue the same new token pair.
			slog.Warn("fitbit token refresh got invalid_grant, retrying once", "user_id", userID)
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return fmt.Errorf("token refresh: %w", ctx.Err())
			}
			newToken, err = ingest.RefreshFitbitToken(ctx, cfg, token)
		}
		if err != nil {
			if errors.Is(err, ingest.ErrTokenRevoked) {
				slog.Warn("fitbit token permanently invalid, clearing stored tokens", "user_id", userID)
				s.Set("fitbit_access_token", "")
				s.Set("fitbit_refresh_token", "")
				s.Set("fitbit_token_expiry", nil)
				if saveErr := app.Save(s); saveErr != nil {
					slog.Error("failed to clear revoked fitbit tokens", "user_id", userID, "error", saveErr)
				}
				notifyTokenRevoked(s)
			}
			return fmt.Errorf("token refresh: %w", err)
		}
		s.Set("fitbit_access_token", newToken.AccessToken)
		s.Set("fitbit_refresh_token", newToken.RefreshToken)
		s.Set("fitbit_token_expiry", newToken.Expiry)
		// Save immediately — Fitbit already rotated the refresh token, so
		// the old one in the DB is dead. If this save fails, clear the
		// tokens to avoid a stale (revoked) refresh token persisting,
		// which would cause a permanent disconnect on next sync.
		if err := app.Save(s); err != nil {
			slog.Error("failed to save refreshed token, clearing to avoid stale state", "user_id", userID, "error", err)
			s.Set("fitbit_access_token", "")
			s.Set("fitbit_refresh_token", "")
			s.Set("fitbit_token_expiry", nil)
			_ = app.Save(s)
			notifyTokenRevoked(s)
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
	// If sleep data is still being classified (user just woke up), wait
	// briefly and retry once. Fitbit typically needs ~3 seconds.
	if err != nil && errors.Is(err, ingest.ErrSleepPending) {
		slog.Info("fitbit sleep data pending, retrying after short wait", "user_id", userID)
		select {
		case <-time.After(4 * time.Second):
		case <-ctx.Done():
			return fmt.Errorf("fetch sleep: %w", ctx.Err())
		}
		if start.Format("2006-01-02") == end.Format("2006-01-02") {
			records, err = ingest.FetchFitbitSleep(ctx, cfg, token, start, loc)
		} else {
			records, err = ingest.FetchFitbitSleepRange(ctx, cfg, token, start, end, loc)
		}
	}
	if err != nil {
		if errors.Is(err, ingest.ErrRateLimited) {
			slog.Warn("fitbit API rate limited, skipping sync", "user_id", userID)
		}
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

// notifyTokenRevoked sends a notification to the user that their Fitbit
// connection was lost and they need to reconnect.
func notifyTokenRevoked(s *core.Record) {
	if !s.GetBool("notifications_enabled") || s.GetString("ntfy_topic") == "" {
		return
	}
	siteURL := s.GetString("site_url")
	settingsURL := ""
	if siteURL != "" {
		settingsURL = strings.TrimRight(siteURL, "/") + "/settings"
	}
	notif := Notification{
		Server:      s.GetString("ntfy_server"),
		Topic:       s.GetString("ntfy_topic"),
		AccessToken: s.GetString("ntfy_access_token"),
		Title:       "Fitbit disconnected",
		Message:     "Your Fitbit token was revoked. Reconnect in Settings to resume auto-sync.",
		Priority:    4,
		Tags:        []string{"warning", "link"},
	}
	if settingsURL != "" {
		notif.Click = settingsURL
		notif.Actions = []Action{{Type: "view", Label: "Reconnect", URL: settingsURL}}
	}
	if err := SendNotification(notif); err != nil {
		slog.Error("failed to send token-revoked notification", "user_id", s.GetString("user"), "error", err)
	}
}

// FitbitConfigFromSettings builds a Fitbit OAuth2 config from a settings record.
// Returns nil if client credentials are missing.
func FitbitConfigFromSettings(app core.App, s *core.Record) *oauth2.Config {
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
