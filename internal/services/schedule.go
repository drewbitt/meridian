package services

import (
	"errors"
	"math"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

var errNoRecords = errors.New("failed to load records")

func loadUserRecords(app core.App, userID string) (records []*core.Record, settings *core.Record, sleepNeed float64, loc *time.Location) {
	// Load settings first so we can use the user's timezone for the date window.
	sleepNeed = 8.0
	loc = time.Local
	settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": userID})
	if err == nil {
		if sn := settings.GetFloat("sleep_need_hours"); sn > 0 {
			sleepNeed = sn
		}
		loc = LocationFromSettings(settings)
	} else {
		settings = nil // ensure nil on error
	}

	fourteenDaysAgo := time.Now().In(loc).AddDate(0, 0, -14).Format("2006-01-02 00:00:00")
	records, err = app.FindRecordsByFilter(
		"sleep_records",
		"user = {:user} && date >= {:since}",
		"-date", 0, 0,
		map[string]any{"user": userID, "since": fourteenDaysAgo},
	)
	if err != nil {
		return nil, settings, sleepNeed, loc
	}
	return records, settings, sleepNeed, loc
}

// ComputeUserDebt loads sleep records and settings for a user, then
// computes the current sleep debt.
func ComputeUserDebt(app core.App, userID string) engine.SleepDebt {
	records, _, sleepNeed, loc := loadUserRecords(app, userID)
	if records == nil {
		return engine.SleepDebt{}
	}
	engineRecords, _ := ConvertSleepRecords(records, loc)
	return engine.CalculateSleepDebt(engineRecords, sleepNeed, time.Now().In(loc))
}

// ComputeUserSchedule loads sleep records and settings for a user, then
// computes the energy schedule, sleep debt, and wake time.
// Returns the classified schedule, the raw prediction points (before zone
// classification), and the sleep debt. Raw points are stored for caching;
// zones are re-derived on load.
func ComputeUserSchedule(app core.App, userID string) (engine.Schedule, []engine.EnergyPoint, engine.SleepDebt, error) {
	records, settings, sleepNeed, loc := loadUserRecords(app, userID)
	if records == nil {
		return engine.Schedule{}, nil, engine.SleepDebt{}, errNoRecords
	}

	now := time.Now().In(loc)

	engineRecords, periods := ConvertSleepRecords(records, loc)
	debt := engine.CalculateSleepDebt(engineRecords, sleepNeed, now)

	// Determine morning wake (stable — naps don't affect it).
	morningWake := DetermineMorningWake(periods, now, loc)

	// Build params with chronotype and debt adjustments.
	params := engine.DefaultParams()

	// Apply chronotype, location, and seasonal adjustments from settings.
	// settings may be nil if the user has no settings record — all
	// downstream helpers (CoordinatesFromSettings, etc.) handle nil safely.
	if settings != nil {
		if shift := settings.GetFloat("chronotype_shift"); shift != 0 {
			// Manual override: apply shift directly to acrophase.
			params.CAcrophase += shift
		} else {
			// Auto-detect: compute habitual sleep midpoint from recent periods.
			if mid, ok := habitualSleepMidpoint(periods, now, loc); ok {
				params = engine.AdjustForChronotype(params, mid)
			}
		}
	}

	// Seasonal circadian adjustment: day length shifts circadian phase.
	// Longer summer days → later peak; shorter winter days → earlier peak.
	lat, lng, _ := CoordinatesFromSettings(settings) // nil-safe: returns NYC defaults
	seasonalShift := SeasonalCAcrophaseShift(lat, lng, now)
	params.CAcrophase += seasonalShift

	// Modulate model parameters based on accumulated sleep debt.
	params = engine.AdjustForDebt(params, debt.Hours)

	// Convert times to user's local timezone before passing to the engine.
	// PocketBase stores UTC, but the TPM's timeOfDay() and CAcrophase are
	// calibrated to local time. Without this conversion, the circadian peak
	// would be offset by the user's UTC delta (e.g., 9 hours for Tokyo).
	localWake := morningWake.In(loc)
	localPeriods := make([]engine.SleepPeriod, len(periods))
	for i, p := range periods {
		localPeriods[i] = engine.SleepPeriod{
			Start: p.Start.In(loc),
			End:   p.End.In(loc),
			IsNap: p.IsNap,
		}
	}

	points := engine.PredictEnergy(params, localPeriods, localWake, localWake.Add(24*time.Hour))
	schedule := engine.ClassifyZones(points, localWake, localPeriods...)
	schedule.MorningWake = localWake

	// Populate sunrise/sunset from solar data.
	solar := GetSolarTimes(lat, lng, now, false)
	schedule.Sunrise = solar.Sunrise.In(loc)
	schedule.Sunset = solar.Sunset.In(loc)

	return schedule, points, debt, nil
}

// DetermineMorningWake finds the end time of the main (non-nap) sleep period
// for the given date. It looks for the longest sleep period overlapping the
// night window (8pm previous day through noon today). Falls back to 7am if
// no qualifying sleep period is found. Naps never affect this value.
func DetermineMorningWake(periods []engine.SleepPeriod, date time.Time, loc *time.Location) time.Time {
	fallback := time.Date(date.Year(), date.Month(), date.Day(), 7, 0, 0, 0, loc)

	// Night window: 8pm yesterday through noon today.
	// Go's time.Date normalizes Day-1 across month boundaries (Day 0 = last day of prev month).
	nightStart := time.Date(date.Year(), date.Month(), date.Day()-1, 20, 0, 0, 0, loc)
	nightEnd := time.Date(date.Year(), date.Month(), date.Day(), 12, 0, 0, 0, loc)

	var bestPeriod *engine.SleepPeriod
	var bestDur time.Duration

	for i := range periods {
		p := &periods[i]
		if p.IsNap {
			continue
		}
		// Check if this period overlaps the night window.
		if p.End.Before(nightStart) || p.Start.After(nightEnd) {
			continue
		}
		dur := p.End.Sub(p.Start)
		if dur > bestDur {
			bestDur = dur
			bestPeriod = p
		}
	}

	if bestPeriod == nil {
		return fallback
	}
	return bestPeriod.End
}

// RefreshScheduleIfNeeded recomputes and stores the energy schedule for a user
// if the current cached schedule is stale (e.g., after new sleep data arrives).
// When a new nap is detected, it sends a post-nap notification instead of a
// "good morning" notification. Returns true if the schedule was updated.
func RefreshScheduleIfNeeded(app core.App, userID string) (bool, error) {
	records, _, _, loc := loadUserRecords(app, userID)
	if records == nil {
		return false, errNoRecords
	}

	schedule, rawPoints, _, err := ComputeUserSchedule(app, userID)
	if err != nil {
		return false, err
	}
	if err := storeSchedule(app, userID, schedule.MorningWake, rawPoints); err != nil {
		return false, err
	}

	// Check if any nap just ended (within last 5 min) and send a post-nap notification.
	_, periods := ConvertSleepRecords(records, loc)
	now := time.Now()
	for _, p := range periods {
		if p.IsNap && now.Sub(p.End) < 5*time.Minute && now.After(p.End) {
			SendPostNapNotification(app, userID, p.End)
			break
		}
	}

	return true, nil
}

// habitualSleepMidpoint computes the average sleep midpoint (fractional hours
// since midnight) from sleep periods over the last 14 days. Uses a trimmed
// circular mean: compute initial mean, discard outliers beyond 2 circular SDs,
// recompute. Returns false if fewer than 5 qualifying data points remain, or
// if timing is too dispersed (mean resultant length R̄ < 0.3).
//
// References:
//   - Mardia & Jupp (2000) "Directional Statistics": circular mean is MLE for von Mises
//   - R̄ threshold: low R̄ means dispersed data, unreliable center estimate
//   - Trimming: robust against party nights / insomnia shifting midpoint
func habitualSleepMidpoint(periods []engine.SleepPeriod, now time.Time, loc *time.Location) (float64, bool) {
	cutoff := now.AddDate(0, 0, -14)

	// Collect midpoint angles (radians on 24h circle).
	var angles []float64
	for _, p := range periods {
		if p.Start.Before(cutoff) {
			continue
		}
		dur := p.End.Sub(p.Start)
		if dur < 3*time.Hour || dur > 14*time.Hour {
			continue // skip naps and implausible records
		}
		mid := p.Start.In(loc).Add(dur / 2)
		h := float64(mid.Hour()) + float64(mid.Minute())/60.0
		angles = append(angles, h*2*math.Pi/24.0)
	}

	if len(angles) < 5 {
		return 0, false
	}

	// First pass: raw circular mean.
	meanAng, rBar := circularMeanAndR(angles)

	// Trim outliers: discard angles beyond 2 circular SDs from the mean.
	// Circular SD = sqrt(-2 * ln(R̄)) in radians (Mardia & Jupp eq. 2.3.7).
	// Only trim if R̄ > 0 (otherwise SD is undefined / infinite).
	if rBar > 0.1 {
		circSD := math.Sqrt(-2 * math.Log(rBar))
		threshold := 2 * circSD
		trimmed := angles[:0] // reuse backing array
		for _, a := range angles {
			if circAngDist(a, meanAng) <= threshold {
				trimmed = append(trimmed, a)
			}
		}
		if len(trimmed) >= 5 {
			angles = trimmed
			meanAng, rBar = circularMeanAndR(angles)
		}
		// If trimming drops below 5, use the untrimmed result.
	}

	// R̄ check: if sleep timing is too dispersed, the estimate is unreliable.
	// R̄ ranges from 0 (uniform/random) to 1 (all identical).
	// Threshold 0.3 corresponds to a circular SD of ~1.55 rad ≈ 5.9 hours.
	if rBar < 0.3 {
		return 0, false
	}

	meanHour := meanAng * 24.0 / (2 * math.Pi)
	if meanHour < 0 {
		meanHour += 24.0
	}
	return meanHour, true
}

// circularMeanAndR computes the circular mean direction and mean resultant
// length R̄ from a slice of angles in radians.
func circularMeanAndR(angles []float64) (meanAngle, rBar float64) {
	var sinSum, cosSum float64
	for _, a := range angles {
		sinSum += math.Sin(a)
		cosSum += math.Cos(a)
	}
	n := float64(len(angles))
	meanAngle = math.Atan2(sinSum/n, cosSum/n)
	rBar = math.Sqrt(sinSum*sinSum+cosSum*cosSum) / n
	return
}

// circAngDist returns the shortest angular distance between two angles in radians.
func circAngDist(a, b float64) float64 {
	d := math.Mod(a-b+3*math.Pi, 2*math.Pi) - math.Pi
	return math.Abs(d)
}
