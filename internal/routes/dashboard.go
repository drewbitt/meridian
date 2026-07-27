package routes

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/drewbitt/meridian/internal/services"
	"github.com/drewbitt/meridian/internal/templates"
	"github.com/pocketbase/pocketbase/core"
)

func registerDashboardRoutes(se *core.ServeEvent, app core.App) {
	// Full page dashboard.
	se.Router.GET("/", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/")
		}

		schedule, debt, err := loadTodayData(app, userID)
		if err != nil {
			schedule = engine.Schedule{}
			debt = engine.SleepDebt{}
		}

		resolvedHabits := loadHabitsForDashboard(app, userID, schedule)
		return render(re, templates.Dashboard(schedule, debt, resolvedHabits))
	})

}

func loadTodayData(app core.App, userID string) (engine.Schedule, engine.SleepDebt, error) {
	loc := services.UserLocation(app, userID)
	today := time.Now().In(loc).Format("2006-01-02")

	// Try loading cached schedule.
	scheduleRec, err := app.FindFirstRecordByFilter("energy_schedules",
		"user = {:user} && date = {:date}",
		map[string]any{"user": userID, "date": today},
	)
	if err == nil && scheduleRec != nil {
		var points []engine.EnergyPoint
		raw := scheduleRec.Get("schedule_json")
		data, err := json.Marshal(raw)
		if err == nil {
			err = json.Unmarshal(data, &points)
		}
		if err == nil && len(points) > 0 {
			wakeTime := scheduleRec.GetDateTime("wake_time").Time()
			schedule := engine.ClassifyZones(points, wakeTime)
			// Still need fresh debt calculation (debt isn't cached).
			debt := services.ComputeUserDebt(app, userID)
			return schedule, debt, nil
		}
		// Cached data corrupt or empty — fall through to recompute.
	}

	// No cached schedule — compute on the fly.
	schedule, _, debt, err := services.ComputeUserSchedule(app, userID)
	return schedule, debt, err
}
