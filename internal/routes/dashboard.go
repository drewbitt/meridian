package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/drewbitt/circadian/internal/engine"
	"github.com/drewbitt/circadian/internal/templates"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/starfederation/datastar-go/datastar"
)

func registerDashboardRoutes(se *core.ServeEvent, app *pocketbase.PocketBase) {
	// Full page dashboard.
	se.Router.GET("/", func(re *core.RequestEvent) error {
		info, _ := re.RequestInfo()
		if info.Auth == nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/")
		}

		userID := info.Auth.Id
		schedule, debt, err := loadTodayData(app, userID)
		if err != nil {
			schedule = engine.Schedule{}
			debt = engine.SleepDebt{}
		}

		var buf bytes.Buffer
		if err := templates.Dashboard(schedule, debt).Render(re.Request.Context(), &buf); err != nil {
			return re.InternalServerError("render failed", err)
		}
		re.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = re.Response.Write(buf.Bytes())
		return nil
	})

	// SSE endpoint for live dashboard updates.
	se.Router.GET("/sse/dashboard", func(re *core.RequestEvent) error {
		info, _ := re.RequestInfo()
		if info.Auth == nil {
			return re.UnauthorizedError("", nil)
		}

		userID := info.Auth.Id
		sse := datastar.NewSSE(re.Response, re.Request)

		schedule, debt, err := loadTodayData(app, userID)
		if err != nil {
			_ = sse.PatchElements(`<div id="error">Failed to load data</div>`)
			return nil
		}

		// Send chart data as a script execution.
		chartData, _ := json.Marshal(schedule.Points)
		_ = sse.ExecuteScript(fmt.Sprintf(`window.updateEnergyChart(%s)`, chartData))

		// Patch the debt card.
		var buf bytes.Buffer
		_ = templates.DebtCard(debt).Render(re.Request.Context(), &buf)
		_ = sse.PatchElements(buf.String())

		buf.Reset()
		_ = templates.TodaySchedule(schedule).Render(re.Request.Context(), &buf)
		_ = sse.PatchElements(buf.String())

		return nil
	})

}

func loadTodayData(app *pocketbase.PocketBase, userID string) (engine.Schedule, engine.SleepDebt, error) {
	today := time.Now().Format("2006-01-02")

	// Try loading cached schedule.
	scheduleRec, err := app.FindFirstRecordByFilter("energy_schedules",
		"user = {:user} && date = {:date}",
		map[string]any{"user": userID, "date": today},
	)
	if err == nil && scheduleRec != nil {
		var points []engine.EnergyPoint
		raw := scheduleRec.Get("schedule_json")
		if data, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(data, &points)
		}
		wakeTime := scheduleRec.GetDateTime("wake_time").Time()
		schedule := engine.ClassifyZones(points, wakeTime)

		// Load debt.
		debt := loadDebt(app, userID)
		return schedule, debt, nil
	}

	// No cached schedule — compute on the fly.
	return computeSchedule(app, userID)
}

func loadDebt(app *pocketbase.PocketBase, userID string) engine.SleepDebt {
	fourteenDaysAgo := time.Now().AddDate(0, 0, -14).Format("2006-01-02 00:00:00")
	records, err := app.FindRecordsByFilter(
		"sleep_records",
		"user = {:user} && date >= {:since}",
		"-date", 0, 0,
		map[string]any{"user": userID, "since": fourteenDaysAgo},
	)
	if err != nil {
		return engine.SleepDebt{}
	}

	// Load sleep need from settings.
	sleepNeed := 8.0
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err == nil {
		if sn := settings.GetFloat("sleep_need_hours"); sn > 0 {
			sleepNeed = sn
		}
	}

	var engineRecords []engine.SleepRecord
	for _, r := range records {
		engineRecords = append(engineRecords, engine.SleepRecord{
			Date:            r.GetDateTime("date").Time(),
			DurationMinutes: r.GetInt("duration_minutes"),
		})
	}

	return engine.CalculateSleepDebt(engineRecords, sleepNeed, time.Now())
}

func computeSchedule(app *pocketbase.PocketBase, userID string) (engine.Schedule, engine.SleepDebt, error) {
	fourteenDaysAgo := time.Now().AddDate(0, 0, -14).Format("2006-01-02 00:00:00")
	records, err := app.FindRecordsByFilter(
		"sleep_records",
		"user = {:user} && date >= {:since}",
		"-date", 0, 0,
		map[string]any{"user": userID, "since": fourteenDaysAgo},
	)
	if err != nil {
		return engine.Schedule{}, engine.SleepDebt{}, err
	}

	sleepNeed := 8.0
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err == nil {
		if sn := settings.GetFloat("sleep_need_hours"); sn > 0 {
			sleepNeed = sn
		}
	}

	var engineRecords []engine.SleepRecord
	var periods []engine.SleepPeriod
	for _, r := range records {
		engineRecords = append(engineRecords, engine.SleepRecord{
			Date:            r.GetDateTime("date").Time(),
			SleepStart:      r.GetDateTime("sleep_start").Time(),
			SleepEnd:        r.GetDateTime("sleep_end").Time(),
			DurationMinutes: r.GetInt("duration_minutes"),
		})
		periods = append(periods, engine.SleepPeriod{
			Start: r.GetDateTime("sleep_start").Time(),
			End:   r.GetDateTime("sleep_end").Time(),
		})
	}

	debt := engine.CalculateSleepDebt(engineRecords, sleepNeed, time.Now())

	now := time.Now()
	wakeTime := time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location())
	if len(periods) > 0 {
		latest := periods[0]
		for _, sp := range periods {
			if sp.End.After(latest.End) {
				latest = sp
			}
		}
		wakeTime = latest.End
	}

	points := engine.PredictEnergy(periods, wakeTime, wakeTime.Add(24*time.Hour))
	schedule := engine.ClassifyZones(points, wakeTime)

	return schedule, debt, nil
}
