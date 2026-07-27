package routes

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestSleepEntryRejectsFutureWakeTime(t *testing.T) {
	start := time.Now().Add(-8 * time.Hour).Format("2006-01-02T15:04")
	end := time.Now().Add(2 * time.Hour).Format("2006-01-02T15:04")
	form := url.Values{
		"sleep_start": {start},
		"sleep_end":   {end},
		"tz":          {"America/New_York"},
	}
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

	(&tests.ApiScenario{
		Name:            "future wake time",
		Method:          http.MethodPost,
		URL:             "/sleep",
		Body:            strings.NewReader(form.Encode()),
		Headers:         headers,
		ExpectedStatus:  http.StatusBadRequest,
		ExpectedContent: []string{"Wake time cannot be in the future"},
		TestAppFactory:  setupApp,
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			registerSleepRoutes(e, app)
			headers["Authorization"] = tokenFor(t, app, testUserEmail)
		},
	}).Test(t)
}
