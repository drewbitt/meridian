package services

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/drewbitt/circadian/internal/engine"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

type SchedulerConfig struct {
	UserID               string
	NtfyServer           string
	NtfyTopic            string
	NtfyAccessToken      string
	SiteURL              string
	SleepNeedHours       float64
	NotificationsEnabled bool
}

func RunMorningJob(app *pocketbase.PocketBase, userID string) error {
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err != nil {
		return fmt.Errorf("load settings for user %s: %w", userID, err)
	}

	cfg := SchedulerConfig{
		UserID:               userID,
		NtfyServer:           settings.GetString("ntfy_server"),
		NtfyTopic:            settings.GetString("ntfy_topic"),
		NtfyAccessToken:      settings.GetString("ntfy_access_token"),
		SiteURL:              settings.GetString("site_url"),
		SleepNeedHours:       settings.GetFloat("sleep_need_hours"),
		NotificationsEnabled: settings.GetBool("notifications_enabled"),
	}
	if cfg.SleepNeedHours == 0 {
		cfg.SleepNeedHours = 8.0
	}

	fourteenDaysAgo := time.Now().AddDate(0, 0, -14).Format("2006-01-02 00:00:00")
	sleepRecords, err := app.FindRecordsByFilter(
		"sleep_records",
		"user = {:user} && date >= {:since}",
		"-date",
		0, 0,
		map[string]any{"user": userID, "since": fourteenDaysAgo},
	)
	if err != nil {
		return fmt.Errorf("load sleep records: %w", err)
	}

	var engineRecords []engine.SleepRecord
	var sleepPeriods []engine.SleepPeriod
	for _, r := range sleepRecords {
		engineRecords = append(engineRecords, engine.SleepRecord{
			Date:            r.GetDateTime("date").Time(),
			SleepStart:      r.GetDateTime("sleep_start").Time(),
			SleepEnd:        r.GetDateTime("sleep_end").Time(),
			DurationMinutes: r.GetInt("duration_minutes"),
		})
		sleepPeriods = append(sleepPeriods, engine.SleepPeriod{
			Start: r.GetDateTime("sleep_start").Time(),
			End:   r.GetDateTime("sleep_end").Time(),
		})
	}

	debt := engine.CalculateSleepDebt(engineRecords, cfg.SleepNeedHours, time.Now())

	wakeTime := time.Now().Truncate(24 * time.Hour).Add(7 * time.Hour)
	if len(sleepPeriods) > 0 {
		latest := sleepPeriods[0]
		for _, sp := range sleepPeriods {
			if sp.End.After(latest.End) {
				latest = sp
			}
		}
		wakeTime = latest.End
	}

	predStart := wakeTime
	predEnd := wakeTime.Add(24 * time.Hour)
	points := engine.PredictEnergy(sleepPeriods, predStart, predEnd)

	schedule := engine.ClassifyZones(points, wakeTime)

	if err := storeSchedule(app, userID, schedule); err != nil {
		slog.Error("failed to store schedule", "user_id", userID, "error", err)
	}

	if cfg.NotificationsEnabled && cfg.NtfyTopic != "" {
		morningMsg := fmt.Sprintf(
			"Sleep debt: %.1fh (%s). Best focus: %s-%s.",
			debt.Hours, debt.Category,
			schedule.BestFocusStart.Format("3:04pm"),
			schedule.BestFocusEnd.Format("3:04pm"),
		)
		if err := SendNotification(cfg.baseNotif(
			"Good morning!",
			morningMsg,
			3,
			0,
			[]string{"sunny", "battery"},
		)); err != nil {
			slog.Error("failed morning notification", "user_id", userID, "error", err)
		}

		dispatchScheduledNotifications(cfg, schedule)
	}

	return nil
}

func storeSchedule(app *pocketbase.PocketBase, userID string, schedule engine.Schedule) error {
	collection, err := app.FindCollectionByNameOrId("energy_schedules")
	if err != nil {
		return err
	}

	today := time.Now().Format("2006-01-02")

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

	record.Set("wake_time", schedule.WakeTime)
	record.Set("schedule_json", schedule.Points)

	return app.Save(record)
}

func (c SchedulerConfig) baseNotif(title, message string, priority int, delay time.Duration, tags []string) Notification {
	return Notification{
		Server:      c.NtfyServer,
		Topic:       c.NtfyTopic,
		AccessToken: c.NtfyAccessToken,
		Title:       title,
		Message:     message,
		Priority:    priority,
		Delay:       delay,
		Tags:        tags,
		Click:       c.dashboardURL(),
		Actions:     c.dashboardAction(),
	}
}

func dispatchScheduledNotifications(cfg SchedulerConfig, schedule engine.Schedule) {
	now := time.Now()

	notifs := []Notification{
		cfg.baseNotif(
			"Caffeine Cutoff Soon",
			fmt.Sprintf("Last call for caffeine at %s", schedule.CaffeineCutoff.Format("3:04pm")),
			3,
			schedule.CaffeineCutoff.Add(-30*time.Minute).Sub(now),
			[]string{"coffee", "warning"},
		),
		cfg.baseNotif(
			"Melatonin Window Opening",
			"Your melatonin window opens in 30 minutes. Start winding down.",
			4,
			schedule.MelatoninWindow.Add(-30*time.Minute).Sub(now),
			[]string{"crescent_moon", "zzz"},
		),
	}

	if !schedule.OptimalNapStart.IsZero() {
		notifs = append(notifs, cfg.baseNotif(
			"Optimal Nap Window",
			fmt.Sprintf("Good time for a 20-min nap until %s", schedule.OptimalNapEnd.Format("3:04pm")),
			2,
			schedule.OptimalNapStart.Sub(now),
			[]string{"bed", "battery"},
		))
	}

	for _, n := range notifs {
		if n.Delay <= 0 {
			continue
		}
		if err := SendNotification(n); err != nil {
			slog.Error("failed scheduled notification", "title", n.Title, "error", err)
		}
	}
}

func (c SchedulerConfig) dashboardURL() string {
	if c.SiteURL == "" {
		return ""
	}
	return strings.TrimRight(c.SiteURL, "/") + "/"
}

func (c SchedulerConfig) dashboardAction() []Action {
	url := c.dashboardURL()
	if url == "" {
		return nil
	}
	return []Action{{Type: "view", Label: "Dashboard", URL: url}}
}
