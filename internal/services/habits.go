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

// HabitPreset defines a science-backed habit suggestion that users can enable
// with a single click. Each preset maps to an anchor point from the energy
// schedule and includes a description of why it matters.
type HabitPreset struct {
	Key           string // stable identifier (e.g. "morning_light")
	Name          string // display name
	Anchor        string // schedule anchor
	OffsetMinutes int    // offset from anchor
	Description   string // short science-backed rationale
	Icon          string // emoji for display
	Category      string // grouping: "morning", "afternoon", "evening"
}

// Presets returns the curated list of science-backed habit suggestions.
// These are derived from RISE's timed habits and circadian research:
//   - Huberman (2021): morning light exposure within 30-60 min of waking
//   - Wehr et al. (2001): light exposure timing shapes circadian phase
//   - Wright et al. (2013): meal timing affects peripheral circadian clocks
//   - Drake et al. (2013): caffeine 6h before bed disrupts sleep by ~1h
func Presets() []HabitPreset {
	return []HabitPreset{
		{
			Key:           "morning_light",
			Name:          "Morning Light",
			Anchor:        "morning_wake",
			OffsetMinutes: 0,
			Description:   "Get bright light within 30 min of waking. Anchors your circadian clock and suppresses melatonin.",
			Icon:          "\u2600", // ☀
			Category:      "morning",
		},
		{
			Key:           "grogginess_clear",
			Name:          "Grogginess Clears",
			Anchor:        "morning_wake",
			OffsetMinutes: 90,
			Description:   "Sleep inertia fades ~90 min after waking. Delay critical decisions until then.",
			Icon:          "\u25d3", // ◓
			Category:      "morning",
		},
		{
			Key:           "peak_focus",
			Name:          "Peak Focus Window",
			Anchor:        "best_focus",
			OffsetMinutes: 0,
			Description:   "Your highest cognitive performance window. Schedule demanding work here.",
			Icon:          "\u25ce", // ◎
			Category:      "morning",
		},
		{
			Key:           "afternoon_dip",
			Name:          "Afternoon Dip",
			Anchor:        "afternoon_dip",
			OffsetMinutes: 0,
			Description:   "Energy naturally dips here. Good time for a walk, nap, or routine tasks.",
			Icon:          "\u25bd", // ▽
			Category:      "afternoon",
		},
		{
			Key:           "nap_window",
			Name:          "Optimal Nap",
			Anchor:        "nap_window",
			OffsetMinutes: 0,
			Description:   "Best window for a 20-min power nap without disrupting tonight's sleep.",
			Icon:          "\u263e", // ☾
			Category:      "afternoon",
		},
		{
			Key:           "evening_peak",
			Name:          "Evening Peak",
			Anchor:        "evening_peak",
			OffsetMinutes: 0,
			Description:   "Second wind \u2014 your circadian alertness peak. Great for exercise or creative work.",
			Icon:          "\u26a1", // ⚡
			Category:      "afternoon",
		},
		{
			Key:           "caffeine_cutoff",
			Name:          "Caffeine Cutoff",
			Anchor:        "caffeine_cutoff",
			OffsetMinutes: 0,
			Description:   "Last call for caffeine. Its 10h half-life means later cups steal deep sleep.",
			Icon:          "\u2615", // ☕
			Category:      "afternoon",
		},
		{
			Key:           "last_meal",
			Name:          "Last Meal",
			Anchor:        "melatonin_window",
			OffsetMinutes: -180,
			Description:   "Finish eating 3h before your melatonin window. Late meals shift your circadian clock.",
			Icon:          "\U0001F374", // 🍴
			Category:      "evening",
		},
		{
			Key:           "sunset_wind_down",
			Name:          "Sunset Wind-Down",
			Anchor:        "sunset",
			OffsetMinutes: 0,
			Description:   "Sunset signals your SCN to begin the transition to sleep mode. Start dimming lights.",
			Icon:          "\U0001F305", // 🌅
			Category:      "evening",
		},
		{
			Key:           "blue_light_cutoff",
			Name:          "Screens Off",
			Anchor:        "melatonin_window",
			OffsetMinutes: -120,
			Description:   "Blue light suppresses melatonin. Dim screens or use night mode 2h before your window.",
			Icon:          "\U0001F4F5", // 📵
			Category:      "evening",
		},
		{
			Key:           "melatonin_window",
			Name:          "Melatonin Window",
			Anchor:        "melatonin_window",
			OffsetMinutes: 0,
			Description:   "Your body begins producing melatonin. The ideal time to fall asleep starts now.",
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
		}
	}
	return active
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
	{"best_focus", "Best focus window"},
	{"morning_peak", "Morning peak"},
	{"afternoon_dip", "Afternoon dip"},
	{"nap_window", "Nap window"},
	{"evening_peak", "Evening peak"},
	{"caffeine_cutoff", "Caffeine cutoff"},
	{"sunset", "Sunset"},
	{"sunrise", "Sunrise"},
	{"melatonin_window", "Melatonin window"},
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
