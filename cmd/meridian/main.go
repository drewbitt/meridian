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

	// Sync Fitbit immediately on startup so data isn't stale until the first
	// cron tick (up to 30 min away). Runs in a goroutine to avoid blocking serve.
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Id: "startup-fitbit-sync",
		Func: func(se *core.ServeEvent) error {
			go syncFitbitForAllUsers(app)
			return se.Next()
		},
	})

	app.Cron().MustAdd("fitbit-sync", "*/30 * * * *", func() {
		syncFitbitForAllUsers(app)
	})

	// Morning job runs hourly but only fires for users whose local time is
	// 8 AM. Computes the schedule + sends morning greeting only.
	app.Cron().MustAdd("morning-schedule", "0 * * * *", func() {
		runMorningJobForAllUsers(app)
	})

	// Short-horizon notification dispatcher: every 5 minutes, check for
	// upcoming events (caffeine cutoff, melatonin, nap window, habits)
	// and send notifications that fall within the next 10-minute window.
	// Always reads the latest schedule, so mid-day changes (nap detection,
	// Fitbit sync) are reflected immediately.
	app.Cron().MustAdd("notification-dispatch", "*/5 * * * *", func() {
		dispatchNotificationsForAllUsers(app)
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
		loc := services.UserLocation(app, user.Id)
		if time.Now().In(loc).Hour() != 8 {
			continue
		}
		if err := services.RunMorningJob(app, user.Id); err != nil {
			slog.Error("morning job failed", "user_id", user.Id, "error", err)
		}
	}
}

func dispatchNotificationsForAllUsers(app *pocketbase.PocketBase) {
	users, err := app.FindAllRecords("users")
	if err != nil {
		slog.Error("failed to find users for notification dispatch", "error", err)
		return
	}
	for _, user := range users {
		if err := services.DispatchUpcomingNotifications(app, user.Id, 10*time.Minute); err != nil {
			slog.Error("notification dispatch failed", "user_id", user.Id, "error", err)
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
		// Pull 3 days back (not just 1) to catch missed syncs, Fitbit sync
		// delays, and timezone edge cases. The upsert is idempotent so
		// re-fetching already-synced days is harmless.
		start := now.AddDate(0, 0, -3)
		if err := services.SyncFitbitUser(app, s, start, now); err != nil {
			slog.Error("fitbit sync failed", "user_id", s.GetString("user"), "error", err)
			continue
		}
		// Use RefreshScheduleIfNeeded so nap detection + post-nap notifications work.
		if _, err := services.RefreshScheduleIfNeeded(app, s.GetString("user")); err != nil {
			slog.Error("schedule update after sync failed", "user_id", s.GetString("user"), "error", err)
		}
	}
}
