package routes

import (
	"bytes"
	"net/http"
	"strconv"

	"github.com/drewbitt/circadian/internal/templates"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

func registerSettingsRoutes(se *core.ServeEvent, app *pocketbase.PocketBase) {
	se.Router.GET("/settings", func(re *core.RequestEvent) error {
		info, _ := re.RequestInfo()
		if info.Auth == nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		settings, _ := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": info.Auth.Id})
		saved := re.Request.URL.Query().Get("saved") == "1"
		var buf bytes.Buffer
		_ = templates.Settings(settings, saved).Render(re.Request.Context(), &buf)
		re.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = re.Response.Write(buf.Bytes())
		return nil
	})

	se.Router.POST("/settings", func(re *core.RequestEvent) error {
		info, _ := re.RequestInfo()
		if info.Auth == nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		if err := re.Request.ParseForm(); err != nil {
			return re.BadRequestError("Invalid data", err)
		}
		form := re.Request.PostForm

		settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": info.Auth.Id})
		if err != nil {
			collection, err := app.FindCollectionByNameOrId("settings")
			if err != nil {
				return re.InternalServerError("", err)
			}
			settings = core.NewRecord(collection)
			settings.Set("user", info.Auth.Id)
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
}
