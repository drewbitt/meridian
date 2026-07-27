package services

import (
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

// Habit is an in-memory representation of a habit record.
type Habit struct {
	ID            string
	Name          string
	Anchor        string
	OffsetMinutes int
	CustomTime    string
	Notify        bool
	Enabled       bool
}

// HabitPreset defines a planning suggestion that users can enable with a
// single click. Some presets use observed wake/solar anchors; others resolve
// only when today's model supports the corresponding feature.
type HabitPreset struct {
	Key           string // stable identifier (e.g. "morning_light")
	Name          string // display name
	Anchor        string // schedule anchor
	OffsetMinutes int    // offset from anchor
	Description   string // short, uncertainty-aware rationale
	Icon          string // emoji for display
	Category      string // grouping: "morning", "afternoon", "evening"
}

// Presets returns conservative planning cues. Wording deliberately separates
// measured events from model-derived estimates and avoids universal claims
// about peaks, dips, melatonin onset, or individual cognitive performance.
func Presets() []HabitPreset {
	return []HabitPreset{
		{
			Key:           "morning_light",
			Name:          "Morning Light",
			Anchor:        "morning_wake",
			OffsetMinutes: 0,
			Description:   "Seek bright light soon after waking when practical. Morning light can help reinforce a regular sleep-wake schedule.",
			Icon:          "\u2600", // ☀
			Category:      "morning",
		},
		{
			Key:           "grogginess_clear",
			Name:          "Grogginess Clears",
			Anchor:        "morning_wake",
			OffsetMinutes: 90,
			Description:   "Sleep inertia often eases over the first 30–90 minutes. Give yourself extra margin if you still feel groggy.",
			Icon:          "\u25d3", // ◓
			Category:      "morning",
		},
		{
			Key:           "peak_focus",
			Name:          "Modeled High-Energy Window",
			Anchor:        "best_focus",
			OffsetMinutes: 0,
			Description:   "The model's highest alertness interval today. Use it as a planning hint and compare it with how you actually feel.",
			Icon:          "\u25ce", // ◎
			Category:      "morning",
		},
		{
			Key:           "afternoon_dip",
			Name:          "Afternoon Dip",
			Anchor:        "afternoon_dip",
			OffsetMinutes: 0,
			Description:   "Shown only when today's curve has a clear two-peak dip. Consider lighter work or a walk if it matches how you feel.",
			Icon:          "\u25bd", // ▽
			Category:      "afternoon",
		},
		{
			Key:           "nap_window",
			Name:          "Optional Short Nap",
			Anchor:        "nap_window",
			OffsetMinutes: 0,
			Description:   "Available only with a clear modeled dip. A short nap may help, but late or long naps can make nighttime sleep harder.",
			Icon:          "\u263e", // ☾
			Category:      "afternoon",
		},
		{
			Key:           "evening_peak",
			Name:          "Modeled Second High",
			Anchor:        "evening_peak",
			OffsetMinutes: 0,
			Description:   "Shown only when the model detects a distinct second high. Treat it as a planning cue, not a guaranteed second wind.",
			Icon:          "\u26a1", // ⚡
			Category:      "afternoon",
		},
		{
			Key:           "caffeine_cutoff",
			Name:          "Caffeine Cutoff",
			Anchor:        "caffeine_cutoff",
			OffsetMinutes: 0,
			Description:   "A conservative cutoff about 10 hours before target sleep. Caffeine response and half-life vary substantially by person.",
			Icon:          "\u2615", // ☕
			Category:      "afternoon",
		},
		{
			Key:           "last_meal",
			Name:          "Last Meal",
			Anchor:        "melatonin_window",
			OffsetMinutes: -180,
			Description:   "A planning cue to finish a large meal about 3 hours before estimated wind-down. Adjust for health needs and comfort.",
			Icon:          "\U0001F374", // 🍴
			Category:      "evening",
		},
		{
			Key:           "sunset_wind_down",
			Name:          "Sunset Wind-Down",
			Anchor:        "sunset",
			OffsetMinutes: 0,
			Description:   "Use local sunset as an optional cue to begin reducing bright light. It is not a measurement of your actual light exposure.",
			Icon:          "\U0001F305", // 🌅
			Category:      "evening",
		},
		{
			Key:           "blue_light_cutoff",
			Name:          "Screens Off",
			Anchor:        "melatonin_window",
			OffsetMinutes: -120,
			Description:   "Reduce bright, close-up light before target sleep if it helps you unwind; brightness and duration both matter.",
			Icon:          "\U0001F4F5", // 📵
			Category:      "evening",
		},
		{
			Key:           "melatonin_window",
			Name:          "Estimated Wind-Down",
			Anchor:        "melatonin_window",
			OffsetMinutes: 0,
			Description:   "A planning estimate based on wake time and sleep need—not a measurement of melatonin onset or an exact bedtime.",
			Icon:          "\U0001F319", // 🌙
			Category:      "evening",
		},
	}
}

// PresetByKey returns a preset by its stable key, or nil if not found.
func PresetByKey(key string) *HabitPreset {
	for _, p := range Presets() {
		if p.Key == key {
			return &p
		}
	}
	return nil
}

// ActivePresetKeys returns the set of preset keys that the user has already
// created as habits (matched by name).
func ActivePresetKeys(habits []*core.Record) map[string]bool {
	nameSet := make(map[string]bool, len(habits))
	for _, h := range habits {
		nameSet[h.GetString("name")] = true
	}
	active := make(map[string]bool)
	for _, p := range Presets() {
		if nameSet[p.Name] {
			active[p.Key] = true
			continue
		}
		for _, oldName := range legacyPresetNames[p.Key] {
			if nameSet[oldName] {
				active[p.Key] = true
				break
			}
		}
	}
	return active
}

var legacyPresetNames = map[string][]string{
	"peak_focus":       {"Peak Focus Window"},
	"nap_window":       {"Optimal Nap"},
	"evening_peak":     {"Evening Peak"},
	"melatonin_window": {"Melatonin Window"},
}

// GetUserHabits loads all enabled habits for a user.
func GetUserHabits(app core.App, userID string) ([]Habit, error) {
	records, err := app.FindRecordsByFilter(
		"habits",
		"user = {:user} && enabled = true",
		"name", 0, 0,
		map[string]any{"user": userID},
	)
	if err != nil {
		return nil, err
	}
	habits := make([]Habit, len(records))
	for i, r := range records {
		habits[i] = Habit{
			ID:            r.Id,
			Name:          r.GetString("name"),
			Anchor:        r.GetString("anchor"),
			OffsetMinutes: r.GetInt("offset_minutes"),
			CustomTime:    r.GetString("custom_time"),
			Notify:        r.GetBool("notify"),
			Enabled:       r.GetBool("enabled"),
		}
	}
	return habits, nil
}

// AllAnchors returns the full set of valid anchor names for habit forms.
var AllAnchors = []struct {
	Value string
	Label string
}{
	{"morning_wake", "Morning wake"},
	{"best_focus", "Modeled high-energy window"},
	{"morning_peak", "Modeled first high"},
	{"afternoon_dip", "Modeled two-peak dip"},
	{"nap_window", "Optional short-nap window"},
	{"evening_peak", "Modeled second high"},
	{"caffeine_cutoff", "Caffeine cutoff"},
	{"sunset", "Sunset"},
	{"sunrise", "Sunrise"},
	{"melatonin_window", "Estimated wind-down"},
	{"custom", "Custom time"},
}

// ResolveHabitTime computes the absolute time for a habit based on its anchor
// and the current schedule. Returns zero time if the anchor can't be resolved.
func ResolveHabitTime(h Habit, schedule engine.Schedule, loc *time.Location) time.Time {
	var base time.Time
	switch h.Anchor {
	case "morning_wake":
		base = schedule.MorningWake
	case "caffeine_cutoff":
		base = schedule.CaffeineCutoff
	case "melatonin_window":
		base = schedule.MelatoninWindow
	case "nap_window":
		base = schedule.OptimalNapStart
	case "best_focus":
		base = schedule.BestFocusStart
	case "morning_peak":
		base = schedule.MorningPeak
	case "afternoon_dip":
		base = schedule.AfternoonDip
	case "evening_peak":
		base = schedule.EveningPeak
	case "sunrise":
		base = schedule.Sunrise
	case "sunset":
		base = schedule.Sunset
	case "custom":
		if h.CustomTime == "" {
			return time.Time{}
		}
		today := schedule.MorningWake
		if today.IsZero() {
			return time.Time{}
		}
		t, err := time.ParseInLocation("15:04", h.CustomTime, loc)
		if err != nil {
			return time.Time{}
		}
		base = time.Date(today.Year(), today.Month(), today.Day(), t.Hour(), t.Minute(), 0, 0, loc)
		return base // custom doesn't use offset
	}

	if base.IsZero() {
		return time.Time{}
	}
	return base.Add(time.Duration(h.OffsetMinutes) * time.Minute)
}

// ResolvedHabit pairs a Habit with its computed time for display/notification.
type ResolvedHabit struct {
	Habit Habit
	Time  time.Time
}

// ResolveAllHabits loads and resolves all enabled habits for a user against
// the given schedule. Returns habits sorted by time, skipping any that
// can't be resolved.
func ResolveAllHabits(app core.App, userID string, schedule engine.Schedule, loc *time.Location) []ResolvedHabit {
	habits, err := GetUserHabits(app, userID)
	if err != nil {
		return nil
	}
	var resolved []ResolvedHabit
	for _, h := range habits {
		t := ResolveHabitTime(h, schedule, loc)
		if t.IsZero() {
			continue
		}
		resolved = append(resolved, ResolvedHabit{Habit: h, Time: t})
	}
	// Sort by time.
	for i := 1; i < len(resolved); i++ {
		for j := i; j > 0 && resolved[j].Time.Before(resolved[j-1].Time); j-- {
			resolved[j], resolved[j-1] = resolved[j-1], resolved[j]
		}
	}
	return resolved
}
