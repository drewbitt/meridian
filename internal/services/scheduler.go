package services

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

// UpdateUserSchedule computes and stores the energy schedule for a user.
// It does not dispatch notifications — call RunMorningJob for that.
func UpdateUserSchedule(app core.App, userID string) error {
	schedule, rawPoints, _, err := ComputeUserSchedule(app, userID)
	if err != nil {
		return fmt.Errorf("compute schedule: %w", err)
	}
	return storeSchedule(app, userID, schedule.MorningWake, rawPoints)
}

// RunMorningJob computes and stores the energy schedule for a user,
// and dispatches scheduled notifications if enabled.
// It is idempotent per day — returns early if a schedule already exists for today.
func RunMorningJob(app core.App, userID string) error {
	loc := UserLocation(app, userID)
	today := time.Now().In(loc).Format("2006-01-02")

	// Dedupe: only run once per user per day (prevents duplicate notifications
	// when the cron fires multiple times within the morning hour).
	existing, _ := app.FindFirstRecordByFilter("energy_schedules",
		"user = {:user} && date = {:date}",
		map[string]any{"user": userID, "date": today},
	)
	if existing != nil {
		return nil
	}

	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err != nil {
		return fmt.Errorf("load settings for user %s: %w", userID, err)
	}

	schedule, rawPoints, debt, err := ComputeUserSchedule(app, userID)
	if err != nil {
		return fmt.Errorf("compute schedule: %w", err)
	}

	if err := storeSchedule(app, userID, schedule.MorningWake, rawPoints); err != nil {
		slog.Error("failed to store schedule", "user_id", userID, "error", err)
	}

	if settings.GetBool("notifications_enabled") && settings.GetString("ntfy_topic") != "" {
		siteURL := settings.GetString("site_url")

		// Warn user if sleep data is stale or insufficient.
		switch debt.Freshness {
		case engine.FreshnessStale:
			if err := SendNotification(buildNotif(settings, siteURL,
				"Sleep data out of date",
				fmt.Sprintf("No sleep data for %d of the last 13 nights. Your schedule may be inaccurate. Try syncing your tracker.", debt.GapDays),
				4,
				time.Time{},
				[]string{"warning", "zzz"},
			)); err != nil {
				slog.Error("failed staleness notification", "user_id", userID, "error", err)
			}
		case engine.FreshnessInsufficient:
			if err := SendNotification(buildNotif(settings, siteURL,
				"Not enough sleep data",
				fmt.Sprintf("Only %d of 13 nights have data. Sync your tracker or add sleep manually for accurate predictions.", 13-debt.GapDays),
				4,
				time.Time{},
				[]string{"warning", "zzz"},
			)); err != nil {
				slog.Error("failed insufficient-data notification", "user_id", userID, "error", err)
			}
		}

		morningMsg := fmt.Sprintf("Sleep debt: %.1fh (%s).", debt.Hours, debt.Category)
		if !schedule.BestFocusStart.IsZero() {
			morningMsg += fmt.Sprintf(" Best focus: %s-%s.",
				schedule.BestFocusStart.In(loc).Format("3:04pm"),
				schedule.BestFocusEnd.In(loc).Format("3:04pm"),
			)
		}
		if debt.Freshness == engine.FreshnessRecent && debt.LastNightMissing {
			morningMsg += " (no sleep data for last night)"
		}
		if err := SendNotification(buildNotif(settings, siteURL,
			"Good morning!",
			morningMsg,
			3,
			time.Time{},
			[]string{"sunny", "battery"},
		)); err != nil {
			slog.Error("failed morning notification", "user_id", userID, "error", err)
		}
		// Future notifications (caffeine, melatonin, nap) are handled by
		// DispatchUpcomingNotifications on a short-horizon cycle, not here.
		// This ensures mid-day schedule changes (e.g., nap detection) are
		// reflected in notifications.
	}

	return nil
}

func storeSchedule(app core.App, userID string, wakeTime time.Time, rawPoints []engine.EnergyPoint) error {
	collection, err := app.FindCollectionByNameOrId("energy_schedules")
	if err != nil {
		return err
	}

	loc := UserLocation(app, userID)
	today := time.Now().In(loc).Format("2006-01-02")

	existing, err := app.FindFirstRecordByFilter("energy_schedules",
		"user = {:user} && date = {:date}",
		map[string]any{"user": userID, "date": today},
	)

	var record *core.Record
	if err == nil && existing != nil {
		record = existing
	} else {
		record = core.NewRecord(collection)
		record.Set("user", userID)
		record.Set("date", today)
	}

	record.Set("wake_time", wakeTime)
	record.Set("schedule_json", rawPoints)

	return app.Save(record)
}

// buildNotif constructs a Notification from settings fields.
func buildNotif(settings *core.Record, siteURL, title, message string, priority int, at time.Time, tags []string) Notification {
	return Notification{
		Server:      settings.GetString("ntfy_server"),
		Topic:       settings.GetString("ntfy_topic"),
		AccessToken: settings.GetString("ntfy_access_token"),
		Title:       title,
		Message:     message,
		Priority:    priority,
		At:          at,
		Tags:        tags,
		Click:       dashboardURL(siteURL),
		Actions:     dashboardAction(siteURL),
	}
}

// DispatchUpcomingNotifications checks for notification events due within the
// given horizon and sends them immediately. It uses a notifications_sent JSON
// field on the energy_schedules record for deduplication, so it is safe to call
// every few minutes.
func DispatchUpcomingNotifications(app core.App, userID string, horizon time.Duration) error {
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err != nil {
		return nil // no settings → nothing to send
	}
	if !settings.GetBool("notifications_enabled") || settings.GetString("ntfy_topic") == "" {
		return nil
	}

	loc := UserLocation(app, userID)
	today := time.Now().In(loc).Format("2006-01-02")
	schedRec, err := app.FindFirstRecordByFilter("energy_schedules",
		"user = {:user} && date = {:date}",
		map[string]any{"user": userID, "date": today},
	)
	if err != nil || schedRec == nil {
		return nil // no schedule computed yet today
	}

	// Load the current schedule from stored points.
	var points []engine.EnergyPoint
	if raw := schedRec.Get("schedule_json"); raw != nil {
		if b, err := json.Marshal(raw); err == nil {
			if err := json.Unmarshal(b, &points); err != nil {
				slog.Error("corrupt cached schedule_json", "user_id", userID, "error", err)
				return nil // treat as no schedule
			}
		}
	}
	wakeTime := schedRec.GetDateTime("wake_time").Time()
	if wakeTime.IsZero() {
		return nil
	}

	schedule := engine.ClassifyZones(points, wakeTime)
	// ClassifyZones doesn't set MorningWake or solar times (those come from
	// ComputeUserSchedule). Set them so habit anchors resolve correctly.
	schedule.MorningWake = wakeTime
	siteURL := settings.GetString("site_url")

	// Build candidate notifications keyed by a stable name.
	now := time.Now().In(loc)

	// Populate solar times for sunrise/sunset-anchored habits.
	lat, lng, _ := CoordinatesFromSettings(settings)
	solar := GetSolarTimes(lat, lng, now, false)
	schedule.Sunrise = solar.Sunrise.In(loc)
	schedule.Sunset = solar.Sunset.In(loc)
	windowEnd := now.Add(horizon)

	type candidate struct {
		key   string
		notif Notification
		at    time.Time
	}
	var candidates []candidate

	// Caffeine cutoff: 30 min before cutoff time.
	if !schedule.CaffeineCutoff.IsZero() {
		candidates = append(candidates, candidate{
			key: "caffeine_cutoff",
			at:  schedule.CaffeineCutoff.Add(-30 * time.Minute),
			notif: buildNotif(settings, siteURL,
				"Caffeine Cutoff Soon",
				fmt.Sprintf("Last call for caffeine at %s", schedule.CaffeineCutoff.In(loc).Format("3:04pm")),
				3, time.Time{}, []string{"coffee", "warning"},
			),
		})
	}

	// Melatonin window: 30 min before window opens.
	if !schedule.MelatoninWindow.IsZero() {
		candidates = append(candidates, candidate{
			key: "melatonin_window",
			at:  schedule.MelatoninWindow.Add(-30 * time.Minute),
			notif: buildNotif(settings, siteURL,
				"Melatonin Window Opening",
				"Your melatonin window opens in 30 minutes. Start winding down.",
				4, time.Time{}, []string{"crescent_moon", "zzz"},
			),
		})
	}

	// Optimal nap window.
	if !schedule.OptimalNapStart.IsZero() {
		candidates = append(candidates, candidate{
			key: "nap_window",
			at:  schedule.OptimalNapStart,
			notif: buildNotif(settings, siteURL,
				"Optimal Nap Window",
				fmt.Sprintf("Good time for a 20-min nap until %s", schedule.OptimalNapEnd.In(loc).Format("3:04pm")),
				2, time.Time{}, []string{"bed", "battery"},
			),
		})
	}

	// Add habit notifications.
	habits, _ := GetUserHabits(app, userID)
	for _, h := range habits {
		if !h.Notify {
			continue
		}
		habitTime := ResolveHabitTime(h, schedule, loc)
		if habitTime.IsZero() {
			continue
		}
		candidates = append(candidates, candidate{
			key: "habit_" + h.ID,
			at:  habitTime,
			notif: buildNotif(settings, siteURL,
				h.Name,
				fmt.Sprintf("Time for: %s", h.Name),
				2, time.Time{}, []string{"bell"},
			),
		})
	}

	// Load already-sent keys for dedup.
	sent := loadSentKeys(schedRec)
	var newlySent []string

	for _, c := range candidates {
		if sent[c.key] {
			continue
		}
		// Send if event falls within [now, now+horizon).
		if c.at.Before(now) || !c.at.Before(windowEnd) {
			continue
		}
		if err := SendNotification(c.notif); err != nil {
			slog.Error("failed dispatching notification", "key", c.key, "error", err)
			continue
		}
		newlySent = append(newlySent, c.key)
	}

	if len(newlySent) > 0 {
		for _, k := range newlySent {
			sent[k] = true
		}
		saveSentKeys(app, schedRec, sent)
	}

	return nil
}

// SendPostNapNotification sends a lightweight notification when a new nap is detected,
// with updated energy forecast info. This replaces the "good morning" that would have
// incorrectly fired after a nap.
func SendPostNapNotification(app core.App, userID string, napEnd time.Time) {
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err != nil || !settings.GetBool("notifications_enabled") || settings.GetString("ntfy_topic") == "" {
		return
	}

	loc := UserLocation(app, userID)
	siteURL := settings.GetString("site_url")

	// Estimate rebound time (~30 min after nap).
	reboundTime := napEnd.Add(30 * time.Minute).In(loc).Format("3:04pm")

	msg := fmt.Sprintf("Nap logged. Energy rebound expected by %s.", reboundTime)
	if err := SendNotification(buildNotif(settings, siteURL,
		"Nap Detected",
		msg,
		2, time.Time{}, []string{"bed", "battery"},
	)); err != nil {
		slog.Error("failed post-nap notification", "user_id", userID, "error", err)
	}
}

func loadSentKeys(schedRec *core.Record) map[string]bool {
	sent := make(map[string]bool)
	raw := schedRec.GetString("notifications_sent")
	if raw != "" {
		var keys []string
		if err := json.Unmarshal([]byte(raw), &keys); err == nil {
			for _, k := range keys {
				sent[k] = true
			}
		}
	}
	return sent
}

func saveSentKeys(app core.App, schedRec *core.Record, sent map[string]bool) {
	var keys []string
	for k := range sent {
		keys = append(keys, k)
	}
	schedRec.Set("notifications_sent", keys)
	if err := app.Save(schedRec); err != nil {
		slog.Error("failed to save notification sent keys", "error", err)
	}
}

func dashboardURL(siteURL string) string {
	if siteURL == "" {
		return ""
	}
	return strings.TrimRight(siteURL, "/") + "/"
}

func dashboardAction(siteURL string) []Action {
	url := dashboardURL(siteURL)
	if url == "" {
		return nil
	}
	return []Action{{Type: "view", Label: "Dashboard", URL: url}}
}
