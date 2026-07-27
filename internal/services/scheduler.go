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
	return storeSchedule(app, userID, schedule, rawPoints)
}

// RunMorningJob computes and stores the energy schedule for a user,
// and dispatches scheduled notifications if enabled.
// Notification delivery is idempotent per day even when Fitbit sync or a
// manual entry already created today's schedule.
func RunMorningJob(app core.App, userID string) error {
	loc := UserLocation(app, userID)
	today := PocketBaseDate(time.Now().In(loc))

	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err != nil {
		return fmt.Errorf("load settings for user %s: %w", userID, err)
	}

	schedule, rawPoints, debt, err := ComputeUserSchedule(app, userID)
	if err != nil {
		return fmt.Errorf("compute schedule: %w", err)
	}

	if err := storeSchedule(app, userID, schedule, rawPoints); err != nil {
		return fmt.Errorf("store schedule: %w", err)
	}

	summaryTitle, summaryReady := scheduleSummaryNotification(schedule, time.Now(), loc)
	if !summaryReady {
		// Keep the schedule/data-status cache current, but do not consume the
		// daily summary key before today's completed main sleep is available.
		return nil
	}

	schedRec, err := app.FindFirstRecordByFilter("energy_schedules",
		"user = {:user} && date = {:date}",
		map[string]any{"user": userID, "date": today},
	)
	if err != nil {
		return fmt.Errorf("reload schedule: %w", err)
	}
	sent := loadSentKeys(schedRec)
	if sent["morning_summary"] {
		return nil
	}

	if settings.GetBool("notifications_enabled") && settings.GetString("ntfy_topic") != "" {
		siteURL := settings.GetString("site_url")

		morningMsg := fmt.Sprintf("Sleep debt: %.1fh (%s).", debt.Hours, debt.Category)
		if debt.IsLowerBound {
			morningMsg = fmt.Sprintf("Observed sleep debt lower bound: %.1fh from %d night(s).", debt.Hours, debt.ObservedNights)
		}
		if reliableModelTiming(schedule.Confidence) && !schedule.BestFocusStart.IsZero() {
			morningMsg += fmt.Sprintf(" Modeled high energy: %s-%s.",
				schedule.BestFocusStart.In(loc).Format("3:04pm"),
				schedule.BestFocusEnd.In(loc).Format("3:04pm"),
			)
		} else if schedule.Confidence == engine.ConfidencePreliminary || schedule.Confidence == engine.ConfidenceLow {
			morningMsg += " Timing is a rough planning estimate; precise model-timed alerts are paused."
		}
		if len(schedule.Points) == 0 && debt.ObservedNights > 0 {
			if schedule.ConfidenceReason != "" {
				morningMsg += " Today's timing curve was withheld: " + schedule.ConfidenceReason + "."
			} else {
				morningMsg += " No recent main sleep was found, so today's curve was not forecast."
			}
		} else if debt.IsEstimate {
			morningMsg += fmt.Sprintf(" %d of 14 sleep days were estimated from stable recent sleep.", debt.EstimatedNights)
		}
		if err := SendNotification(buildNotif(settings, siteURL,
			summaryTitle,
			morningMsg,
			3,
			time.Time{},
			[]string{"sunny", "battery"},
		)); err != nil {
			slog.Error("failed morning notification", "user_id", userID, "error", err)
			return nil
		}
		sent["morning_summary"] = true
		if err := saveSentKeys(app, schedRec, sent); err != nil {
			return fmt.Errorf("save morning notification state: %w", err)
		}
		// Future notifications (caffeine, melatonin, nap) are handled by
		// DispatchUpcomingNotifications on a short-horizon cycle, not here.
		// This ensures mid-day schedule changes (e.g., nap detection) are
		// reflected in notifications.
	}

	return nil
}

func scheduleSummaryNotification(schedule engine.Schedule, now time.Time, loc *time.Location) (string, bool) {
	if len(schedule.Points) == 0 || schedule.MorningWake.IsZero() {
		return "", false
	}
	localNow := now.In(loc)
	localWake := schedule.MorningWake.In(loc)
	nowY, nowM, nowD := localNow.Date()
	wakeY, wakeM, wakeD := localWake.Date()
	if nowY != wakeY || nowM != wakeM || nowD != wakeD {
		return "", false
	}
	age := localNow.Sub(localWake)
	if age < 0 || age > maxForecastWakeAge {
		return "", false
	}
	if age <= 4*time.Hour {
		return "Good morning!", true
	}
	return "Today's sleep synced", true
}

func storeSchedule(app core.App, userID string, schedule engine.Schedule, rawPoints []engine.EnergyPoint) error {
	collection, err := app.FindCollectionByNameOrId("energy_schedules")
	if err != nil {
		return err
	}

	loc := UserLocation(app, userID)
	today := PocketBaseDate(time.Now().In(loc))

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

	record.Set("wake_time", schedule.MorningWake)
	record.Set("schedule_json", rawPoints)
	record.Set("confidence", string(schedule.Confidence))
	record.Set("confidence_reason", schedule.ConfidenceReason)
	record.Set("observed_nights", schedule.ObservedNights)
	record.Set("is_estimate", schedule.IsEstimate)

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
	today := PocketBaseDate(time.Now().In(loc))
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

	sleepNeed := settings.GetFloat("sleep_need_hours")
	if sleepNeed <= 0 {
		sleepNeed = 8
	}
	schedule := engine.ClassifyZonesForSleepNeed(points, wakeTime, sleepNeed)
	schedule.Confidence = engine.ForecastConfidence(schedRec.GetString("confidence"))
	schedule.ConfidenceReason = schedRec.GetString("confidence_reason")
	schedule.ObservedNights = schedRec.GetInt("observed_nights")
	schedule.IsEstimate = schedRec.GetBool("is_estimate")
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

	// Only turn model-derived estimates into proactive alerts when recent sleep
	// supports at least a moderate-confidence personalized schedule.
	reliableTiming := reliableModelTiming(schedule.Confidence)

	// Caffeine cutoff: 30 min before cutoff time.
	if reliableTiming && !schedule.CaffeineCutoff.IsZero() {
		candidates = append(candidates, candidate{
			key: "caffeine_cutoff",
			at:  schedule.CaffeineCutoff.Add(-30 * time.Minute),
			notif: buildNotif(settings, siteURL,
				"Caffeine Cutoff Soon",
				fmt.Sprintf("Conservative caffeine cutoff at %s (about 10 hours before target sleep)", schedule.CaffeineCutoff.In(loc).Format("3:04pm")),
				3, time.Time{}, []string{"coffee", "warning"},
			),
		})
	}

	// Estimated wind-down: 30 min before the planning window starts.
	if reliableTiming && !schedule.MelatoninWindow.IsZero() {
		candidates = append(candidates, candidate{
			key: "melatonin_window",
			at:  schedule.MelatoninWindow.Add(-30 * time.Minute),
			notif: buildNotif(settings, siteURL,
				"Wind-Down in 30 Minutes",
				"Your estimated wind-down starts in 30 minutes. This is a planning cue, not a melatonin measurement.",
				4, time.Time{}, []string{"crescent_moon", "zzz"},
			),
		})
	}

	// Optimal nap window.
	if reliableTiming && !schedule.OptimalNapStart.IsZero() {
		candidates = append(candidates, candidate{
			key: "nap_window",
			at:  schedule.OptimalNapStart,
			notif: buildNotif(settings, siteURL,
				"Optional Short-Nap Window",
				fmt.Sprintf("A clear modeled dip runs until %s. Nap only if it fits your sleep plan.", schedule.OptimalNapEnd.In(loc).Format("3:04pm")),
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
		if isModelDerivedAnchor(h.Anchor) && !reliableTiming {
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
		if err := saveSentKeys(app, schedRec, sent); err != nil {
			return fmt.Errorf("save notification state: %w", err)
		}
	}

	return nil
}

// SendPostNapNotification sends a lightweight notification when a new nap is detected,
// with updated energy forecast info. This replaces the "good morning" that would have
// incorrectly fired after a nap.
func SendPostNapNotification(app core.App, userID string, _ time.Time) {
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err != nil || !settings.GetBool("notifications_enabled") || settings.GetString("ntfy_topic") == "" {
		return
	}

	siteURL := settings.GetString("site_url")

	msg := "Nap logged. Today's curve was updated; brief sleep inertia can follow a nap, so compare the forecast with how you feel."
	if err := SendNotification(buildNotif(settings, siteURL,
		"Nap Detected",
		msg,
		2, time.Time{}, []string{"bed", "battery"},
	)); err != nil {
		slog.Error("failed post-nap notification", "user_id", userID, "error", err)
	}
}

func reliableModelTiming(confidence engine.ForecastConfidence) bool {
	return confidence == engine.ConfidenceModerate || confidence == engine.ConfidenceHigh
}

func isModelDerivedAnchor(anchor string) bool {
	switch anchor {
	case "best_focus", "morning_peak", "afternoon_dip", "nap_window",
		"evening_peak", "caffeine_cutoff", "melatonin_window":
		return true
	default:
		return false
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

func saveSentKeys(app core.App, schedRec *core.Record, sent map[string]bool) error {
	var keys []string
	for k := range sent {
		keys = append(keys, k)
	}
	schedRec.Set("notifications_sent", keys)
	return app.Save(schedRec)
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
