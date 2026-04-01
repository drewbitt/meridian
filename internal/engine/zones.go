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

// Schedule holds the classified energy zones and derived times for a day.
type Schedule struct {
	Points          []EnergyPoint `json:"points"`
	WakeTime        time.Time     `json:"wake_time"`
	MorningWake     time.Time     `json:"morning_wake"`
	CaffeineCutoff  time.Time     `json:"caffeine_cutoff"`
	OptimalNapStart time.Time     `json:"optimal_nap_start"`
	OptimalNapEnd   time.Time     `json:"optimal_nap_end"`
	MelatoninWindow time.Time     `json:"melatonin_window"`
	BestFocusStart  time.Time     `json:"best_focus_start"`
	BestFocusEnd    time.Time     `json:"best_focus_end"`
	MorningPeak     time.Time     `json:"morning_peak"`
	AfternoonDip    time.Time     `json:"afternoon_dip"`
	EveningPeak     time.Time     `json:"evening_peak"`
	Sunrise         time.Time     `json:"sunrise"`
	Sunset          time.Time     `json:"sunset"`
}

// ClassifyZones assigns energy zone labels to each point and derives key times.
// wakeTime is when the user woke up today (morning wake, not nap wake).
// Optional sleepPeriods are used to detect nap recovery zones.
func ClassifyZones(points []EnergyPoint, wakeTime time.Time, sleepPeriods ...SleepPeriod) Schedule {
	if len(points) == 0 {
		return Schedule{}
	}

	// First pass: classify each point.
	classified := make([]EnergyPoint, len(points))
	copy(classified, points)

	// Find wake-only points (exclude sleep periods).
	var wakePoints []int
	for i, p := range classified {
		if !p.Time.Before(wakeTime) {
			wakePoints = append(wakePoints, i)
		}
	}

	// Detect sleep inertia: from wake time until ~90 min or W decays.
	inertiaEnd := wakeTime.Add(90 * time.Minute)
	for _, idx := range wakePoints {
		p := &classified[idx]
		if p.Time.Before(inertiaEnd) {
			// Check if alertness is still depressed (W component still significant).
			// Heuristic: if KSS > 5 in the first 90 min, it's inertia.
			if p.KSS > 5.0 || p.Time.Sub(wakeTime) < 30*time.Minute {
				p.Zone = ZoneSleepInertia
			}
		}
	}

	// Find local extrema in wake points after inertia.
	type extremum struct {
		idx   int
		isMax bool
		value float64
		time  time.Time
	}
	var extrema []extremum

	var postInertia []int
	for _, idx := range wakePoints {
		if !classified[idx].Time.Before(inertiaEnd) {
			postInertia = append(postInertia, idx)
		}
	}
	for i := 1; i < len(postInertia)-1; i++ {
		prev := classified[postInertia[i-1]].Alertness
		curr := classified[postInertia[i]].Alertness
		next := classified[postInertia[i+1]].Alertness
		if curr > prev && curr > next {
			extrema = append(extrema, extremum{postInertia[i], true, curr, classified[postInertia[i]].Time})
		} else if curr < prev && curr < next {
			extrema = append(extrema, extremum{postInertia[i], false, curr, classified[postInertia[i]].Time})
		}
	}

	// Identify morning peak (first max), afternoon dip (first min after morning peak),
	// and evening peak (first max after afternoon dip).
	var morningPeak, afternoonDip, eveningPeak *extremum
	for i := range extrema {
		e := &extrema[i]
		switch {
		case morningPeak == nil && e.isMax:
			morningPeak = e
		case morningPeak != nil && afternoonDip == nil && !e.isMax:
			afternoonDip = e
		case afternoonDip != nil && eveningPeak == nil && e.isMax:
			eveningPeak = e
		}
	}

	// Robust dip detection: if we found two peaks but no strict local minimum
	// between them (common when C's rise masks U's dip), find the minimum
	// value between the two peaks and use that as the afternoon dip. This
	// captures the "plateau" that the FIPS model produces between the morning
	// and evening energy peaks.
	if morningPeak != nil && afternoonDip == nil {
		// Find the second peak (first max after morning peak that has higher value,
		// or the one closest to evening).
		var secondPeak *extremum
		for i := range extrema {
			e := &extrema[i]
			if e.isMax && e.time.After(morningPeak.time) {
				secondPeak = e
				break
			}
		}
		if secondPeak != nil {
			// Find minimum between the two peaks.
			minVal := math.MaxFloat64
			var minIdx int
			for _, idx := range postInertia {
				p := classified[idx]
				if p.Time.After(morningPeak.time) && p.Time.Before(secondPeak.time) && p.Alertness < minVal {
					minVal = p.Alertness
					minIdx = idx
				}
			}
			if minVal < math.MaxFloat64 {
				afternoonDip = &extremum{minIdx, false, minVal, classified[minIdx].Time}
				eveningPeak = secondPeak
			}
		}
	}

	// If still no two peaks found but we have at least one peak and enough
	// post-inertia data, detect an implicit dip as the minimum in the middle
	// third of the wake period and use global max as evening peak.
	if morningPeak == nil && len(postInertia) > 12 {
		// Use the overall global max as the sole peak.
		var globalMax *extremum
		for _, idx := range postInertia {
			p := classified[idx]
			if globalMax == nil || p.Alertness > globalMax.value {
				globalMax = &extremum{idx, true, p.Alertness, p.Time}
			}
		}
		morningPeak = globalMax
	}

	// Assign zones to unclassified points.
	for i := range classified {
		p := &classified[i]
		if p.Zone != "" {
			continue // already classified (inertia or sleep)
		}
		if p.Time.Before(wakeTime) {
			p.Zone = ZoneSleep
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

	// Melatonin window: 14-16h after wake time.
	melStart := wakeTime.Add(14 * time.Hour)
	melEnd := wakeTime.Add(16 * time.Hour)
	for i := range classified {
		p := &classified[i]
		if !p.Time.Before(melStart) && p.Time.Before(melEnd) {
			p.Zone = ZoneMelatoninWindow
		}
	}

	// Wind-down: after the first local peak (evening peak when present, or the
	// global circadian peak), mark points where alertness drops below 70% of
	// that peak value.
	peakForWindDown := eveningPeak
	if peakForWindDown == nil && morningPeak != nil {
		peakForWindDown = morningPeak
	}
	if peakForWindDown != nil && peakForWindDown.value > 0 {
		windDownThreshold := peakForWindDown.value * 0.7
		for i := range classified {
			p := &classified[i]
			if p.Time.After(peakForWindDown.time) && p.Alertness < windDownThreshold && p.Zone == ZoneNormal {
				if p.Time.Before(melStart) {
					p.Zone = ZoneWindDown
				}
			}
		}
	}

	// Mark nap recovery zones: 30 minutes after each nap ends.
	// Nap recovery overwrites normal and afternoon_dip zones since the user
	// just woke from a nap — that's the most relevant context.
	for _, sp := range sleepPeriods {
		if !sp.IsNap {
			continue
		}
		napEnd := sp.End
		recoveryEnd := napEnd.Add(30 * time.Minute)
		for i := range classified {
			p := &classified[i]
			if !p.Time.Before(napEnd) && p.Time.Before(recoveryEnd) {
				if p.Zone == ZoneNormal || p.Zone == ZoneAfternoonDip {
					p.Zone = ZoneNapRecovery
				}
			}
		}
	}

	sched := Schedule{
		Points:          classified,
		WakeTime:        wakeTime,
		MorningWake:     wakeTime,
		MelatoninWindow: melStart,
		CaffeineCutoff:  melStart.Add(-10 * time.Hour),
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

	// Best focus: 2h window centred on the morning peak. We prefer the morning
	// peak (the homeostatic peak, where S is still high and inertia has cleared)
	// over the evening peak (the circadian peak at ~5pm). While the true
	// circadian alertness maximum is in the late afternoon, the morning window
	// is more actionable for knowledge work and aligns with how RISE presents
	// focus windows. If morning peak was detected, use it directly. Otherwise
	// fall back to the global max between inertia end and noon+3h.
	var peakPoint *EnergyPoint
	if morningPeak != nil {
		peakPoint = &classified[morningPeak.idx]
	} else {
		// Fallback: global max in the morning-to-early-afternoon window.
		earlyAfternoon := wakeTime.Add(8 * time.Hour) // roughly noon-ish for typical wakers
		for i := range classified {
			p := &classified[i]
			if p.Time.Before(inertiaEnd) || p.Time.After(earlyAfternoon) {
				continue
			}
			if peakPoint == nil || p.Alertness > peakPoint.Alertness {
				peakPoint = p
			}
		}
	}
	// If still nothing found, use global max before melatonin.
	if peakPoint == nil {
		for i := range classified {
			p := &classified[i]
			if p.Time.Before(inertiaEnd) || !p.Time.Before(melStart) {
				continue
			}
			if peakPoint == nil || p.Alertness > peakPoint.Alertness {
				peakPoint = p
			}
		}
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

func isNearExtremum(t, extremumTime time.Time, window time.Duration) bool {
	diff := math.Abs(t.Sub(extremumTime).Minutes())
	return diff <= window.Minutes()
}
