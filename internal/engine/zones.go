package engine

import (
	"math"
	"time"
)

// Zone names for the energy schedule.
const (
	ZoneSleepInertia   = "sleep_inertia"
	ZoneMorningPeak    = "morning_peak"
	ZoneAfternoonDip   = "afternoon_dip"
	ZoneEveningPeak    = "evening_peak"
	ZoneWindDown       = "wind_down"
	ZoneMelatoninWindow = "melatonin_window"
	ZoneNormal         = "normal"
	ZoneSleep          = "sleep"
)

// Schedule holds the classified energy zones and derived times for a day.
type Schedule struct {
	Points           []EnergyPoint `json:"points"`
	WakeTime         time.Time     `json:"wake_time"`
	CaffeineCutoff   time.Time     `json:"caffeine_cutoff"`
	OptimalNapStart  time.Time     `json:"optimal_nap_start"`
	OptimalNapEnd    time.Time     `json:"optimal_nap_end"`
	MelatoninWindow  time.Time     `json:"melatonin_window"`
	BestFocusStart   time.Time     `json:"best_focus_start"`
	BestFocusEnd     time.Time     `json:"best_focus_end"`
}

// ClassifyZones assigns energy zone labels to each point and derives key times.
// wakeTime is when the user woke up today.
func ClassifyZones(points []EnergyPoint, wakeTime time.Time) Schedule {
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
		idx    int
		isMax  bool
		value  float64
		time   time.Time
	}
	var extrema []extremum

	postInertia := filterAfter(wakePoints, classified, inertiaEnd)
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
		if morningPeak == nil && e.isMax {
			morningPeak = e
		} else if morningPeak != nil && afternoonDip == nil && !e.isMax {
			afternoonDip = e
		} else if afternoonDip != nil && eveningPeak == nil && e.isMax {
			eveningPeak = e
		}
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

	// Wind-down: after evening peak, when alertness drops below morning peak * 0.7.
	if morningPeak != nil && eveningPeak != nil {
		windDownThreshold := morningPeak.value * 0.7
		for i := range classified {
			p := &classified[i]
			if p.Time.After(eveningPeak.time) && p.Alertness < windDownThreshold && p.Zone == ZoneNormal {
				if p.Time.Before(melStart) {
					p.Zone = ZoneWindDown
				}
			}
		}
	}

	sched := Schedule{
		Points:          classified,
		WakeTime:        wakeTime,
		MelatoninWindow: melStart,
		CaffeineCutoff:  melStart.Add(-10 * time.Hour),
	}

	// Optimal nap window: centered on afternoon dip.
	if afternoonDip != nil {
		sched.OptimalNapStart = afternoonDip.time.Add(-30 * time.Minute)
		sched.OptimalNapEnd = afternoonDip.time.Add(30 * time.Minute)
	}

	// Best focus hours: morning peak zone boundaries.
	if morningPeak != nil {
		sched.BestFocusStart = morningPeak.time.Add(-60 * time.Minute)
		sched.BestFocusEnd = morningPeak.time.Add(60 * time.Minute)
	}

	return sched
}

func filterAfter(indices []int, points []EnergyPoint, after time.Time) []int {
	var result []int
	for _, idx := range indices {
		if !points[idx].Time.Before(after) {
			result = append(result, idx)
		}
	}
	return result
}

func isNearExtremum(t, extremumTime time.Time, window time.Duration) bool {
	diff := math.Abs(t.Sub(extremumTime).Minutes())
	return diff <= window.Minutes()
}
