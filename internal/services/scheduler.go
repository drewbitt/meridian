package services

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

// SchedulerConfig holds per-user notification and scheduling preferences.
type SchedulerConfig struct {
	UserID               string
	NtfyServer           string
	NtfyTopic            string
	NtfyAccessToken      string
	SiteURL              string
	SleepNeedHours       float64
	NotificationsEnabled bool
}

// UpdateUserSchedule computes and stores the energy schedule for a user.
// It does not dispatch notifications — call RunMorningJob for that.
func UpdateUserSchedule(app core.App, userID string) error {
	schedule, rawPoints, _, err := ComputeUserSchedule(app, userID)
	if err != nil {
		return fmt.Errorf("compute schedule: %w", err)
	}
	return storeSchedule(app, userID, schedule.WakeTime, rawPoints, schedule.WakeTime.Location())
}

// RunMorningJob computes and stores the energy schedule for a user,
// and dispatches scheduled notifications if enabled.
func RunMorningJob(app core.App, userID string) error {
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

	schedule, rawPoints, debt, err := ComputeUserSchedule(app, userID)
	if err != nil {
		return fmt.Errorf("compute schedule: %w", err)
	}

	loc := schedule.WakeTime.Location()
	if err := storeSchedule(app, userID, schedule.WakeTime, rawPoints, loc); err != nil {
		slog.Error("failed to store schedule", "user_id", userID, "error", err)
	}

	if cfg.NotificationsEnabled && cfg.NtfyTopic != "" {
		morningMsg := fmt.Sprintf(
			"Sleep debt: %.1fh (%s). Best focus: %s-%s.",
			debt.Hours, debt.Category,
			schedule.BestFocusStart.In(loc).Format("3:04pm"),
			schedule.BestFocusEnd.In(loc).Format("3:04pm"),
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

		dispatchScheduledNotifications(cfg, schedule, loc)
	}

	return nil
}

func storeSchedule(app core.App, userID string, wakeTime time.Time, rawPoints []engine.EnergyPoint, loc *time.Location) error {
	collection, err := app.FindCollectionByNameOrId("energy_schedules")
	if err != nil {
		return err
	}

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

func dispatchScheduledNotifications(cfg SchedulerConfig, schedule engine.Schedule, loc *time.Location) {
	now := time.Now()

	notifs := []Notification{
		cfg.baseNotif(
			"Caffeine Cutoff Soon",
			fmt.Sprintf("Last call for caffeine at %s", schedule.CaffeineCutoff.In(loc).Format("3:04pm")),
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
			fmt.Sprintf("Good time for a 20-min nap until %s", schedule.OptimalNapEnd.In(loc).Format("3:04pm")),
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
