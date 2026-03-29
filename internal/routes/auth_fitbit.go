package routes

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"

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

		nonce := generateNonce()
		state := signState(app, info.Auth.Id, nonce)

		cfg := fitbitConfig(re.Request)
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

		cfg := fitbitConfig(re.Request)
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

func fitbitConfig(r *http.Request) *oauth2.Config {
	cfg := *ingest.FitbitOAuthConfig
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	cfg.RedirectURL = scheme + "://" + r.Host + "/auth/fitbit/callback"
	return &cfg
}

func generateNonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func oauthSecret(app *pocketbase.PocketBase) []byte {
	return []byte(app.Settings().Meta.AppURL + ":" + app.Settings().Meta.AppName)
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
