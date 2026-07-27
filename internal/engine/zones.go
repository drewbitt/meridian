package engine

import (
	"math"
	"time"
)

// Zone names for the energy schedule.
const (
	ZoneSleepInertia    = "sleep_inertia"
	ZoneMorningPeak     = "morning_peak"
	ZoneAfternoonDip    = "afternoon_dip"
	ZoneEveningPeak     = "evening_peak"
	ZoneWindDown        = "wind_down"
	ZoneMelatoninWindow = "melatonin_window"
	ZoneNapRecovery     = "nap_recovery"
	ZoneNormal          = "normal"
	ZoneSleep           = "sleep"
)

// ForecastConfidence describes how personalized a daily schedule is.
type ForecastConfidence string

// Forecast-confidence values describe how much recent personal sleep supports
// the timing forecast.
const (
	ConfidenceNone        ForecastConfidence = "none"
	ConfidenceLow         ForecastConfidence = "low"
	ConfidencePreliminary ForecastConfidence = "preliminary"
	ConfidenceModerate    ForecastConfidence = "moderate"
	ConfidenceHigh        ForecastConfidence = "high"
)

// Energy-pattern values describe only displayable curve shape, not a measured
// biological rhythm.
const (
	PatternUnclear = "unclear"
	PatternOnePeak = "one_peak"
	PatternTwoPeak = "two_peak"
)

// A 0.5-point KSS change is a conservative product-display policy, not a
// claim about a biological detection limit. The FIPS transform is
// KSS = 10.6 - 0.6*alertness, so this is 0.83 alertness units. Keeping this
// explicit prevents sub-tenth-KSS plateaus from being marketed as a real dip.
const minimumDisplayedKSSProminence = 0.5

// Schedule holds the classified energy zones and derived times for a day.
type Schedule struct {
	Points            []EnergyPoint      `json:"points"`
	WakeTime          time.Time          `json:"wake_time"`
	MorningWake       time.Time          `json:"morning_wake"`
	CaffeineCutoff    time.Time          `json:"caffeine_cutoff"`
	OptimalNapStart   time.Time          `json:"optimal_nap_start"`
	OptimalNapEnd     time.Time          `json:"optimal_nap_end"`
	MelatoninWindow   time.Time          `json:"melatonin_window"`
	BestFocusStart    time.Time          `json:"best_focus_start"`
	BestFocusEnd      time.Time          `json:"best_focus_end"`
	MorningPeak       time.Time          `json:"morning_peak"`
	AfternoonDip      time.Time          `json:"afternoon_dip"`
	EveningPeak       time.Time          `json:"evening_peak"`
	Sunrise           time.Time          `json:"sunrise"`
	Sunset            time.Time          `json:"sunset"`
	Confidence        ForecastConfidence `json:"confidence"`
	ConfidenceReason  string             `json:"confidence_reason"`
	ObservedNights    int                `json:"observed_nights"`
	IsEstimate        bool               `json:"is_estimate"`
	EnergyPattern     string             `json:"energy_pattern"`
	DipProminence     float64            `json:"dip_prominence"`
	AwaitingSleepData bool               `json:"awaiting_sleep_data"`
	LastSync          time.Time          `json:"last_sync"`
}

// ClassifyZones assigns energy zone labels to each point and derives key times.
// wakeTime is when the user woke up today (morning wake, not nap wake).
// Optional sleepPeriods are used to detect nap recovery zones.
func ClassifyZones(points []EnergyPoint, wakeTime time.Time, sleepPeriods ...SleepPeriod) Schedule {
	return ClassifyZonesForSleepNeed(points, wakeTime, 8, sleepPeriods...)
}

// ClassifyZonesForSleepNeed classifies the curve and derives planning anchors
// using the user's stated daily sleep need. The legacy MelatoninWindow field
// stores the start of a two-hour estimated wind-down period; it is not a DLMO
// or other measured melatonin phase marker.
func ClassifyZonesForSleepNeed(points []EnergyPoint, wakeTime time.Time, sleepNeedHours float64, sleepPeriods ...SleepPeriod) Schedule {
	if len(points) == 0 {
		return Schedule{}
	}

	classified := make([]EnergyPoint, len(points))
	copy(classified, points)
	for i := range classified {
		// A cached post-nap recovery label cannot be reconstructed without the
		// original nap interval. Preserve it; recompute every other zone.
		if classified[i].Zone != ZoneNapRecovery {
			classified[i].Zone = ""
		}
	}

	inertiaEnd := wakeTime.Add(90 * time.Minute)
	sleepNeedHours = math.Max(4, math.Min(12, sleepNeedHours))
	targetSleep := wakeTime.Add(time.Duration((24 - sleepNeedHours) * float64(time.Hour)))
	windDownStart := targetSleep.Add(-2 * time.Hour)

	// Mark actual sleep first and collect awake points eligible for shape
	// detection. A nap interval must not be mistaken for a dip or peak.
	var eligible []int
	for i := range classified {
		point := &classified[i]
		if point.Time.Before(wakeTime) || inAnySleep(point.Time, sleepPeriods) {
			point.Zone = ZoneSleep
			continue
		}
		if point.Time.Before(inertiaEnd) {
			if point.KSS > 5 || point.Time.Sub(wakeTime) < 30*time.Minute {
				point.Zone = ZoneSleepInertia
			}
			continue
		}
		if point.Time.Before(windDownStart) {
			eligible = append(eligible, i)
		}
	}

	type extremum struct {
		idx   int
		isMax bool
		value float64
		time  time.Time
	}
	smoothed := smoothAlertness(classified, eligible, 3)
	var maxima []extremum
	for i := 1; i < len(eligible)-1; i++ {
		if smoothed[i] > smoothed[i-1] && smoothed[i] >= smoothed[i+1] {
			idx := eligible[i]
			maxima = append(maxima, extremum{idx, true, smoothed[i], classified[idx].Time})
		}
	}

	var morningPeak, afternoonDip, eveningPeak *extremum
	bestProminence := 0.0
	for i := 0; i < len(maxima); i++ {
		for j := i + 1; j < len(maxima); j++ {
			first, second := maxima[i], maxima[j]
			if second.time.Sub(first.time) < 2*time.Hour {
				continue
			}
			minimum := extremum{value: math.MaxFloat64}
			for k, idx := range eligible {
				pointTime := classified[idx].Time
				if !pointTime.After(first.time) || !pointTime.Before(second.time) {
					continue
				}
				if smoothed[k] < minimum.value {
					minimum = extremum{idx: idx, value: smoothed[k], time: pointTime}
				}
			}
			if minimum.value == math.MaxFloat64 {
				continue
			}
			prominence := math.Min(first.value-minimum.value, second.value-minimum.value)
			if prominence*0.6 >= minimumDisplayedKSSProminence && prominence > bestProminence {
				firstCopy, minimumCopy, secondCopy := first, minimum, second
				morningPeak = &firstCopy
				afternoonDip = &minimumCopy
				eveningPeak = &secondCopy
				bestProminence = prominence
			}
		}
	}

	pattern := PatternTwoPeak
	var solePeak *extremum
	if morningPeak == nil {
		pattern = PatternUnclear
		// Do not label a peak from a tiny partial series. At 5-minute sampling,
		// twelve eligible points provide one hour of post-inertia context.
		if len(eligible) >= 12 {
			for k, idx := range eligible {
				if solePeak == nil || smoothed[k] > solePeak.value {
					solePeak = &extremum{idx: idx, isMax: true, value: smoothed[k], time: classified[idx].Time}
				}
			}
		}
		if solePeak != nil {
			pattern = PatternOnePeak
			if solePeak.time.Before(wakeTime.Add(8 * time.Hour)) {
				morningPeak = solePeak
			} else {
				eveningPeak = solePeak
			}
		}
	}

	for i := range classified {
		p := &classified[i]
		if p.Zone != "" {
			continue
		}
		switch {
		case morningPeak != nil && isNearExtremum(p.Time, morningPeak.time, 60*time.Minute):
			p.Zone = ZoneMorningPeak
		case afternoonDip != nil && isNearExtremum(p.Time, afternoonDip.time, 45*time.Minute):
			p.Zone = ZoneAfternoonDip
		case eveningPeak != nil && isNearExtremum(p.Time, eveningPeak.time, 60*time.Minute):
			p.Zone = ZoneEveningPeak
		default:
			p.Zone = ZoneNormal
		}
	}

	for i := range classified {
		p := &classified[i]
		if p.Zone != ZoneSleep && !p.Time.Before(windDownStart) && p.Time.Before(targetSleep) {
			p.Zone = ZoneMelatoninWindow
		}
	}

	peakForWindDown := eveningPeak
	if peakForWindDown == nil && morningPeak != nil {
		peakForWindDown = morningPeak
	}
	if peakForWindDown != nil && peakForWindDown.value > 0 {
		windDownThreshold := peakForWindDown.value * 0.7
		for i := range classified {
			p := &classified[i]
			if p.Time.After(peakForWindDown.time) && p.Alertness < windDownThreshold && p.Zone == ZoneNormal {
				if p.Time.Before(windDownStart) {
					p.Zone = ZoneWindDown
				}
			}
		}
	}

	for _, sp := range sleepPeriods {
		if !sp.IsNap {
			continue
		}
		napEnd := sp.End
		recoveryEnd := napEnd.Add(30 * time.Minute)
		for i := range classified {
			p := &classified[i]
			if !p.Time.Before(napEnd) && p.Time.Before(recoveryEnd) {
				if p.Zone != ZoneSleep {
					p.Zone = ZoneNapRecovery
				}
			}
		}
	}

	sched := Schedule{
		Points:      classified,
		WakeTime:    wakeTime,
		MorningWake: wakeTime,
		// The persisted JSON names predate the distinction between a planning
		// cue and a biological marker. Keep them for compatibility, while the
		// product labels this as estimated wind-down.
		MelatoninWindow: windDownStart,
		// Conservative planning cutoff: about ten hours before the target
		// sleep time, not a claim that caffeine has a ten-hour half-life.
		CaffeineCutoff: targetSleep.Add(-10 * time.Hour),
		EnergyPattern:  pattern,
		DipProminence:  math.Round(bestProminence*100) / 100,
	}

	// Populate peak/dip times from detected extrema.
	if morningPeak != nil {
		sched.MorningPeak = morningPeak.time
	}
	if afternoonDip != nil {
		sched.AfternoonDip = afternoonDip.time
	}
	if eveningPeak != nil {
		sched.EveningPeak = eveningPeak.time
	}

	// Optimal nap window: centered on afternoon dip.
	if afternoonDip != nil {
		sched.OptimalNapStart = afternoonDip.time.Add(-30 * time.Minute)
		sched.OptimalNapEnd = afternoonDip.time.Add(30 * time.Minute)
	}

	// Use the first clear peak for a two-peak day and the sole modeled maximum
	// for a one-peak day. Do not invent a morning maximum for actionability.
	var peakPoint *EnergyPoint
	if pattern == PatternTwoPeak && morningPeak != nil {
		peakPoint = &classified[morningPeak.idx]
	} else if solePeak != nil {
		peakPoint = &classified[solePeak.idx]
	}
	if peakPoint != nil {
		sched.BestFocusStart = peakPoint.Time.Add(-60 * time.Minute)
		// Don't let BestFocusStart fall within the inertia period.
		if sched.BestFocusStart.Before(inertiaEnd) {
			sched.BestFocusStart = inertiaEnd
		}
		sched.BestFocusEnd = peakPoint.Time.Add(60 * time.Minute)
	}

	return sched
}

func smoothAlertness(points []EnergyPoint, indexes []int, radius int) []float64 {
	smoothed := make([]float64, len(indexes))
	for i := range indexes {
		start := max(0, i-radius)
		end := min(len(indexes)-1, i+radius)
		var total float64
		for j := start; j <= end; j++ {
			total += points[indexes[j]].Alertness
		}
		smoothed[i] = total / float64(end-start+1)
	}
	return smoothed
}

func inAnySleep(t time.Time, periods []SleepPeriod) bool {
	for _, period := range periods {
		if !t.Before(period.Start) && t.Before(period.End) {
			return true
		}
	}
	return false
}

func isNearExtremum(t, extremumTime time.Time, window time.Duration) bool {
	diff := math.Abs(t.Sub(extremumTime).Minutes())
	return diff <= window.Minutes()
}
