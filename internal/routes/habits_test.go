package routes

import (
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

const otherUserEmail = "other@example.com"

func setupHabitOwnership(t testing.TB, app *tests.TestApp, e *core.ServeEvent) string {
	t.Helper()
	registerHabitRoutes(e, app)

	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	other := core.NewRecord(users)
	other.Set("email", otherUserEmail)
	other.Set("password", "1234567890")
	if err := app.Save(other); err != nil {
		t.Fatal(err)
	}

	owner, err := app.FindAuthRecordByEmail("users", testUserEmail)
	if err != nil {
		t.Fatal(err)
	}
	habits, err := app.FindCollectionByNameOrId("habits")
	if err != nil {
		t.Fatal(err)
	}
	habit := core.NewRecord(habits)
	habit.Id = "ownerhabit00001"
	habit.Set("user", owner.Id)
	habit.Set("name", "Owner habit")
	habit.Set("anchor", "morning_wake")
	if err := app.Save(habit); err != nil {
		t.Fatal(err)
	}

	return tokenFor(t, app, otherUserEmail)
}

func TestHabitEditRejectsNonOwner(t *testing.T) {
	headers := map[string]string{}
	(&tests.ApiScenario{
		Name:            "non-owner cannot view edit page",
		Method:          http.MethodGet,
		URL:             "/habits/ownerhabit00001/edit",
		ExpectedStatus:  http.StatusNotFound,
		ExpectedContent: []string{"Habit not found"},
		TestAppFactory:  setupApp,
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			headers["Authorization"] = setupHabitOwnership(t, app, e)
		},
		Headers: headers,
	}).Test(t)
}

func TestHabitDeleteRejectsNonOwner(t *testing.T) {
	headers := map[string]string{}
	(&tests.ApiScenario{
		Name:            "non-owner cannot delete habit",
		Method:          http.MethodPost,
		URL:             "/habits/ownerhabit00001/delete",
		ExpectedStatus:  http.StatusNotFound,
		ExpectedContent: []string{"Habit not found"},
		TestAppFactory:  setupApp,
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			headers["Authorization"] = setupHabitOwnership(t, app, e)
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, _ *http.Response) {
			if _, err := app.FindRecordById("habits", "ownerhabit00001"); err != nil {
				t.Errorf("owner habit was deleted: %v", err)
			}
		},
		Headers: headers,
	}).Test(t)
}
