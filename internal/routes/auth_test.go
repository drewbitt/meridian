package routes

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func setupEmptyApp(t testing.TB) *tests.TestApp {
	t.Helper()
	app := setupApp(t)
	users, err := app.FindAllRecords("users")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if err := app.Delete(user); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func TestRegisterCreatesFirstUserAndSession(t *testing.T) {
	t.Setenv("ALLOW_REGISTRATION", "")
	form := url.Values{
		"email":            {"owner@example.com"},
		"password":         {"correct-horse"},
		"password_confirm": {"correct-horse"},
	}

	(&tests.ApiScenario{
		Name:           "first account creates a session",
		Method:         http.MethodPost,
		URL:            "/register",
		Body:           strings.NewReader(form.Encode()),
		Headers:        map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		ExpectedStatus: http.StatusSeeOther,
		TestAppFactory: setupEmptyApp,
		BeforeTestFunc: func(_ testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			registerAuthRoutes(e, app)
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
			expectLocation(t, res, "/settings?welcome=1")
			if registrationEnabled(app) {
				t.Error("first-user registration remained open")
			}
			assertSessionCookie(t, res)
		},
	}).Test(t)
}

func TestLoginIssuesServerOnlySessionCookie(t *testing.T) {
	form := url.Values{
		"identity": {"test@example.com"},
		"password": {"1234567890"},
		"redirect": {"/settings"},
	}

	(&tests.ApiScenario{
		Name:           "valid login",
		Method:         http.MethodPost,
		URL:            "/login",
		Body:           strings.NewReader(form.Encode()),
		Headers:        map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		ExpectedStatus: http.StatusSeeOther,
		TestAppFactory: setupApp,
		BeforeTestFunc: func(_ testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			registerAuthRoutes(e, app)
		},
		AfterTestFunc: func(t testing.TB, _ *tests.TestApp, res *http.Response) {
			expectLocation(t, res, "/settings")
			assertSessionCookie(t, res)
		},
	}).Test(t)
}

func assertSessionCookie(t testing.TB, res *http.Response) {
	t.Helper()
	for _, cookie := range res.Cookies() {
		if cookie.Name != "pb_auth" {
			continue
		}
		if cookie.Value == "" {
			t.Error("pb_auth cookie is empty")
		}
		if !cookie.HttpOnly {
			t.Error("pb_auth cookie is accessible to JavaScript")
		}
		if cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("pb_auth SameSite = %v, want Lax", cookie.SameSite)
		}
		return
	}
	t.Error("pb_auth cookie was not set")
}
