package routes

import (
	"net/http"
	"time"

	"github.com/drewbitt/meridian/internal/templates"
	"github.com/pocketbase/pocketbase/core"
)

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func registerSleepRoutes(se *core.ServeEvent, app core.App) {
	// Manual sleep entry form.
	se.Router.GET("/sleep", func(re *core.RequestEvent) error {
		if _, err := authedUserID(re); err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/sleep")
		}

		return render(re, templates.SleepEntry())
	})

	// Submit manual sleep entry.
	se.Router.POST("/sleep", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/sleep")
		}

		data := struct {
			SleepStart string `json:"sleep_start" form:"sleep_start"`
			SleepEnd   string `json:"sleep_end" form:"sleep_end"`
		}{}
		if err := re.BindBody(&data); err != nil {
			return re.BadRequestError("Invalid data", err)
		}

		loc := userLocationFromForm(app, re)
		sleepStart, err := time.ParseInLocation("2006-01-02T15:04", data.SleepStart, loc)
		if err != nil {
			return re.BadRequestError("Invalid sleep start time", err)
		}
		sleepEnd, err := time.ParseInLocation("2006-01-02T15:04", data.SleepEnd, loc)
		if err != nil {
			return re.BadRequestError("Invalid sleep end time", err)
		}

		if !sleepEnd.After(sleepStart) {
			return re.BadRequestError("Wake time must be after sleep time", nil)
		}

		duration := int(sleepEnd.Sub(sleepStart).Minutes())
		if duration > 24*60 {
			return re.BadRequestError("Sleep duration cannot exceed 24 hours", nil)
		}
		sleepDate := dateOnly(sleepStart)

		collection, err := app.FindCollectionByNameOrId("sleep_records")
		if err != nil {
			return re.InternalServerError("", err)
		}

		record := core.NewRecord(collection)
		record.Set("user", userID)
		record.Set("date", sleepDate)
		record.Set("sleep_start", sleepStart)
		record.Set("sleep_end", sleepEnd)
		record.Set("source", "manual")
		record.Set("duration_minutes", duration)

		if err := app.Save(record); err != nil {
			return re.InternalServerError("Failed to save", err)
		}

		return re.Redirect(http.StatusSeeOther, "/")
	})
}
