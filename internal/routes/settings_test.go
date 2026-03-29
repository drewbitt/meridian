package routes

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/drewbitt/circadian/internal/schema"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

const testUserEmail = "test@example.com"

func setupApp(t testing.TB) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.EnsureCollections(app); err != nil {
		t.Fatal(err)
	}
	return app
}

func tokenFor(t testing.TB, app *tests.TestApp, email string) string {
	t.Helper()
	user, err := app.FindAuthRecordByEmail("users", email)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := user.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func hcMultipart(t testing.TB) (body *bytes.Buffer, contentType string) {
	t.Helper()
	payload := `{"sleepSessions":[{
		"startTime":"2024-01-15T23:00:00Z","endTime":"2024-01-16T07:00:00Z",
		"stages":[
			{"startTime":"2024-01-15T23:00:00Z","endTime":"2024-01-16T01:00:00Z","stage":4},
			{"startTime":"2024-01-16T01:00:00Z","endTime":"2024-01-16T03:00:00Z","stage":5},
			{"startTime":"2024-01-16T03:00:00Z","endTime":"2024-01-16T05:00:00Z","stage":6},
			{"startTime":"2024-01-16T05:00:00Z","endTime":"2024-01-16T07:00:00Z","stage":4}
		]
	}]}`

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	writeField(t, w, "source", "healthconnect")
	fw, err := w.CreateFormFile("file", "export.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func writeField(t testing.TB, w *multipart.Writer, name, value string) {
	t.Helper()
	fw, err := w.CreateFormField(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
}

func expectLocation(t testing.TB, res *http.Response, substr string) {
	t.Helper()
	loc := res.Header.Get("Location")
	if !strings.Contains(loc, substr) {
		t.Errorf("Location header %q does not contain %q", loc, substr)
	}
}

// TestSettingsImport_Redirect verifies that the HTML import endpoint redirects
// back to settings with an imported count.
func TestSettingsImport_Redirect(t *testing.T) {
	body, ct := hcMultipart(t)
	headers := map[string]string{"Content-Type": ct}

	(&tests.ApiScenario{
		Name:           "import redirects with count",
		Method:         http.MethodPost,
		URL:            "/settings/import",
		Body:           body,
		ExpectedStatus: 303,
		TestAppFactory: setupApp,
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			registerSettingsRoutes(e, app)
			headers["Authorization"] = tokenFor(t, app, testUserEmail)
		},
		AfterTestFunc: func(_ testing.TB, _ *tests.TestApp, res *http.Response) {
			expectLocation(t, res, "/settings?imported=1")
		},
		Headers: headers,
	}).Test(t)
}

func TestSettingsImport_NoFile(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	writeField(t, w, "source", "healthconnect")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	headers := map[string]string{"Content-Type": w.FormDataContentType()}

	(&tests.ApiScenario{
		Name:           "no file redirects with error",
		Method:         http.MethodPost,
		URL:            "/settings/import",
		Body:           &buf,
		ExpectedStatus: 303,
		TestAppFactory: setupApp,
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			registerSettingsRoutes(e, app)
			headers["Authorization"] = tokenFor(t, app, testUserEmail)
		},
		AfterTestFunc: func(_ testing.TB, _ *tests.TestApp, res *http.Response) {
			expectLocation(t, res, "/settings?import_error=")
		},
		Headers: headers,
	}).Test(t)
}

func TestSettingsImport_Unauthenticated(t *testing.T) {
	body, ct := hcMultipart(t)

	(&tests.ApiScenario{
		Name:           "unauthenticated redirects to login",
		Method:         http.MethodPost,
		URL:            "/settings/import",
		Body:           body,
		ExpectedStatus: 307,
		TestAppFactory: setupApp,
		BeforeTestFunc: func(_ testing.TB, _ *tests.TestApp, e *core.ServeEvent) {
			registerSettingsRoutes(e, setupApp(t))
		},
		AfterTestFunc: func(_ testing.TB, _ *tests.TestApp, res *http.Response) {
			expectLocation(t, res, "/login")
		},
		Headers: map[string]string{"Content-Type": ct},
	}).Test(t)
}

// TestAPIImport_ReturnsJSON confirms the JSON API still returns JSON.
func TestAPIImport_ReturnsJSON(t *testing.T) {
	body, ct := hcMultipart(t)
	headers := map[string]string{"Content-Type": ct}

	(&tests.ApiScenario{
		Name:            "api import returns JSON not redirect",
		Method:          http.MethodPost,
		URL:             "/api/import?source=healthconnect",
		Body:            body,
		ExpectedStatus:  200,
		ExpectedContent: []string{`"imported"`, `"total"`},
		TestAppFactory:  setupApp,
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			registerAPIRoutes(e, app)
			headers["Authorization"] = tokenFor(t, app, testUserEmail)
		},
		Headers: headers,
	}).Test(t)
}
