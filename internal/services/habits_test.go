package services

import (
	"testing"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
)

func TestResolveHabitTime_AllAnchors(t *testing.T) {
	loc := time.FixedZone("EST", -5*3600)
	wake := time.Date(2025, 6, 15, 7, 0, 0, 0, loc)

	schedule := engine.Schedule{
		MorningWake:     wake,
		CaffeineCutoff:  wake.Add(6 * time.Hour),                // 1pm
		MelatoninWindow: wake.Add(14 * time.Hour),               // 9pm
		OptimalNapStart: wake.Add(7 * time.Hour),                // 2pm
		OptimalNapEnd:   wake.Add(8 * time.Hour),                // 3pm
		BestFocusStart:  wake.Add(3 * time.Hour),                // 10am
		BestFocusEnd:    wake.Add(5 * time.Hour),                // 12pm
		MorningPeak:     wake.Add(4 * time.Hour),                // 11am
		AfternoonDip:    wake.Add(7*time.Hour + 30*time.Minute), // 2:30pm
		EveningPeak:     wake.Add(10 * time.Hour),               // 5pm
		Sunrise:         wake.Add(-1 * time.Hour),               // 6am
		Sunset:          wake.Add(13 * time.Hour),               // 8pm
	}

	tests := []struct {
		name  string
		habit Habit
		want  time.Time
	}{
		{"morning_wake", Habit{Anchor: "morning_wake"}, wake},
		{"morning_wake+30", Habit{Anchor: "morning_wake", OffsetMinutes: 30}, wake.Add(30 * time.Minute)},
		{"caffeine_cutoff", Habit{Anchor: "caffeine_cutoff"}, schedule.CaffeineCutoff},
		{"melatonin_window", Habit{Anchor: "melatonin_window"}, schedule.MelatoninWindow},
		{"nap_window", Habit{Anchor: "nap_window"}, schedule.OptimalNapStart},
		{"best_focus", Habit{Anchor: "best_focus"}, schedule.BestFocusStart},
		{"morning_peak", Habit{Anchor: "morning_peak"}, schedule.MorningPeak},
		{"afternoon_dip", Habit{Anchor: "afternoon_dip"}, schedule.AfternoonDip},
		{"evening_peak", Habit{Anchor: "evening_peak"}, schedule.EveningPeak},
		{"sunrise", Habit{Anchor: "sunrise"}, schedule.Sunrise},
		{"sunset", Habit{Anchor: "sunset"}, schedule.Sunset},
		{"melatonin-3h", Habit{Anchor: "melatonin_window", OffsetMinutes: -180}, schedule.MelatoninWindow.Add(-3 * time.Hour)},
		{"custom_14:30", Habit{Anchor: "custom", CustomTime: "14:30"}, time.Date(2025, 6, 15, 14, 30, 0, 0, loc)},
		{"custom_empty", Habit{Anchor: "custom"}, time.Time{}},
		{"unknown_anchor", Habit{Anchor: "nonexistent"}, time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveHabitTime(tt.habit, schedule, loc)
			if !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveHabitTime_ZeroSchedule(t *testing.T) {
	loc := time.UTC
	h := Habit{Anchor: "morning_peak"}
	got := ResolveHabitTime(h, engine.Schedule{}, loc)
	if !got.IsZero() {
		t.Errorf("expected zero time for zero schedule, got %v", got)
	}
}

func TestPresets(t *testing.T) {
	presets := Presets()
	if len(presets) == 0 {
		t.Fatal("expected presets to be non-empty")
	}

	keys := make(map[string]bool)
	for _, p := range presets {
		if p.Key == "" || p.Name == "" || p.Anchor == "" || p.Description == "" {
			t.Errorf("preset %q has empty required field", p.Key)
		}
		if keys[p.Key] {
			t.Errorf("duplicate preset key: %s", p.Key)
		}
		keys[p.Key] = true

		// Verify anchor is valid.
		validAnchor := false
		for _, a := range AllAnchors {
			if a.Value == p.Anchor {
				validAnchor = true
				break
			}
		}
		if !validAnchor {
			t.Errorf("preset %q has invalid anchor %q", p.Key, p.Anchor)
		}
	}
}

func TestPresetByKey(t *testing.T) {
	p := PresetByKey("morning_light")
	if p == nil {
		t.Fatal("expected morning_light preset")
	}
	if p.Name != "Morning Light" {
		t.Errorf("got name %q, want Morning Light", p.Name)
	}

	if PresetByKey("nonexistent") != nil {
		t.Error("expected nil for nonexistent key")
	}
}
