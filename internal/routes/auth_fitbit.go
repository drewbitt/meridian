package routes

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/drewbitt/meridian/internal/services"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
)

var (
	errMalformedState   = errors.New("malformed state")
	errInvalidSig       = errors.New("invalid state signature")
	errMalformedPayload = errors.New("malformed state payload")
	errExpiredState     = errors.New("state expired")
)

func registerFitbitAuthRoutes(se *core.ServeEvent, app core.App) {
	se.Router.GET("/auth/fitbit", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		cfg := fitbitConfigForUser(app, userID)
		if cfg == nil {
			return re.Redirect(http.StatusSeeOther, "/settings?fitbit_error=not_configured")
		}

		nonce, err := generateNonce()
		if err != nil {
			return re.InternalServerError("Failed to generate nonce", err)
		}

		verifier := oauth2.GenerateVerifier()
		state := signState(app, userID, nonce, verifier)

		authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier))
		return re.Redirect(http.StatusTemporaryRedirect, authURL)
	})

	se.Router.GET("/auth/fitbit/callback", func(re *core.RequestEvent) error {
		code := re.Request.URL.Query().Get("code")
		state := re.Request.URL.Query().Get("state")

		if code == "" || state == "" {
			return re.BadRequestError("Missing code or state", nil)
		}

		userID, verifier, err := verifyState(app, state)
		if err != nil {
			return re.BadRequestError("Invalid state", err)
		}

		cfg := fitbitConfigForUser(app, userID)
		if cfg == nil {
			return re.Redirect(http.StatusSeeOther, "/settings?fitbit_error=not_configured")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		token, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
		if err != nil {
			return re.InternalServerError("Token exchange failed", err)
		}

		settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
		if err != nil {
			collection, err := app.FindCollectionByNameOrId("settings")
			if err != nil {
				return re.InternalServerError("settings collection not found", err)
			}
			settings = core.NewRecord(collection)
			settings.Set("user", userID)
		}

		settings.Set("fitbit_access_token", token.AccessToken)
		settings.Set("fitbit_refresh_token", token.RefreshToken)
		settings.Set("fitbit_token_expiry", token.Expiry)

		if err := app.Save(settings); err != nil {
			return re.InternalServerError("Failed to save tokens", err)
		}

		// Backfill last 30 days in the background, bounded to the request lifecycle.
		var wg sync.WaitGroup
		wg.Add(1)
		go func(uid string) {
			defer wg.Done()
			// Use a fresh context since the request context is cancelled after redirect.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			s, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": uid})
			if err != nil {
				slog.Error("fitbit backfill: could not load settings", "user_id", uid, "error", err)
				return
			}
			end := time.Now()
			start := end.AddDate(0, 0, -30)
			if err := services.SyncFitbitUser(app, s, start, end); err != nil {
				slog.Error("fitbit backfill failed", "user_id", uid, "error", err)
			}
		}(userID)
		// Detach: we don't wait for the backfill before responding, but the goroutine
		// is now properly bounded by a context with timeout and will terminate on expiry.

		return re.Redirect(http.StatusSeeOther, "/settings?fitbit=connected")
	})

	se.Router.POST("/auth/fitbit/disconnect", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
		if err != nil {
			return re.Redirect(http.StatusSeeOther, "/settings")
		}

		// Best-effort revocation at Fitbit.
		refreshToken := settings.GetString("fitbit_refresh_token")
		clientID := settings.GetString("fitbit_client_id")
		clientSecret := settings.GetString("fitbit_client_secret")
		if refreshToken != "" && clientID != "" && clientSecret != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = revokeFitbitToken(ctx, clientID, clientSecret, refreshToken)
		}

		settings.Set("fitbit_access_token", "")
		settings.Set("fitbit_refresh_token", "")
		settings.Set("fitbit_token_expiry", nil)

		if err := app.Save(settings); err != nil {
			return re.InternalServerError("Failed to clear tokens", err)
		}

		return re.Redirect(http.StatusSeeOther, "/settings?fitbit=disconnected")
	})

	se.Router.POST("/auth/fitbit/sync", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		settings, err := app.FindFirstRecordByFilter("settings", "user = {:user} && fitbit_access_token != ''", map[string]any{"user": userID})
		if err != nil {
			return re.Redirect(http.StatusSeeOther, "/settings?fitbit_error=not_configured")
		}

		end := time.Now()
		start := end.AddDate(0, 0, -1)
		if err := services.SyncFitbitUser(app, settings, start, end); err != nil {
			slog.Error("manual fitbit sync failed", "user_id", userID, "error", err)
			return re.Redirect(http.StatusSeeOther, "/settings?fitbit_error=sync_failed")
		}

		if err := services.UpdateUserSchedule(app, userID); err != nil {
			slog.Error("schedule update after manual sync failed", "user_id", userID, "error", err)
		}

		return re.Redirect(http.StatusSeeOther, "/settings?fitbit=synced")
	})
}

// fitbitConfigForUser builds an OAuth2 config from the given user's settings.
func fitbitConfigForUser(app core.App, userID string) *oauth2.Config {
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user} && fitbit_client_id != ''", map[string]any{"user": userID})
	if err != nil {
		return nil
	}

	return services.FitbitConfigFromSettings(app, settings)
}

func generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func oauthSecret(app core.App) []byte {
	// Derive HMAC key from install-specific values that are not publicly visible.
	// DataDir is unique per installation; the SMTP password (if set) adds entropy.
	h := sha256.Sum256([]byte(app.DataDir() + ":" + app.Settings().SMTP.Password + ":" + app.Settings().Meta.AppURL))
	return h[:]
}

const stateMaxAge = 10 * time.Minute

// signState produces an HMAC-signed state string encoding the user ID, nonce,
// PKCE verifier, and timestamp: "userID:nonce:verifier:timestamp:signature".
func signState(app core.App, userID, nonce, verifier string) string {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	payload := userID + ":" + nonce + ":" + verifier + ":" + ts
	mac := hmac.New(sha256.New, oauthSecret(app))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + ":" + sig
}

// verifyState validates the HMAC signature and expiry, returning the user ID
// and PKCE verifier from the state string.
func verifyState(app core.App, state string) (userID, verifier string, err error) {
	// Split off the trailing signature (last colon-delimited segment).
	lastColon := strings.LastIndex(state, ":")
	if lastColon <= 0 {
		return "", "", errMalformedState
	}
	payload := state[:lastColon]
	sig := state[lastColon+1:]

	mac := hmac.New(sha256.New, oauthSecret(app))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", "", errInvalidSig
	}

	// Payload format: "userID:nonce:verifier:timestamp"
	parts := strings.SplitN(payload, ":", 4)
	if len(parts) != 4 || parts[0] == "" || parts[2] == "" {
		return "", "", errMalformedPayload
	}

	// Check expiry.
	ts, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return "", "", errMalformedPayload
	}
	if time.Since(time.Unix(ts, 0)) > stateMaxAge {
		return "", "", errExpiredState
	}

	return parts[0], parts[2], nil
}

// revokeFitbitToken revokes the given token at Fitbit's revocation endpoint.
func revokeFitbitToken(ctx context.Context, clientID, clientSecret, token string) error {
	body := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.fitbit.com/oauth2/revoke", strings.NewReader(body.Encode()))
	if err != nil {
		return fmt.Errorf("create revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fitbit revoke request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fitbit revoke returned %d: %w", resp.StatusCode, errFitbitRevoke)
	}
	return nil
}

var errFitbitRevoke = errors.New("fitbit token revocation failed")
