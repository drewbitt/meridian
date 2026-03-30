package main

import (
	"log/slog"
	"time"

	"github.com/drewbitt/meridian/assets"
	"github.com/drewbitt/meridian/internal/routes"
	"github.com/drewbitt/meridian/internal/schema"
	"github.com/drewbitt/meridian/internal/services"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

func main() {
	app := pocketbase.New()

	// Ensure collections exist on first run.
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Id: "init-collections",
		Func: func(se *core.ServeEvent) error {
			if err := schema.EnsureCollections(app); err != nil {
				slog.Error("failed to ensure collections", "error", err)
			}
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

	// Cron uses the server timezone (set via TZ env var).
	// Fitbit sync runs first so the morning job has fresh data at 8 AM.
	app.Cron().MustAdd("fitbit-sync", "*/30 * * * *", func() {
		syncFitbitForAllUsers(app)
	})

	app.Cron().MustAdd("morning-schedule", "0 8 * * *", func() {
		runMorningJobForAllUsers(app)
	})

	if err := app.Start(); err != nil {
		slog.Error("failed to start", "error", err)
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
		loc := services.LocationFromSettings(s)
		now := time.Now().In(loc)
		start := now.AddDate(0, 0, -1)
		if err := services.SyncFitbitUser(app, s, start, now); err != nil {
			slog.Error("fitbit sync failed", "user_id", s.GetString("user"), "error", err)
			continue
		}
		if err := services.UpdateUserSchedule(app, s.GetString("user")); err != nil {
			slog.Error("schedule update after sync failed", "user_id", s.GetString("user"), "error", err)
		}
	}
}
