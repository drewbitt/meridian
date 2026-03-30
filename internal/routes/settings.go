package routes

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/drewbitt/meridian/internal/services"
	"github.com/drewbitt/meridian/internal/templates"
	"github.com/pocketbase/pocketbase/core"
)

func registerSettingsRoutes(se *core.ServeEvent, app core.App) {
	se.Router.GET("/settings", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		settings, _ := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
		q := re.Request.URL.Query()
		saved := q.Get("saved") == "1"
		importedCount := q.Get("imported")
		importError := q.Get("import_error")
		fitbitError := q.Get("fitbit_error")
		fitbitStatus := q.Get("fitbit") // "connected", "disconnected", "synced"
		return render(re, templates.Settings(settings, saved, importedCount, importError, fitbitError, fitbitStatus))
	})

	se.Router.POST("/settings", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		if err := re.Request.ParseForm(); err != nil {
			return re.BadRequestError("Invalid data", err)
		}
		form := re.Request.PostForm

		settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
		if err != nil {
			collection, err := app.FindCollectionByNameOrId("settings")
			if err != nil {
				return re.InternalServerError("", err)
			}
			settings = core.NewRecord(collection)
			settings.Set("user", userID)
		}

		if v, err := strconv.ParseFloat(form.Get("sleep_need_hours"), 64); err == nil && v >= 4 && v <= 12 {
			settings.Set("sleep_need_hours", v)
		}
		settings.Set("ntfy_topic", form.Get("ntfy_topic"))
		if v := form.Get("ntfy_server"); v != "" {
			settings.Set("ntfy_server", v)
		}
		settings.Set("ntfy_access_token", form.Get("ntfy_access_token"))
		settings.Set("site_url", form.Get("site_url"))
		if tz := form.Get("timezone"); tz != "" {
			if _, err := time.LoadLocation(tz); err == nil {
				settings.Set("timezone", tz)
			}
		} else if loc := locationFromCookie(re); loc != nil {
			// Auto-populate from browser cookie if the user didn't set one explicitly.
			settings.Set("timezone", loc.String())
		} else {
			settings.Set("timezone", "")
		}
		if v := form.Get("fitbit_client_id"); v != "" {
			settings.Set("fitbit_client_id", v)
		}
		if v := form.Get("fitbit_client_secret"); v != "" {
			settings.Set("fitbit_client_secret", v)
		}
		settings.Set("notifications_enabled", form.Get("notifications_enabled") == "on")

		if err := app.Save(settings); err != nil {
			return re.InternalServerError("Failed to save settings", err)
		}

		return re.Redirect(http.StatusSeeOther, "/settings?saved=1")
	})

	se.Router.POST("/settings/test-notification", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.JSON(401, map[string]string{"error": "not authenticated"})
		}
		_ = userID

		if err := re.Request.ParseForm(); err != nil {
			return re.JSON(400, map[string]string{"error": "invalid data"})
		}
		form := re.Request.PostForm

		topic := form.Get("ntfy_topic")
		if topic == "" {
			return re.JSON(400, map[string]string{"error": "ntfy topic is required"})
		}

		if err := services.SendNotification(services.Notification{
			Server:      form.Get("ntfy_server"),
			Topic:       topic,
			AccessToken: form.Get("ntfy_access_token"),
			Title:       "Test Notification",
			Message:     "This is a test notification from Meridian!",
			Priority:    3,
			Tags:        []string{"white_check_mark", "bell"},
		}); err != nil {
			return re.JSON(500, map[string]string{"error": err.Error()})
		}

		return re.JSON(200, map[string]bool{"success": true})
	})

	// File import (HTML form version — redirects back to settings with feedback).
	se.Router.POST("/settings/import", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		re.Request.Body = http.MaxBytesReader(re.Response, re.Request.Body, maxUploadSize)

		if err := re.Request.ParseMultipartForm(maxUploadSize); err != nil {
			return re.Redirect(http.StatusSeeOther, "/settings?import_error=File+too+large")
		}

		source := re.Request.FormValue("source")
		if source == "" {
			return re.Redirect(http.StatusSeeOther, "/settings?import_error=No+source+selected")
		}

		file, header, err := re.Request.FormFile("file")
		if err != nil {
			return re.Redirect(http.StatusSeeOther, "/settings?import_error=No+file+selected")
		}
		defer file.Close()

		records, err := parseImportSource(file, header.Filename, source)
		if err != nil {
			return re.Redirect(http.StatusSeeOther, "/settings?import_error=Failed+to+parse+file")
		}

		imported := 0
		for _, rec := range records {
			if _, err := services.UpsertSleepRecord(app, userID, rec); err == nil {
				imported++
			}
		}

		return re.Redirect(http.StatusSeeOther, fmt.Sprintf("/settings?imported=%d", imported))
	})
}
