// Package engine implements the FIPS Three Process Model (TPM) for circadian
// rhythm alertness prediction. The model combines homeostatic sleep pressure (S),
// circadian rhythm (C), ultradian rhythm (U), and sleep inertia (W) to produce
// a continuous alertness curve.
package engine

import (
	"math"
	"sort"
	"time"
)

// SleepPeriod represents a single sleep interval.
type SleepPeriod struct {
	Start time.Time
	End   time.Time
}

// EnergyPoint is a single sample on the predicted alertness curve.
type EnergyPoint struct {
	Time      time.Time `json:"time"`
	Alertness float64   `json:"alertness"`
	KSS       float64   `json:"kss"`  // Karolinska Sleepiness Scale (1=alert, 9=very sleepy)
	Zone      string    `json:"zone"` // Named energy zone
}

// TPM parameters (FIPS reference values).
const (
	// Process S – homeostatic sleep pressure
	sLowerAsymptote = 2.4    // la: lower asymptote during wake
	sDecayRate      = -0.0514 // d: decay rate during wake (per hour)
	sBreakLevel     = 14.0   // bl: linear→exponential recovery breakpoint
	sRecoveryLinear = 0.8    // rs: linear recovery rate (units/hr) during early sleep
	sUpperAsymptote = 16.0   // ha: upper asymptote during sleep recovery
	sRecoveryRate   = 0.4    // r: exponential recovery rate during late sleep

	// Process C – 24h circadian
	cMean      = 0.0
	cAmplitude = 2.5
	cAcrophase = 16.8 // hours after midnight

	// Process U – 12h ultradian (post-lunch dip)
	uMean      = 0.0
	uAmplitude = 0.5
	uPhaseShift = 3.0 // hours relative to C acrophase

	// Process W – sleep inertia
	wCoefficient = -5.72
	wDecayRate   = -1.51 // per hour (~27 min half-life)

	// Initial S value for a well-rested person
	sInitial = 15.0

	// Sampling interval
	sampleMinutes = 5
)

// PredictEnergy generates an alertness curve from the given sleep history.
// It produces one EnergyPoint every 5 minutes from predStart to predEnd.
func PredictEnergy(sleepPeriods []SleepPeriod, predStart, predEnd time.Time) []EnergyPoint {
	if predEnd.Before(predStart) || predEnd.Equal(predStart) {
		return nil
	}

	// Sort sleep periods chronologically.
	periods := make([]SleepPeriod, len(sleepPeriods))
	copy(periods, sleepPeriods)
	sort.Slice(periods, func(i, j int) bool {
		return periods[i].Start.Before(periods[j].Start)
	})

	// Build the timeline: we need to simulate from the earliest sleep period
	// (or predStart, whichever is earlier) to predEnd.
	simStart := predStart
	if len(periods) > 0 && periods[0].Start.Before(simStart) {
		simStart = periods[0].Start
	}

	// State variables
	s := sInitial                 // homeostatic pressure
	var lastWakeTime *time.Time   // when the person last woke up
	sleeping := false
	sleepPhase2 := false          // true once S >= breakLevel during sleep
	phase2Start := time.Time{}

	// Determine initial sleep/wake state at simStart.
	for _, sp := range periods {
		if !simStart.Before(sp.Start) && simStart.Before(sp.End) {
			sleeping = true
			break
		}
	}

	// If awake at simStart, find the most recent wake time.
	if !sleeping {
		wt := simStart
		for _, sp := range periods {
			if sp.End.Before(simStart) || sp.End.Equal(simStart) {
				wt = sp.End
			}
		}
		lastWakeTime = &wt
	}

	step := time.Duration(sampleMinutes) * time.Minute
	var points []EnergyPoint

	for t := simStart; t.Before(predEnd); t = t.Add(step) {
		// Check if we transition between sleep/wake at this step.
		nowSleeping := isAsleep(t, periods)

		if sleeping && !nowSleeping {
			// Just woke up
			wt := t
			lastWakeTime = &wt
			sleepPhase2 = false
		}
		if !sleeping && nowSleeping {
			// Just fell asleep
			sleepPhase2 = false
		}
		sleeping = nowSleeping

		dt := float64(sampleMinutes) / 60.0 // hours

		// Update Process S
		if sleeping {
			if !sleepPhase2 && s < sBreakLevel {
				// Phase 1: linear recovery
				s += sRecoveryLinear * dt
				if s >= sBreakLevel {
					sleepPhase2 = true
					phase2Start = t
				}
			} else {
				if !sleepPhase2 {
					sleepPhase2 = true
					phase2Start = t
				}
				// Phase 2: exponential recovery toward upper asymptote
				tSleep := t.Sub(phase2Start).Hours()
				s = sUpperAsymptote - (sUpperAsymptote-sBreakLevel)*math.Exp(-sRecoveryRate*tSleep)
			}
		} else {
			// Wake: S decays toward lower asymptote
			s = sLowerAsymptote + (s-sLowerAsymptote)*math.Exp(sDecayRate*dt)
		}

		// Process C: 24h circadian
		tod := timeOfDay(t)
		c := cMean + cAmplitude*math.Cos(2*math.Pi/24.0*(tod-cAcrophase))

		// Process U: 12h ultradian
		u := uMean + uAmplitude*math.Cos(2*math.Pi/12.0*(tod-cAcrophase-uPhaseShift))

		// Process W: sleep inertia (only during wake)
		w := 0.0
		if !sleeping && lastWakeTime != nil {
			hoursAwake := t.Sub(*lastWakeTime).Hours()
			if hoursAwake < 3.0 { // W is negligible after ~3 hours
				w = wCoefficient * math.Exp(wDecayRate*hoursAwake)
			}
		}

		alertness := s + c + u + w

		// Only emit points in the requested prediction window.
		if !t.Before(predStart) {
			kss := alertnessToKSS(alertness)
			points = append(points, EnergyPoint{
				Time:      t,
				Alertness: math.Round(alertness*100) / 100,
				KSS:       math.Round(kss*100) / 100,
			})
		}
	}

	return points
}

// alertnessToKSS converts the TPM alertness value to the Karolinska Sleepiness Scale.
// KSS ranges from 1 (extremely alert) to 9 (extremely sleepy).
func alertnessToKSS(alertness float64) float64 {
	kss := 10.6 - 0.6*alertness
	return math.Max(1, math.Min(9, kss))
}

// timeOfDay returns fractional hours since midnight in local time.
func timeOfDay(t time.Time) float64 {
	h, m, sec := t.Clock()
	return float64(h) + float64(m)/60.0 + float64(sec)/3600.0
}

// isAsleep returns true if time t falls within any sleep period.
func isAsleep(t time.Time, periods []SleepPeriod) bool {
	for _, sp := range periods {
		if !t.Before(sp.Start) && t.Before(sp.End) {
			return true
		}
	}
	return false
}
