package routes

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/drewbitt/meridian/internal/services"
	"github.com/drewbitt/meridian/internal/templates"
	"github.com/pocketbase/pocketbase/core"
)

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

		// Reject entries in the future (sleep_start can't be after right now).
		if sleepStart.After(time.Now().Add(1 * time.Hour)) {
			return re.BadRequestError("Sleep time cannot be in the future", nil)
		}

		sleepDate := ingest.SleepNightDate(sleepStart)

		// Use upsert to prevent duplicate records for the same night + source.
		rec := ingest.SleepRecord{
			Date:            sleepDate,
			SleepStart:      sleepStart,
			SleepEnd:        sleepEnd,
			DurationMinutes: duration,
			Source:          "manual",
		}
		if _, err := services.UpsertSleepRecord(app, userID, rec); err != nil {
			return re.InternalServerError("Failed to save", err)
		}

		// Recompute schedule with the new sleep data.
		if _, err := services.RefreshScheduleIfNeeded(app, userID); err != nil {
			slog.Error("failed to refresh schedule after manual entry", "user_id", userID, "error", err)
		}

		return re.Redirect(http.StatusSeeOther, "/")
	})
}
