package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
)

var (
	errGoogleHealthCredentials = errors.New("google health credentials missing")
	errGoogleHealthSaveRecords = errors.New("save google health records")

	healthSyncLocks sync.Map // map[userID]*sync.Mutex
)

func userHealthSyncLock(userID string) *sync.Mutex {
	value, _ := healthSyncLocks.LoadOrStore(userID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// SyncGoogleHealthUser syncs reconciled Google Health sleep data for one user
// over the inclusive local-date range and upserts every returned session.
func SyncGoogleHealthUser(app core.App, settings *core.Record, start, end time.Time) error {
	userID := settings.GetString("user")
	lock := userHealthSyncLock(userID)
	lock.Lock()
	defer lock.Unlock()

	// A manual sync and a cron tick can arrive together. Always start with the
	// newest saved token.
	if fresh, err := app.FindFirstRecordByFilter(
		"settings",
		"user = {:user}",
		map[string]any{"user": userID},
	); err == nil {
		settings = fresh
	}

	cfg := GoogleHealthConfigFromSettings(app, settings)
	if cfg == nil || settings.GetString("google_health_access_token") == "" {
		return fmt.Errorf("%w for user %s", errGoogleHealthCredentials, userID)
	}

	storedToken := &oauth2.Token{
		AccessToken:  settings.GetString("google_health_access_token"),
		RefreshToken: settings.GetString("google_health_refresh_token"),
		Expiry:       settings.GetDateTime("google_health_token_expiry").Time(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tokenSource := oauth2.ReuseTokenSource(storedToken, cfg.TokenSource(ctx, storedToken))
	client := oauth2.NewClient(ctx, tokenSource)
	loc := LocationFromSettings(settings)
	records, pending, err := ingest.FetchGoogleHealthSleep(ctx, client, start, end, loc)
	if err != nil {
		settings.Set("google_health_last_attempt", time.Now())
		if saveErr := app.Save(settings); saveErr != nil {
			slog.Warn(
				"failed to record google health sync attempt",
				"user_id",
				userID,
				"error",
				saveErr,
			)
		}
		if isGoogleHealthReauthorizationError(err) {
			clearGoogleHealthTokens(app, settings)
		} else if errors.Is(err, ingest.ErrRateLimited) {
			slog.Warn("google health API rate limited, skipping sync", "user_id", userID)
		}
		return fmt.Errorf("fetch google health sleep: %w", err)
	}

	refreshedToken, err := tokenSource.Token()
	if err != nil {
		if isGoogleHealthReauthorizationError(err) {
			clearGoogleHealthTokens(app, settings)
		}
		return fmt.Errorf("refresh google health token: %w", err)
	}
	if tokenChanged(storedToken, refreshedToken) {
		settings.Set("google_health_access_token", refreshedToken.AccessToken)
		settings.Set("google_health_refresh_token", refreshedToken.RefreshToken)
		settings.Set("google_health_token_expiry", refreshedToken.Expiry)
		if err := app.Save(settings); err != nil {
			return fmt.Errorf("save refreshed google health token: %w", err)
		}
	}

	saveFailures := 0
	for _, record := range records {
		if _, err := UpsertSleepRecord(app, userID, record); err != nil {
			slog.Error("failed to save google health record", "user_id", userID, "error", err)
			saveFailures++
		}
	}
	if saveFailures > 0 {
		return fmt.Errorf(
			"%w: %d of %d failed",
			errGoogleHealthSaveRecords,
			saveFailures,
			len(records),
		)
	}

	syncedAt := time.Now()
	settings.Set("google_health_last_attempt", syncedAt)
	settings.Set("google_health_sleep_pending", pending)
	if !pending {
		settings.Set("google_health_last_sync", syncedAt)
	}
	if err := app.Save(settings); err != nil {
		return fmt.Errorf("save google health sync state: %w", err)
	}
	if pending {
		return fmt.Errorf("%w", ingest.ErrSleepPending)
	}
	return nil
}

func tokenChanged(before, after *oauth2.Token) bool {
	return before.AccessToken != after.AccessToken ||
		before.RefreshToken != after.RefreshToken ||
		!before.Expiry.Equal(after.Expiry)
}

func isGoogleHealthReauthorizationError(err error) bool {
	if errors.Is(err, ingest.ErrReauthorizationRequired) {
		return true
	}
	var retrieveErr *oauth2.RetrieveError
	return errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant"
}

func clearGoogleHealthTokens(app core.App, settings *core.Record) {
	settings.Set("google_health_access_token", "")
	settings.Set("google_health_refresh_token", "")
	settings.Set("google_health_token_expiry", nil)
	settings.Set("google_health_sleep_pending", false)
	if err := app.Save(settings); err != nil {
		slog.Error(
			"failed to clear invalid google health tokens",
			"user_id",
			settings.GetString("user"),
			"error",
			err,
		)
	}
	notifyGoogleHealthDisconnected(settings)
}

func notifyGoogleHealthDisconnected(settings *core.Record) {
	if !settings.GetBool("notifications_enabled") || settings.GetString("ntfy_topic") == "" {
		return
	}
	settingsURL := ""
	if siteURL := settings.GetString("site_url"); siteURL != "" {
		settingsURL = strings.TrimRight(siteURL, "/") + "/settings"
	}
	notification := Notification{
		Server:      settings.GetString("ntfy_server"),
		Topic:       settings.GetString("ntfy_topic"),
		AccessToken: settings.GetString("ntfy_access_token"),
		Title:       "Google Health disconnected",
		Message:     "Reconnect Google Health in Settings to resume sleep sync.",
		Priority:    4,
		Tags:        []string{"warning", "link"},
	}
	if settingsURL != "" {
		notification.Click = settingsURL
		notification.Actions = []Action{{Type: "view", Label: "Reconnect", URL: settingsURL}}
	}
	if err := SendNotification(notification); err != nil {
		slog.Error(
			"failed to send google health disconnect notification",
			"user_id",
			settings.GetString("user"),
			"error",
			err,
		)
	}
}

// GoogleHealthConfigFromSettings builds the per-installation OAuth2 config.
func GoogleHealthConfigFromSettings(app core.App, settings *core.Record) *oauth2.Config {
	envClientID := strings.TrimSpace(os.Getenv("GOOGLE_HEALTH_CLIENT_ID"))
	envClientSecret := strings.TrimSpace(os.Getenv("GOOGLE_HEALTH_CLIENT_SECRET"))
	clientID, clientSecret := envClientID, envClientSecret
	if envClientID == "" && envClientSecret == "" && settings != nil {
		clientID = settings.GetString("google_health_client_id")
		clientSecret = settings.GetString("google_health_client_secret")
	}
	if clientID == "" || clientSecret == "" {
		return nil
	}
	siteURL := ""
	if settings != nil {
		siteURL = settings.GetString("site_url")
	}
	if siteURL == "" {
		siteURL = app.Settings().Meta.AppURL
	}
	redirectURL, ok := GoogleHealthRedirectURL(siteURL)
	if !ok {
		return nil
	}
	return ingest.NewGoogleHealthOAuthConfig(
		clientID,
		clientSecret,
		redirectURL,
	)
}

// GoogleHealthRedirectURL enforces Google's web-client redirect rules and
// Meridian's root-mounted routes. HTTP is allowed only for loopback local
// development; deployed instances must use HTTPS.
func GoogleHealthRedirectURL(siteURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(siteURL))
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", false
	}

	hostname := parsed.Hostname()
	ip := net.ParseIP(hostname)
	isLoopback := strings.EqualFold(hostname, "localhost") ||
		(ip != nil && ip.IsLoopback())
	if ip != nil && !ip.IsLoopback() {
		// Google exempts localhost IP addresses from its hostname rule, but
		// rejects other raw IP redirect hosts even when they use HTTPS.
		return "", false
	}

	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopback {
			return "", false
		}
	default:
		return "", false
	}

	parsed.Path = "/auth/google-health/callback"
	parsed.RawPath = ""
	return parsed.String(), true
}

// GoogleHealthCredentialsManaged reports whether this installation supplies
// the OAuth client through environment variables instead of each user's
// settings record.
func GoogleHealthCredentialsManaged() bool {
	return strings.TrimSpace(os.Getenv("GOOGLE_HEALTH_CLIENT_ID")) != "" &&
		strings.TrimSpace(os.Getenv("GOOGLE_HEALTH_CLIENT_SECRET")) != ""
}
