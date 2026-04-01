package routes

import (
	"net/http"
	"strconv"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/drewbitt/meridian/internal/services"
	"github.com/drewbitt/meridian/internal/templates"
	"github.com/pocketbase/pocketbase/core"
)

func registerHabitRoutes(se *core.ServeEvent, app core.App) {
	se.Router.GET("/habits", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/habits")
		}

		habits, _ := app.FindRecordsByFilter("habits",
			"user = {:user}", "name", 0, 0,
			map[string]any{"user": userID},
		)

		// Resolve habit times against today's schedule.
		schedule, _, _ := loadTodayData(app, userID)
		loc := services.UserLocation(app, userID)
		resolved := services.ResolveAllHabits(app, userID, schedule, loc)

		presets := services.Presets()
		activePresets := services.ActivePresetKeys(habits)

		q := re.Request.URL.Query()
		return render(re, templates.Habits(habits, resolved, presets, activePresets, q.Get("saved") == "1", q.Get("deleted") == "1"))
	})

	// Enable a preset habit with one click.
	se.Router.POST("/habits/preset", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/habits")
		}

		if err := re.Request.ParseForm(); err != nil {
			return re.BadRequestError("Invalid data", err)
		}

		key := re.Request.PostForm.Get("key")
		preset := services.PresetByKey(key)
		if preset == nil {
			return re.BadRequestError("Unknown preset", nil)
		}

		// Skip if already enabled.
		if existing, _ := app.FindFirstRecordByFilter("habits",
			"user = {:user} && name = {:name}",
			map[string]any{"user": userID, "name": preset.Name},
		); existing != nil {
			return re.Redirect(http.StatusSeeOther, "/habits")
		}

		collection, err := app.FindCollectionByNameOrId("habits")
		if err != nil {
			return re.InternalServerError("", err)
		}

		record := core.NewRecord(collection)
		record.Set("user", userID)
		record.Set("name", preset.Name)
		record.Set("anchor", preset.Anchor)
		record.Set("offset_minutes", preset.OffsetMinutes)
		record.Set("notify", true)
		record.Set("enabled", true)

		if err := app.Save(record); err != nil {
			return re.InternalServerError("Failed to save habit", err)
		}

		return re.Redirect(http.StatusSeeOther, "/habits?saved=1")
	})

	se.Router.POST("/habits", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/habits")
		}

		if err := re.Request.ParseForm(); err != nil {
			return re.BadRequestError("Invalid data", err)
		}

		collection, err := app.FindCollectionByNameOrId("habits")
		if err != nil {
			return re.InternalServerError("", err)
		}

		record := core.NewRecord(collection)
		record.Set("user", userID)
		applyHabitForm(record, re)

		if err := app.Save(record); err != nil {
			return re.InternalServerError("Failed to save habit", err)
		}

		return re.Redirect(http.StatusSeeOther, "/habits?saved=1")
	})

	se.Router.GET("/habits/{id}/edit", func(re *core.RequestEvent) error {
		if _, err := authedUserID(re); err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/habits")
		}

		record, err := app.FindRecordById("habits", re.Request.PathValue("id"))
		if err != nil {
			return re.NotFoundError("Habit not found", nil)
		}

		return render(re, templates.HabitEdit(record))
	})

	se.Router.POST("/habits/{id}/edit", func(re *core.RequestEvent) error {
		if _, err := authedUserID(re); err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/habits")
		}

		record, err := app.FindRecordById("habits", re.Request.PathValue("id"))
		if err != nil {
			return re.NotFoundError("Habit not found", nil)
		}

		if err := re.Request.ParseForm(); err != nil {
			return re.BadRequestError("Invalid data", err)
		}

		applyHabitForm(record, re)

		if err := app.Save(record); err != nil {
			return re.InternalServerError("Failed to save habit", err)
		}

		return re.Redirect(http.StatusSeeOther, "/habits?saved=1")
	})

	se.Router.POST("/habits/{id}/delete", func(re *core.RequestEvent) error {
		if _, err := authedUserID(re); err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/habits")
		}

		record, err := app.FindRecordById("habits", re.Request.PathValue("id"))
		if err != nil {
			return re.NotFoundError("Habit not found", nil)
		}

		if err := app.Delete(record); err != nil {
			return re.InternalServerError("Failed to delete habit", err)
		}

		return re.Redirect(http.StatusSeeOther, "/habits?deleted=1")
	})
}

func applyHabitForm(record *core.Record, re *core.RequestEvent) {
	form := re.Request.PostForm
	record.Set("name", form.Get("name"))
	record.Set("anchor", form.Get("anchor"))
	if v, err := strconv.Atoi(form.Get("offset_minutes")); err == nil {
		record.Set("offset_minutes", v)
	} else {
		record.Set("offset_minutes", 0)
	}
	record.Set("custom_time", form.Get("custom_time"))
	record.Set("notify", form.Get("notify") == "on")
	record.Set("enabled", form.Get("enabled") == "on")
}

// loadHabitsForDashboard resolves all enabled habits for the dashboard display.
func loadHabitsForDashboard(app core.App, userID string, schedule engine.Schedule) []services.ResolvedHabit {
	loc := services.UserLocation(app, userID)
	return services.ResolveAllHabits(app, userID, schedule, loc)
}
