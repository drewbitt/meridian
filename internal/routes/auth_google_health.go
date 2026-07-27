package routes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/drewbitt/meridian/internal/services"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
)

var (
	errInvalidState = errors.New("invalid or already used state")
	errExpiredState = errors.New("state expired")
	errGoogleRevoke = errors.New("google token revocation failed")

	googleHealthOAuthStates = struct {
		sync.Mutex
		values map[string]googleHealthOAuthState
	}{values: make(map[string]googleHealthOAuthState)}
)

type googleHealthOAuthState struct {
	userID    string
	verifier  string
	expiresAt time.Time
}

func registerGoogleHealthAuthRoutes(se *core.ServeEvent, app core.App) {
	se.Router.GET("/auth/google-health", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		cfg := googleHealthConfigForUser(app, userID)
		if cfg == nil {
			return re.Redirect(http.StatusSeeOther, "/settings?health_error=not_configured")
		}

		nonce, err := generateNonce()
		if err != nil {
			return re.InternalServerError("Failed to generate nonce", err)
		}
		verifier := oauth2.GenerateVerifier()
		state := signState(app, userID, nonce, verifier)
		authURL := cfg.AuthCodeURL(
			state,
			oauth2.AccessTypeOffline,
			oauth2.S256ChallengeOption(verifier),
			oauth2.SetAuthURLParam("prompt", "consent"),
		)
		return re.Redirect(http.StatusTemporaryRedirect, authURL)
	})

	se.Router.GET("/auth/google-health/callback", func(re *core.RequestEvent) error {
		query := re.Request.URL.Query()
		state := query.Get("state")
		if state == "" {
			return re.BadRequestError("Missing state", nil)
		}
		userID, verifier, err := verifyState(app, state)
		if err != nil {
			return re.BadRequestError("Invalid state", err)
		}
		if query.Get("error") != "" {
			return re.Redirect(http.StatusSeeOther, "/settings?health_error=authorization_denied")
		}

		code := query.Get("code")
		if code == "" {
			return re.BadRequestError("Missing code", nil)
		}

		cfg := googleHealthConfigForUser(app, userID)
		if cfg == nil {
			return re.Redirect(http.StatusSeeOther, "/settings?health_error=not_configured")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		token, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
		if err != nil {
			slog.Error("google health token exchange failed", "user_id", userID, "error", err)
			return re.Redirect(http.StatusSeeOther, "/settings?health_error=token_exchange")
		}
		if !googleHealthSleepScopeGranted(token) {
			return re.Redirect(http.StatusSeeOther, "/settings?health_error=insufficient_scope")
		}

		settings, err := app.FindFirstRecordByFilter(
			"settings",
			"user = {:user}",
			map[string]any{"user": userID},
		)
		if err != nil {
			collection, findErr := app.FindCollectionByNameOrId("settings")
			if findErr != nil {
				return re.InternalServerError("settings collection not found", findErr)
			}
			settings = core.NewRecord(collection)
			settings.Set("user", userID)
		}
		if token.RefreshToken == "" {
			token.RefreshToken = settings.GetString("google_health_refresh_token")
		}
		if token.RefreshToken == "" {
			return re.Redirect(http.StatusSeeOther, "/settings?health_error=missing_refresh_token")
		}

		settings.Set("google_health_access_token", token.AccessToken)
		settings.Set("google_health_refresh_token", token.RefreshToken)
		settings.Set("google_health_token_expiry", token.Expiry)
		settings.Set("google_health_sleep_pending", false)
		if err := app.Save(settings); err != nil {
			return re.InternalServerError("Failed to save tokens", err)
		}

		go func(uid string) {
			current, err := app.FindFirstRecordByFilter(
				"settings",
				"user = {:user}",
				map[string]any{"user": uid},
			)
			if err != nil {
				slog.Error(
					"google health backfill: could not load settings",
					"user_id",
					uid,
					"error",
					err,
				)
				return
			}
			end := time.Now()
			start := end.AddDate(0, 0, -30)
			if err := services.SyncGoogleHealthUser(app, current, start, end); err != nil &&
				!errors.Is(err, ingest.ErrSleepPending) {
				slog.Error("google health backfill failed", "user_id", uid, "error", err)
				return
			}
			if _, err := services.RefreshScheduleIfNeeded(app, uid); err != nil {
				slog.Error("google health backfill schedule refresh failed", "user_id", uid, "error", err)
			}
			if err := services.RunMorningJob(app, uid); err != nil {
				slog.Error("google health backfill summary reconciliation failed", "user_id", uid, "error", err)
			}
		}(userID)

		return re.Redirect(http.StatusSeeOther, "/settings?health=connected")
	})

	se.Router.POST("/auth/google-health/disconnect", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}
		settings, err := app.FindFirstRecordByFilter(
			"settings",
			"user = {:user}",
			map[string]any{"user": userID},
		)
		if err != nil {
			return re.Redirect(http.StatusSeeOther, "/settings")
		}

		token := settings.GetString("google_health_refresh_token")
		if token == "" {
			token = settings.GetString("google_health_access_token")
		}
		if token != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := revokeGoogleToken(ctx, token); err != nil {
				slog.Warn("google health token revocation failed", "user_id", userID, "error", err)
			}
		}

		settings.Set("google_health_access_token", "")
		settings.Set("google_health_refresh_token", "")
		settings.Set("google_health_token_expiry", nil)
		settings.Set("google_health_sleep_pending", false)
		if err := app.Save(settings); err != nil {
			return re.InternalServerError("Failed to clear tokens", err)
		}
		return re.Redirect(http.StatusSeeOther, "/settings?health=disconnected")
	})

	se.Router.POST("/auth/google-health/sync", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}
		settings, err := app.FindFirstRecordByFilter(
			"settings",
			"user = {:user} && google_health_access_token != ''",
			map[string]any{"user": userID},
		)
		if err != nil {
			return re.Redirect(http.StatusSeeOther, "/settings?health_error=not_configured")
		}

		end := time.Now()
		start := end.AddDate(0, 0, -1)
		if err := services.SyncGoogleHealthUser(app, settings, start, end); err != nil {
			if errors.Is(err, ingest.ErrSleepPending) {
				return re.Redirect(http.StatusSeeOther, "/settings?health=pending")
			}
			slog.Error("manual google health sync failed", "user_id", userID, "error", err)
			return re.Redirect(http.StatusSeeOther, "/settings?health_error=sync_failed")
		}
		if err := services.RunMorningJob(app, userID); err != nil {
			slog.Error("schedule update after manual sync failed", "user_id", userID, "error", err)
		}
		return re.Redirect(http.StatusSeeOther, "/settings?health=synced")
	})
}

func googleHealthConfigForUser(app core.App, userID string) *oauth2.Config {
	settings, err := app.FindFirstRecordByFilter(
		"settings",
		"user = {:user}",
		map[string]any{"user": userID},
	)
	if err != nil {
		return nil
	}
	return services.GoogleHealthConfigFromSettings(app, settings)
}

func generateNonce() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(value), nil
}

const stateMaxAge = 10 * time.Minute

// signState keeps the PKCE verifier server-side and returns only a random
// one-time state value to the browser and Google.
func signState(_ core.App, userID, nonce, verifier string) string {
	now := time.Now()
	googleHealthOAuthStates.Lock()
	defer googleHealthOAuthStates.Unlock()
	for state, pending := range googleHealthOAuthStates.values {
		if !pending.expiresAt.After(now) {
			delete(googleHealthOAuthStates.values, state)
		}
	}
	googleHealthOAuthStates.values[nonce] = googleHealthOAuthState{
		userID:    userID,
		verifier:  verifier,
		expiresAt: now.Add(stateMaxAge),
	}
	return nonce
}

func verifyState(_ core.App, state string) (userID, verifier string, err error) {
	googleHealthOAuthStates.Lock()
	pending, ok := googleHealthOAuthStates.values[state]
	delete(googleHealthOAuthStates.values, state)
	googleHealthOAuthStates.Unlock()
	if !ok {
		return "", "", errInvalidState
	}
	if time.Now().After(pending.expiresAt) {
		return "", "", errExpiredState
	}
	return pending.userID, pending.verifier, nil
}

func revokeGoogleToken(ctx context.Context, token string) error {
	body := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://oauth2.googleapis.com/revoke",
		strings.NewReader(body.Encode()),
	)
	if err != nil {
		return fmt.Errorf("create google revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("google revoke request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("google revoke returned %d: %w", resp.StatusCode, errGoogleRevoke)
	}
	return nil
}

func googleHealthSleepScopeGranted(token *oauth2.Token) bool {
	if token == nil {
		return false
	}
	raw, _ := token.Extra("scope").(string)
	for scope := range strings.FieldsSeq(raw) {
		if scope == ingest.GoogleHealthSleepReadonlyScope {
			return true
		}
	}
	return false
}
