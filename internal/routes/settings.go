package routes

import (
	"bytes"
	"net/http"

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
		var buf bytes.Buffer
		templates.Settings(settings).Render(re.Request.Context(), &buf)
		re.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
		re.Response.Write(buf.Bytes())
		return nil
	})

	se.Router.POST("/settings", func(re *core.RequestEvent) error {
		info, _ := re.RequestInfo()
		if info.Auth == nil {
			return re.Redirect(http.StatusTemporaryRedirect, "/login?redirect=/settings")
		}

		data := struct {
			SleepNeedHours       float64 `json:"sleep_need_hours" form:"sleep_need_hours"`
			NtfyTopic            string  `json:"ntfy_topic" form:"ntfy_topic"`
			NtfyServer           string  `json:"ntfy_server" form:"ntfy_server"`
			NtfyAccessToken      string  `json:"ntfy_access_token" form:"ntfy_access_token"`
			SiteURL              string  `json:"site_url" form:"site_url"`
			FitbitClientID       string  `json:"fitbit_client_id" form:"fitbit_client_id"`
			FitbitClientSecret   string  `json:"fitbit_client_secret" form:"fitbit_client_secret"`
			NotificationsEnabled bool    `json:"notifications_enabled" form:"notifications_enabled"`
		}{}
		if err := re.BindBody(&data); err != nil {
			return re.BadRequestError("Invalid data", err)
		}

		// Find or create settings record.
		settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": info.Auth.Id})
		if err != nil {
			collection, err := app.FindCollectionByNameOrId("settings")
			if err != nil {
				return re.InternalServerError("", err)
			}
			settings = core.NewRecord(collection)
			settings.Set("user", info.Auth.Id)
		}

		if data.SleepNeedHours > 0 {
			settings.Set("sleep_need_hours", data.SleepNeedHours)
		}
		settings.Set("ntfy_topic", data.NtfyTopic)
		if data.NtfyServer != "" {
			settings.Set("ntfy_server", data.NtfyServer)
		}
		settings.Set("ntfy_access_token", data.NtfyAccessToken)
		settings.Set("site_url", data.SiteURL)
		if data.FitbitClientID != "" {
			settings.Set("fitbit_client_id", data.FitbitClientID)
		}
		if data.FitbitClientSecret != "" {
			settings.Set("fitbit_client_secret", data.FitbitClientSecret)
		}
		settings.Set("notifications_enabled", data.NotificationsEnabled)

		if err := app.Save(settings); err != nil {
			return re.InternalServerError("Failed to save settings", err)
		}

		return re.Redirect(http.StatusSeeOther, "/settings")
	})
}
