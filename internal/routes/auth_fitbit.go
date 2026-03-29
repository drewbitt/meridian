package routes

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/drewbitt/circadian/internal/ingest"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
)

func registerFitbitAuthRoutes(se *core.ServeEvent, app *pocketbase.PocketBase) {
	se.Router.GET("/auth/fitbit", func(re *core.RequestEvent) error {
		info, _ := re.RequestInfo()
		if info.Auth == nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		cfg := fitbitConfig(app)
		if cfg == nil {
			return re.BadRequestError("Fitbit OAuth not configured", nil)
		}

		nonce, err := generateNonce()
		if err != nil {
			return re.InternalServerError("Failed to generate nonce", err)
		}
		state := signState(app, info.Auth.Id, nonce)

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

		cfg := fitbitConfig(app)
		if cfg == nil {
			return re.BadRequestError("Fitbit OAuth not configured", nil)
		}

		token, err := cfg.Exchange(context.Background(), code)
		if err != nil {
			return re.InternalServerError("Token exchange failed", err)
		}

		settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
		if err != nil {
			collection, _ := app.FindCollectionByNameOrId("settings")
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

func fitbitConfig(app *pocketbase.PocketBase) *oauth2.Config {
	settings, err := app.FindFirstRecordByFilter("settings", "fitbit_client_id != ''", nil)
	if err != nil {
		return nil
	}

	siteURL := settings.GetString("site_url")
	if siteURL == "" {
		siteURL = app.Settings().Meta.AppURL
	}

	cfg := &oauth2.Config{
		ClientID:     settings.GetString("fitbit_client_id"),
		ClientSecret: settings.GetString("fitbit_client_secret"),
		Scopes:       []string{"sleep"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://www.fitbit.com/oauth2/authorize",
			TokenURL: "https://api.fitbit.com/oauth2/token",
		},
		RedirectURL: strings.TrimRight(siteURL, "/") + "/auth/fitbit/callback",
	}

	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}

	ingest.FitbitOAuthConfig = cfg
	return cfg
}

func generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func oauthSecret(app *pocketbase.PocketBase) []byte {
	return []byte(app.Settings().Meta.AppURL + ":" + app.Settings().Meta.SenderAddress + ":" + app.Settings().Meta.SenderName)
}

func signState(app *pocketbase.PocketBase, userID, nonce string) string {
	payload := userID + ":" + nonce
	mac := hmac.New(sha256.New, oauthSecret(app))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + ":" + sig
}

func verifyState(app *pocketbase.PocketBase, state string) (string, error) {
	lastColon := -1
	for i := len(state) - 1; i >= 0; i-- {
		if state[i] == ':' {
			lastColon = i
			break
		}
	}
	if lastColon <= 0 {
		return "", fmt.Errorf("malformed state")
	}
	payload := state[:lastColon]
	sig := state[lastColon+1:]

	mac := hmac.New(sha256.New, oauthSecret(app))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", fmt.Errorf("invalid state signature")
	}

	firstColon := -1
	for i, c := range payload {
		if c == ':' {
			firstColon = i
			break
		}
	}
	if firstColon <= 0 {
		return "", fmt.Errorf("malformed state payload")
	}

	return payload[:firstColon], nil
}
