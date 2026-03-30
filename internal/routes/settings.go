package routes

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/drewbitt/meridian/internal/ingest"
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
		fitbitConnected := q.Get("fitbit") == "connected"
		return render(re, templates.Settings(settings, saved, importedCount, importError, fitbitError, fitbitConnected))
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

		if v, err := strconv.ParseFloat(form.Get("sleep_need_hours"), 64); err == nil && v > 0 {
			settings.Set("sleep_need_hours", v)
		}
		settings.Set("ntfy_topic", form.Get("ntfy_topic"))
		if v := form.Get("ntfy_server"); v != "" {
			settings.Set("ntfy_server", v)
		}
		settings.Set("ntfy_access_token", form.Get("ntfy_access_token"))
		settings.Set("site_url", form.Get("site_url"))
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

		var records []ingest.SleepRecord

		switch source {
		case "healthconnect":
			records, err = ingest.ParseHealthConnect(io.LimitReader(file, maxUploadSize))
		case "applehealth":
			records, err = importFileToDisk(io.LimitReader(file, maxUploadSize), header.Filename, func(tmpPath string) ([]ingest.SleepRecord, error) {
				if strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
					return ingest.ParseAppleHealthZip(tmpPath)
				}
				f, ferr := os.Open(tmpPath) //nolint:gosec // tmpPath from our own CreateTemp
				if ferr != nil {
					return nil, ferr
				}
				defer f.Close()
				return ingest.ParseAppleHealthXML(f)
			})
		case "gadgetbridge":
			records, err = importFileToDisk(io.LimitReader(file, maxUploadSize), header.Filename, ingest.ParseGadgetbridge)
		default:
			return re.Redirect(http.StatusSeeOther, "/settings?import_error=Unknown+source")
		}

		if err != nil {
			return re.Redirect(http.StatusSeeOther, "/settings?import_error=Failed+to+parse+file")
		}

		collection, err := app.FindCollectionByNameOrId("sleep_records")
		if err != nil {
			return re.Redirect(http.StatusSeeOther, "/settings?import_error=Internal+error")
		}

		imported := 0
		for _, rec := range records {
			dateStr := rec.Date.Format("2006-01-02")
			existing, _ := app.FindFirstRecordByFilter("sleep_records",
				"user = {:user} && date = {:date} && source = {:source}",
				map[string]any{"user": userID, "date": dateStr, "source": rec.Source},
			)

			var record *core.Record
			if existing != nil {
				record = existing
			} else {
				record = core.NewRecord(collection)
				record.Set("user", userID)
			}

			record.Set("date", rec.Date)
			record.Set("sleep_start", rec.SleepStart)
			record.Set("sleep_end", rec.SleepEnd)
			record.Set("source", rec.Source)
			record.Set("duration_minutes", rec.DurationMinutes)
			record.Set("deep_minutes", rec.DeepMinutes)
			record.Set("rem_minutes", rec.REMMinutes)
			record.Set("light_minutes", rec.LightMinutes)
			record.Set("awake_minutes", rec.AwakeMinutes)

			if err := app.Save(record); err == nil {
				imported++
			}
		}

		return re.Redirect(http.StatusSeeOther, fmt.Sprintf("/settings?imported=%d", imported))
	})
}
