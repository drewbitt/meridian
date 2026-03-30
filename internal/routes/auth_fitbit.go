package routes

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
)

var (
	errMalformedState   = errors.New("malformed state")
	errInvalidSig       = errors.New("invalid state signature")
	errMalformedPayload = errors.New("malformed state payload")
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
		state := signState(app, userID, nonce)

		url := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
		return re.Redirect(http.StatusTemporaryRedirect, url)
	})

	se.Router.GET("/auth/fitbit/callback", func(re *core.RequestEvent) error {
		code := re.Request.URL.Query().Get("code")
		state := re.Request.URL.Query().Get("state")

		if code == "" || state == "" {
			return re.BadRequestError("Missing code or state", nil)
		}

		userID, err := verifyState(app, state)
		if err != nil {
			return re.BadRequestError("Invalid state", err)
		}

		cfg := fitbitConfigForUser(app, userID)
		if cfg == nil {
			return re.Redirect(http.StatusSeeOther, "/settings?fitbit_error=not_configured")
		}

		token, err := cfg.Exchange(context.Background(), code)
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

		return re.Redirect(http.StatusSeeOther, "/settings?fitbit=connected")
	})
}

// fitbitConfigForUser builds an OAuth2 config from the given user's settings.
func fitbitConfigForUser(app core.App, userID string) *oauth2.Config {
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user} && fitbit_client_id != ''", map[string]any{"user": userID})
	if err != nil {
		return nil
	}

	return buildFitbitConfig(app, settings)
}

// buildFitbitConfig creates an OAuth2 config from a settings record.
func buildFitbitConfig(app core.App, settings *core.Record) *oauth2.Config {
	clientID := settings.GetString("fitbit_client_id")
	clientSecret := settings.GetString("fitbit_client_secret")
	if clientID == "" || clientSecret == "" {
		return nil
	}

	siteURL := settings.GetString("site_url")
	if siteURL == "" {
		siteURL = app.Settings().Meta.AppURL
	}

	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{"sleep"},
		Endpoint: oauth2.Endpoint{ //nolint:gosec // OAuth URLs, not credentials
			AuthURL:  "https://www.fitbit.com/oauth2/authorize",
			TokenURL: "https://api.fitbit.com/oauth2/token",
		},
		RedirectURL: strings.TrimRight(siteURL, "/") + "/auth/fitbit/callback",
	}
}

func generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func oauthSecret(app core.App) []byte {
	return []byte(app.Settings().Meta.AppURL + ":" + app.Settings().Meta.SenderAddress + ":" + app.Settings().Meta.SenderName)
}

func signState(app core.App, userID, nonce string) string {
	payload := userID + ":" + nonce
	mac := hmac.New(sha256.New, oauthSecret(app))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + ":" + sig
}

func verifyState(app core.App, state string) (string, error) {
	lastColon := -1
	for i := len(state) - 1; i >= 0; i-- {
		if state[i] == ':' {
			lastColon = i
			break
		}
	}
	if lastColon <= 0 {
		return "", errMalformedState
	}
	payload := state[:lastColon]
	sig := state[lastColon+1:]

	mac := hmac.New(sha256.New, oauthSecret(app))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", errInvalidSig
	}

	firstColon := -1
	for i, c := range payload {
		if c == ':' {
			firstColon = i
			break
		}
	}
	if firstColon <= 0 {
		return "", errMalformedPayload
	}

	return payload[:firstColon], nil
}
