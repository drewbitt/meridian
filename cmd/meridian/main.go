package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/drewbitt/meridian/assets"
	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/drewbitt/meridian/internal/routes"
	"github.com/drewbitt/meridian/internal/schema"
	"github.com/drewbitt/meridian/internal/services"
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

		// Build per-user OAuth config from their stored credentials.
		clientID := s.GetString("fitbit_client_id")
		clientSecret := s.GetString("fitbit_client_secret")
		if clientID == "" || clientSecret == "" {
			slog.Warn("fitbit credentials missing, skipping sync", "user_id", userID)
			continue
		}
		siteURL := s.GetString("site_url")
		if siteURL == "" {
			siteURL = app.Settings().Meta.AppURL
		}
		cfg := ingest.NewFitbitOAuthConfig(clientID, clientSecret, strings.TrimRight(siteURL, "/")+"/auth/fitbit/callback")

		token := &oauth2.Token{
			AccessToken:  s.GetString("fitbit_access_token"),
			RefreshToken: s.GetString("fitbit_refresh_token"),
			Expiry:       s.GetDateTime("fitbit_token_expiry").Time(),
		}

		// Refresh token if expired.
		if token.Expiry.Before(time.Now()) {
			newToken, err := ingest.RefreshFitbitToken(context.Background(), cfg, token)
			if err != nil {
				slog.Error("fitbit token refresh failed", "user_id", userID, "error", err)
				continue
			}
			s.Set("fitbit_access_token", newToken.AccessToken)
			s.Set("fitbit_refresh_token", newToken.RefreshToken)
			s.Set("fitbit_token_expiry", newToken.Expiry)
			if err := app.Save(s); err != nil {
				slog.Error("failed to save fitbit token", "user_id", userID, "error", err)
				continue
			}
			token = newToken
		}

		// Fetch user's timezone from Fitbit profile — their API returns
		// sleep times in this timezone without offsets.
		loc, err := ingest.FetchFitbitTimezone(context.Background(), cfg, token)
		if err != nil {
			slog.Warn("could not fetch fitbit timezone, falling back to UTC", "user_id", userID, "error", err)
			loc = time.UTC
		}

		// Fetch yesterday's and today's sleep.
		for _, date := range []time.Time{time.Now().AddDate(0, 0, -1), time.Now()} {
			records, err := ingest.FetchFitbitSleep(context.Background(), cfg, token, date, loc)
			if err != nil {
				slog.Error("fitbit sync failed", "user_id", userID, "error", err)
				continue
			}

			collection, err := app.FindCollectionByNameOrId("sleep_records")
			if err != nil {
				slog.Error("sleep_records collection not found", "error", err)
				continue
			}
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
				if err := app.Save(record); err != nil {
					slog.Error("failed to save fitbit record", "user_id", userID, "error", err)
					continue
				}
			}

		}
	}
}
