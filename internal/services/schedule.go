package services

import (
	"errors"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

var errNoRecords = errors.New("failed to load records")

func loadUserRecords(app core.App, userID string) (records []*core.Record, sleepNeed float64, loc *time.Location) {
	// Load settings first so we can use the user's timezone for the date window.
	sleepNeed = 8.0
	loc = time.Local
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err == nil {
		if sn := settings.GetFloat("sleep_need_hours"); sn > 0 {
			sleepNeed = sn
		}
		loc = LocationFromSettings(settings)
	}

	fourteenDaysAgo := time.Now().In(loc).AddDate(0, 0, -14).Format("2006-01-02 00:00:00")
	records, err = app.FindRecordsByFilter(
		"sleep_records",
		"user = {:user} && date >= {:since}",
		"-date", 0, 0,
		map[string]any{"user": userID, "since": fourteenDaysAgo},
	)
	if err != nil {
		return nil, sleepNeed, loc
	}
	return records, sleepNeed, loc
}

// ComputeUserDebt loads sleep records and settings for a user, then
// computes the current sleep debt.
func ComputeUserDebt(app core.App, userID string) engine.SleepDebt {
	records, sleepNeed, loc := loadUserRecords(app, userID)
	if records == nil {
		return engine.SleepDebt{}
	}
	engineRecords, _ := ConvertSleepRecords(records)
	return engine.CalculateSleepDebt(engineRecords, sleepNeed, time.Now().In(loc))
}

// ComputeUserSchedule loads sleep records and settings for a user, then
// computes the energy schedule, sleep debt, and wake time.
// Returns the classified schedule, the raw prediction points (before zone
// classification), and the sleep debt. Raw points are stored for caching;
// zones are re-derived on load.
func ComputeUserSchedule(app core.App, userID string) (engine.Schedule, []engine.EnergyPoint, engine.SleepDebt, error) {
	records, sleepNeed, loc := loadUserRecords(app, userID)
	if records == nil {
		return engine.Schedule{}, nil, engine.SleepDebt{}, errNoRecords
	}

	now := time.Now().In(loc)

	engineRecords, periods := ConvertSleepRecords(records)
	debt := engine.CalculateSleepDebt(engineRecords, sleepNeed, now)
	wakeTime := time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, loc)
	for _, p := range periods {
		if p.End.After(wakeTime) {
			wakeTime = p.End
		}
	}

	points := engine.PredictEnergy(periods, wakeTime, wakeTime.Add(24*time.Hour))
	schedule := engine.ClassifyZones(points, wakeTime)

	return schedule, points, debt, nil
}
