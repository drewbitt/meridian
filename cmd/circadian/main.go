package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/drewbitt/circadian/assets"
	"github.com/drewbitt/circadian/internal/ingest"
	"github.com/drewbitt/circadian/internal/routes"
	"github.com/drewbitt/circadian/internal/services"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"golang.org/x/oauth2"
)

func main() {
	app := pocketbase.New()

	// Ensure collections exist on first run.
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Id: "init-collections",
		Func: func(se *core.ServeEvent) error {
			ensureCollections(app)
			return se.Next()
		},
	})

	// Register routes.
	routes.Register(app)

	// Load auth from pb_auth cookie for server-rendered pages.
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Id: "cookie-auth",
		Func: func(se *core.ServeEvent) error {
			se.Router.BindFunc(func(re *core.RequestEvent) error {
				if re.Auth == nil {
					if cookie, err := re.Request.Cookie("pb_auth"); err == nil && cookie.Value != "" {
						if record, err := app.FindAuthRecordByToken(cookie.Value, core.TokenTypeAuth); err == nil && record != nil {
							re.Auth = record
						}
					}
				}
				return re.Next()
			})
			return se.Next()
		},
	})

	// Register static file serving for embedded assets.
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Id: "static-assets",
		Func: func(se *core.ServeEvent) error {
			se.Router.GET("/assets/{path...}", func(re *core.RequestEvent) error {
				path := re.Request.PathValue("path")
				re.Response.Header().Set("Cache-Control", "public, max-age=31536000")
				return re.FileFS(assets.FS(), path)
			})
			return se.Next()
		},
	})

	app.Cron().MustAdd("morning-schedule", "0 8 * * *", func() {
		runMorningJobForAllUsers(app)
	})

	app.Cron().MustAdd("fitbit-sync", "0 */4 * * *", func() {
		syncFitbitForAllUsers(app)
	})

	if err := app.Start(); err != nil {
		slog.Error("failed to start", "error", err)
	}
}

func ensureCollections(app *pocketbase.PocketBase) {
	// sleep_records
	if _, err := app.FindCollectionByNameOrId("sleep_records"); err != nil {
		c := core.NewBaseCollection("sleep_records", "")
		authRule := "@request.auth.id != '' && user = @request.auth.id"
		c.ListRule = &authRule
		c.ViewRule = &authRule
		c.CreateRule = &authRule
		c.UpdateRule = &authRule
		c.DeleteRule = &authRule
		c.Fields.Add(
			&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1},
			&core.DateField{Name: "date", Required: true},
			&core.DateField{Name: "sleep_start", Required: true},
			&core.DateField{Name: "sleep_end", Required: true},
			&core.TextField{Name: "source", Required: true},
			&core.NumberField{Name: "duration_minutes", Required: true},
			&core.NumberField{Name: "deep_minutes"},
			&core.NumberField{Name: "rem_minutes"},
			&core.NumberField{Name: "light_minutes"},
			&core.NumberField{Name: "awake_minutes"},
		)
		if err := app.Save(c); err != nil {
			slog.Error("failed to create sleep_records collection", "error", err)
		}
	}

	// energy_schedules
	if _, err := app.FindCollectionByNameOrId("energy_schedules"); err != nil {
		c := core.NewBaseCollection("energy_schedules", "")
		authRule := "@request.auth.id != '' && user = @request.auth.id"
		c.ListRule = &authRule
		c.ViewRule = &authRule
		c.Fields.Add(
			&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1},
			&core.DateField{Name: "date", Required: true},
			&core.DateField{Name: "wake_time", Required: true},
			&core.JSONField{Name: "schedule_json", MaxSize: 1000000},
		)
		if err := app.Save(c); err != nil {
			slog.Error("failed to create energy_schedules collection", "error", err)
		}
	}

	// settings
	if _, err := app.FindCollectionByNameOrId("settings"); err != nil {
		c := core.NewBaseCollection("settings", "")
		authRule := "@request.auth.id != '' && user = @request.auth.id"
		c.ListRule = &authRule
		c.ViewRule = &authRule
		c.CreateRule = &authRule
		c.UpdateRule = &authRule
		c.Fields.Add(
			&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1},
			&core.NumberField{Name: "sleep_need_hours"},
			&core.TextField{Name: "ntfy_topic"},
			&core.TextField{Name: "ntfy_server"},
			&core.TextField{Name: "ntfy_access_token"},
			&core.TextField{Name: "site_url"},
			&core.TextField{Name: "fitbit_client_id"},
			&core.TextField{Name: "fitbit_client_secret"},
			&core.TextField{Name: "fitbit_access_token"},
			&core.TextField{Name: "fitbit_refresh_token"},
			&core.DateField{Name: "fitbit_token_expiry"},
			&core.BoolField{Name: "notifications_enabled"},
		)
		if err := app.Save(c); err != nil {
			slog.Error("failed to create settings collection", "error", err)
		}
	}
}

func runMorningJobForAllUsers(app *pocketbase.PocketBase) {
	users, err := app.FindAllRecords("users")
	if err != nil {
		slog.Error("failed to find users for morning job", "error", err)
		return
	}
	for _, user := range users {
		if err := services.RunMorningJob(app, user.Id); err != nil {
			slog.Error("morning job failed", "user_id", user.Id, "error", err)
		}
	}
}

func syncFitbitForAllUsers(app *pocketbase.PocketBase) {
	settings, err := app.FindRecordsByFilter("settings", "fitbit_access_token != ''", "", 0, 0, nil)
	if err != nil {
		slog.Error("failed to find fitbit users for sync", "error", err)
		return
	}

	for _, s := range settings {
		userID := s.GetString("user")
		token := &oauth2.Token{
			AccessToken:  s.GetString("fitbit_access_token"),
			RefreshToken: s.GetString("fitbit_refresh_token"),
			Expiry:       s.GetDateTime("fitbit_token_expiry").Time(),
		}

		// Refresh token if expired.
		if token.Expiry.Before(time.Now()) {
			newToken, err := ingest.RefreshFitbitToken(context.Background(), token)
			if err != nil {
				slog.Error("fitbit token refresh failed", "user_id", userID, "error", err)
				continue
			}
			s.Set("fitbit_access_token", newToken.AccessToken)
			s.Set("fitbit_refresh_token", newToken.RefreshToken)
			s.Set("fitbit_token_expiry", newToken.Expiry)
			app.Save(s)
			token = newToken
		}

		// Fetch yesterday's and today's sleep.
		for _, date := range []time.Time{time.Now().AddDate(0, 0, -1), time.Now()} {
			records, err := ingest.FetchFitbitSleep(context.Background(), token, date)
			if err != nil {
				slog.Error("fitbit sync failed", "user_id", userID, "error", err)
				continue
			}

			collection, _ := app.FindCollectionByNameOrId("sleep_records")
			for _, rec := range records {
				dateStr := rec.Date.Format("2006-01-02")
				existing, _ := app.FindFirstRecordByFilter("sleep_records",
					"user = {:user} && date = {:date} && source = {:source}",
					map[string]any{"user": userID, "date": dateStr, "source": "fitbit"},
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
				record.Set("source", "fitbit")
				record.Set("duration_minutes", rec.DurationMinutes)
				record.Set("deep_minutes", rec.DeepMinutes)
				record.Set("rem_minutes", rec.REMMinutes)
				record.Set("light_minutes", rec.LightMinutes)
				record.Set("awake_minutes", rec.AwakeMinutes)
				app.Save(record)
			}

		}
	}
}
