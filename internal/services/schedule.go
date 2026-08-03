package services

import (
	"errors"
	"math"
	"slices"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

var errNoRecords = errors.New("failed to load records")

const maxForecastWakeAge = 20 * time.Hour

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

	fourteenDaysAgo := PocketBaseDate(time.Now().In(loc).AddDate(0, 0, -14))
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
// Returns the classified schedule, its classified prediction points for
// caching, and the sleep debt. Persisting classified points preserves
// nap-recovery zones when a cached schedule is loaded without sleep periods.
func ComputeUserSchedule(app core.App, userID string) (engine.Schedule, []engine.EnergyPoint, engine.SleepDebt, error) {
	records, settings, sleepNeed, loc := loadUserRecords(app, userID)
	if records == nil {
		return engine.Schedule{}, nil, engine.SleepDebt{}, errNoRecords
	}

	now := time.Now().In(loc)

	engineRecords, periods := ConvertSleepRecords(records, loc)
	debt := engine.CalculateSleepDebt(engineRecords, sleepNeed, now)

	// Determine the end of the most recent main-sleep episode. Broken sleep
	// fragments are treated as one episode and naps never move this anchor.
	morningWake, hasRecentMainSleep := determineRecentMainSleep(periods, now, loc)
	if len(engineRecords) == 0 {
		// Do not present a model-generated forecast as personalized when the
		// user has not supplied any sleep data. Keep a valid wake time so the
		// daily job can still store/dedupe its insufficient-data warning.
		return engine.Schedule{
			WakeTime:          morningWake,
			MorningWake:       morningWake,
			AwaitingSleepData: settings != nil && settings.GetString("google_health_access_token") != "",
			LastSync:          GoogleHealthLastAttempt(settings),
		}, nil, debt, nil
	}
	if !hasRecentMainSleep {
		// An old record or a nap by itself is not enough to say where today's
		// homeostatic state began. Avoid presenting a generic 7am curve as a
		// personalized forecast.
		return engine.Schedule{
			WakeTime:          morningWake,
			MorningWake:       morningWake,
			Confidence:        engine.ConfidenceNone,
			ObservedNights:    debt.ObservedNights,
			AwaitingSleepData: settings != nil && settings.GetString("google_health_access_token") != "",
			LastSync:          GoogleHealthLastAttempt(settings),
		}, nil, debt, nil
	}

	timing := estimateSleepTiming(periods, now, loc)
	currentMidpoint, hasCurrentMidpoint := currentSleepMidpoint(periods, morningWake, loc)
	// A single recent night is not enough evidence to classify a user's sleep
	// phase as day/shift-oriented. Keep the preliminary forecast available
	// until habitual timing has enough observations to be meaningful.
	unsupportedPhase := timing.valid && hasCurrentMidpoint && circularHourDistance(currentMidpoint, 3.5) > 5
	if timing.valid && circularHourDistance(timing.midpoint, 3.5) > 4 {
		unsupportedPhase = true
	}
	if unsupportedPhase {
		// The static FIPS phase adjustment is only supported within ±2 hours.
		// A day/shift-sleep pattern needs actual light-driven phase modeling;
		// showing precise windows from the clamped model would be misleading.
		return engine.Schedule{
			WakeTime:         morningWake,
			MorningWake:      morningWake,
			Confidence:       engine.ConfidenceLow,
			ConfidenceReason: "sleep timing is outside the static model's supported phase range",
			ObservedNights:   debt.ObservedNights,
			IsEstimate:       debt.IsEstimate,
			LastSync:         GoogleHealthLastAttempt(settings),
		}, nil, debt, nil
	}

	// Build params with chronotype and debt adjustments.
	params := engine.DefaultParams()

	// Apply the bounded chronotype adjustment from settings.
	// settings may be nil if the user has no settings record — all
	// downstream helpers (CoordinatesFromSettings, etc.) handle nil safely.
	chronotypePersonalized := false
	if settings != nil {
		if shift := settings.GetFloat("chronotype_shift"); shift != 0 {
			// Keep manual input inside the same evidence-backed bounds as
			// automatic chronotype adjustment.
			params.CAcrophase += math.Max(-2, math.Min(2, shift))
			chronotypePersonalized = true
		} else if timing.valid {
			// Auto-detect: compute habitual sleep midpoint from recent periods.
			params = engine.AdjustForChronotype(params, timing.midpoint)
			chronotypePersonalized = true
		}
	}

	lat, lng, _ := CoordinatesFromSettings(settings) // nil-safe: returns NYC defaults
	// Sunrise and sunset remain useful display anchors, but local day length is
	// not the user's retinal light exposure. Do not shift individualized phase
	// from a weather-free seasonal proxy; measured light/activity would be
	// required to justify that adjustment.

	// Modulate model parameters based on accumulated sleep debt.
	params = engine.AdjustForDebt(params, debt.Hours)

	// Convert times to user's local timezone before passing to the engine.
	// PocketBase stores UTC, but the TPM's timeOfDay() and CAcrophase are
	// calibrated to local time. Without this conversion, the circadian peak
	// would be offset by the user's UTC delta (e.g., 9 hours for Tokyo).
	localWake := morningWake.In(loc)
	relevantPeriods := predictionSleepPeriods(periods, morningWake)
	localPeriods := make([]engine.SleepPeriod, len(relevantPeriods))
	for i, p := range relevantPeriods {
		localPeriods[i] = engine.SleepPeriod{
			Start: p.Start.In(loc),
			End:   p.End.In(loc),
			IsNap: p.IsNap,
		}
	}

	points := engine.PredictEnergy(params, localPeriods, localWake, localWake.Add(24*time.Hour))
	schedule := engine.ClassifyZonesForSleepNeed(points, localWake, sleepNeed, localPeriods...)
	schedule.MorningWake = localWake
	schedule.ObservedNights = debt.ObservedNights
	schedule.IsEstimate = debt.IsEstimate
	schedule.LastSync = GoogleHealthLastAttempt(settings)
	switch {
	case debt.ObservedNights < 5:
		schedule.Confidence = engine.ConfidencePreliminary
		schedule.ConfidenceReason = "fewer than five main-sleep nights"
	case !chronotypePersonalized || timing.sdHours > 2.5:
		schedule.Confidence = engine.ConfidenceLow
		schedule.ConfidenceReason = "sleep timing is too irregular to personalize phase reliably"
	case debt.IsLowerBound:
		schedule.Confidence = engine.ConfidenceLow
		schedule.ConfidenceReason = "missing or variable sleep makes debt a lower bound"
	case debt.ObservedNights >= 10 && debt.GapDays <= 2 && timing.sdHours <= 1.5:
		schedule.Confidence = engine.ConfidenceHigh
		schedule.ConfidenceReason = "ten or more recent nights with stable timing"
	default:
		schedule.Confidence = engine.ConfidenceModerate
		schedule.ConfidenceReason = "personalized from recent sleep with remaining uncertainty"
	}

	// Populate sunrise/sunset from solar data.
	solar := GetSolarTimes(lat, lng, now, false)
	schedule.Sunrise = solar.Sunrise.In(loc)
	schedule.Sunset = solar.Sunset.In(loc)

	return schedule, schedule.Points, debt, nil
}

// DetermineMorningWake finds the end of the most recent main-sleep episode.
// Main-sleep fragments separated by no more than four hours are consolidated,
// so a broken night anchors to the final wake rather than the longest fragment.
// The recency-based window also supports day and shift sleepers. It falls back
// to 7am when no main sleep ended in the past 20 hours.
func DetermineMorningWake(periods []engine.SleepPeriod, date time.Time, loc *time.Location) time.Time {
	wake, _ := determineRecentMainSleep(periods, date, loc)
	return wake
}

type mainSleepEpisode struct {
	start time.Time
	end   time.Time
}

func determineRecentMainSleep(periods []engine.SleepPeriod, date time.Time, loc *time.Location) (time.Time, bool) {
	fallback := time.Date(date.Year(), date.Month(), date.Day(), 7, 0, 0, 0, loc)
	cutoff := date.Add(-maxForecastWakeAge)
	var latest time.Time
	for _, episode := range mainSleepEpisodes(periods) {
		if episode.end.After(date) || episode.end.Before(cutoff) {
			continue
		}
		if latest.IsZero() || episode.end.After(latest) {
			latest = episode.end
		}
	}
	if latest.IsZero() {
		return fallback, false
	}
	return latest, true
}

func mainSleepEpisodes(periods []engine.SleepPeriod) []mainSleepEpisode {
	main := make([]engine.SleepPeriod, 0, len(periods))
	for _, period := range periods {
		if period.IsNap || period.Start.IsZero() || !period.End.After(period.Start) {
			continue
		}
		main = append(main, period)
	}
	slices.SortFunc(main, func(a, b engine.SleepPeriod) int {
		return a.Start.Compare(b.Start)
	})

	var episodes []mainSleepEpisode
	for _, period := range main {
		if len(episodes) == 0 ||
			period.Start.After(episodes[len(episodes)-1].end.Add(maxSleepFragmentGap)) {
			episodes = append(episodes, mainSleepEpisode{start: period.Start, end: period.End})
			continue
		}
		if period.End.After(episodes[len(episodes)-1].end) {
			episodes[len(episodes)-1].end = period.End
		}
	}
	return episodes
}

// predictionSleepPeriods keeps only the current main-sleep episode and naps
// after it. Simulating across multi-day gaps implicitly models every missing
// hour as wake and can drive the homeostatic state to an unrealistic extreme.
func predictionSleepPeriods(periods []engine.SleepPeriod, wake time.Time) []engine.SleepPeriod {
	var episodeStart time.Time
	for _, episode := range mainSleepEpisodes(periods) {
		if episode.end.Equal(wake) {
			episodeStart = episode.start
			break
		}
	}
	if episodeStart.IsZero() {
		return nil
	}

	relevant := make([]engine.SleepPeriod, 0, len(periods))
	for _, period := range periods {
		if period.IsNap {
			if !period.End.Before(wake) {
				relevant = append(relevant, period)
			}
			continue
		}
		if !period.Start.Before(episodeStart) && !period.End.After(wake) {
			relevant = append(relevant, period)
		}
	}
	return relevant
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
	if err := storeSchedule(app, userID, schedule, rawPoints); err != nil {
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

type sleepTimingEstimate struct {
	midpoint float64
	rBar     float64
	sdHours  float64
	nights   int
	valid    bool
}

// habitualSleepMidpoint computes the average sleep midpoint (fractional hours
// since midnight) from sleep periods over the last 14 days.
//
// References:
//   - Mardia & Jupp (2000) "Directional Statistics": circular mean is MLE for von Mises
//   - R̄ threshold: low R̄ means dispersed data, unreliable center estimate
//   - Trimming: robust against party nights / insomnia shifting midpoint
func habitualSleepMidpoint(periods []engine.SleepPeriod, now time.Time, loc *time.Location) (float64, bool) {
	estimate := estimateSleepTiming(periods, now, loc)
	return estimate.midpoint, estimate.valid
}

// estimateSleepTiming uses a trimmed circular mean and retains dispersion
// metadata for forecast-confidence decisions. Broken sleep contributes one
// episode midpoint, not one midpoint per fragment.
func estimateSleepTiming(periods []engine.SleepPeriod, now time.Time, loc *time.Location) sleepTimingEstimate {
	cutoff := now.AddDate(0, 0, -14)

	var angles []float64
	for _, episode := range mainSleepEpisodes(periods) {
		if episode.end.Before(cutoff) {
			continue
		}
		dur := episode.end.Sub(episode.start)
		if dur < 3*time.Hour || dur > 16*time.Hour {
			continue // skip implausible or isolated short episodes
		}
		mid := episode.start.In(loc).Add(dur / 2)
		h := float64(mid.Hour()) + float64(mid.Minute())/60.0
		angles = append(angles, h*2*math.Pi/24.0)
	}

	if len(angles) < 5 {
		return sleepTimingEstimate{nights: len(angles)}
	}

	meanAng, rBar := circularMeanAndR(angles)

	if rBar > 0.1 {
		circSD := math.Sqrt(-2 * math.Log(rBar))
		threshold := 2 * circSD
		trimmed := make([]float64, 0, len(angles))
		for _, a := range angles {
			if circAngDist(a, meanAng) <= threshold {
				trimmed = append(trimmed, a)
			}
		}
		if len(trimmed) >= 5 {
			angles = trimmed
			meanAng, rBar = circularMeanAndR(angles)
		}
	}

	if rBar < 0.3 {
		return sleepTimingEstimate{rBar: rBar, nights: len(angles)}
	}

	meanHour := meanAng * 24.0 / (2 * math.Pi)
	if meanHour < 0 {
		meanHour += 24.0
	}
	sdHours := math.Sqrt(-2*math.Log(rBar)) * 24 / (2 * math.Pi)
	return sleepTimingEstimate{
		midpoint: meanHour,
		rBar:     rBar,
		sdHours:  sdHours,
		nights:   len(angles),
		valid:    true,
	}
}

func currentSleepMidpoint(periods []engine.SleepPeriod, wake time.Time, loc *time.Location) (float64, bool) {
	for _, episode := range mainSleepEpisodes(periods) {
		if !episode.end.Equal(wake) {
			continue
		}
		midpoint := episode.start.In(loc).Add(episode.end.Sub(episode.start) / 2)
		return float64(midpoint.Hour()) + float64(midpoint.Minute())/60, true
	}
	return 0, false
}

func circularHourDistance(a, b float64) float64 {
	difference := math.Mod(a-b+36, 24) - 12
	return math.Abs(difference)
}

// GoogleHealthLastAttempt returns the latest Google Health API check, falling back to the
// last successful sync for records created before attempt tracking existed.
func GoogleHealthLastAttempt(settings *core.Record) time.Time {
	if settings == nil {
		return time.Time{}
	}
	if attempt := settings.GetDateTime("google_health_last_attempt").Time(); !attempt.IsZero() {
		return attempt
	}
	return settings.GetDateTime("google_health_last_sync").Time()
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
