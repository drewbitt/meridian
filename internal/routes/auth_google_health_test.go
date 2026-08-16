package routes

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"golang.org/x/oauth2"
)

func TestGoogleHealthAuthorize_RedirectUsesLeastPrivilegeOfflinePKCE(t *testing.T) {
	t.Setenv("GOOGLE_HEALTH_CLIENT_ID", "")
	t.Setenv("GOOGLE_HEALTH_CLIENT_SECRET", "")

	headers := map[string]string{}
	(&tests.ApiScenario{
		Name:           "authorize redirects to Google with safe parameters",
		Method:         http.MethodGet,
		URL:            "/auth/google-health",
		ExpectedStatus: http.StatusTemporaryRedirect,
		TestAppFactory: setupApp,
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			registerGoogleHealthAuthRoutes(e, app)
			user, err := app.FindAuthRecordByEmail("users", testUserEmail)
			if err != nil {
				t.Fatal(err)
			}
			collection, err := app.FindCollectionByNameOrId("settings")
			if err != nil {
				t.Fatal(err)
			}
			settings := core.NewRecord(collection)
			settings.Set("user", user.Id)
			settings.Set("site_url", "http://127.0.0.1:8090")
			settings.Set("google_health_client_id", "test-client")
			settings.Set("google_health_client_secret", "test-secret")
			if err := app.Save(settings); err != nil {
				t.Fatal(err)
			}
			headers["Authorization"] = tokenFor(t, app, testUserEmail)
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
			location := res.Header.Get("Location")
			got, err := url.Parse(location)
			if err != nil {
				t.Fatal(err)
			}
			if got.Scheme != "https" || got.Host != "accounts.google.com" ||
				got.Path != "/o/oauth2/v2/auth" {
				t.Fatalf("authorization URL = %q", location)
			}
			query := got.Query()
			if query.Get("client_id") != "test-client" {
				t.Errorf("client_id = %q", query.Get("client_id"))
			}
			if query.Get("redirect_uri") !=
				"http://127.0.0.1:8090/auth/google-health/callback" {
				t.Errorf("redirect_uri = %q", query.Get("redirect_uri"))
			}
			if query.Get("access_type") != "offline" {
				t.Errorf("access_type = %q, want offline", query.Get("access_type"))
			}
			if query.Get("prompt") != "consent" {
				t.Errorf("prompt = %q, want consent", query.Get("prompt"))
			}
			if query.Get("code_challenge_method") != "S256" ||
				query.Get("code_challenge") == "" {
				t.Errorf(
					"PKCE = method:%q challenge:%q",
					query.Get("code_challenge_method"),
					query.Get("code_challenge"),
				)
			}
			if query.Get("scope") !=
				ingest.GoogleHealthSleepReadonlyScope {
				t.Errorf("scope = %q", query.Get("scope"))
			}

			userID, verifier, err := verifyState(app, query.Get("state"))
			if err != nil {
				t.Fatalf("verify state: %v", err)
			}
			user, err := app.FindAuthRecordByEmail("users", testUserEmail)
			if err != nil {
				t.Fatal(err)
			}
			if userID != user.Id || verifier == "" {
				t.Errorf("state payload = user:%q verifier:%q", userID, verifier)
			}
		},
		Headers: headers,
	}).Test(t)
}

func TestGoogleHealthSleepScopeGranted(t *testing.T) {
	t.Parallel()

	withScope := (&oauth2.Token{AccessToken: "token"}).WithExtra(map[string]any{
		"scope": "openid " + ingest.GoogleHealthSleepReadonlyScope,
	})
	withoutScope := (&oauth2.Token{AccessToken: "token"}).WithExtra(map[string]any{
		"scope": "openid profile",
	})
	if !googleHealthSleepScopeGranted(withScope) {
		t.Fatal("sleep scope was not recognized")
	}
	if googleHealthSleepScopeGranted(withoutScope) {
		t.Fatal("token without sleep scope was accepted")
	}
	if googleHealthSleepScopeGranted(nil) {
		t.Fatal("nil token was accepted")
	}
}

func TestGoogleHealthAuthorize_UsesEnvironmentCredentials(t *testing.T) {
	t.Setenv("GOOGLE_HEALTH_CLIENT_ID", "environment-client")
	t.Setenv("GOOGLE_HEALTH_CLIENT_SECRET", "environment-secret")

	headers := map[string]string{}
	(&tests.ApiScenario{
		Name:           "instance credentials avoid per-user setup",
		Method:         http.MethodGet,
		URL:            "/auth/google-health",
		ExpectedStatus: http.StatusTemporaryRedirect,
		TestAppFactory: setupApp,
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			registerGoogleHealthAuthRoutes(e, app)
			user, err := app.FindAuthRecordByEmail("users", testUserEmail)
			if err != nil {
				t.Fatal(err)
			}
			collection, err := app.FindCollectionByNameOrId("settings")
			if err != nil {
				t.Fatal(err)
			}
			settings := core.NewRecord(collection)
			settings.Set("user", user.Id)
			settings.Set("site_url", "https://meridian.example.com")
			if err := app.Save(settings); err != nil {
				t.Fatal(err)
			}
			headers["Authorization"] = tokenFor(t, app, testUserEmail)
		},
		AfterTestFunc: func(t testing.TB, _ *tests.TestApp, res *http.Response) {
			location := res.Header.Get("Location")
			if !strings.Contains(location, "client_id=environment-client") {
				t.Errorf("authorization location = %q", location)
			}
			if !strings.Contains(
				location,
				url.QueryEscape("https://meridian.example.com/auth/google-health/callback"),
			) {
				t.Errorf("authorization location missing configured callback: %q", location)
			}
		},
		Headers: headers,
	}).Test(t)
}

func TestVerifyState_RejectsTampering(t *testing.T) {
	app := setupApp(t)
	defer app.Cleanup()

	state := signState(app, "user-id", "nonce", "verifier")
	if strings.Contains(state, "verifier") {
		t.Fatal("PKCE verifier leaked into browser-visible state")
	}
	if _, _, err := verifyState(app, state+"tampered"); err == nil {
		t.Fatal("tampered state was accepted")
	}
	userID, verifier, err := verifyState(app, state)
	if err != nil || userID != "user-id" || verifier != "verifier" {
		t.Fatalf("valid state = user:%q verifier:%q err:%v", userID, verifier, err)
	}
	if _, _, err := verifyState(app, state); !errors.Is(err, errInvalidState) {
		t.Fatalf("reused state error = %v, want %v", err, errInvalidState)
	}
}
