package services

import (
	"sort"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

// ConvertSleepRecords converts PocketBase records into engine SleepRecords and
// SleepPeriods, merging overlapping records.
func ConvertSleepRecords(records []*core.Record) ([]engine.SleepRecord, []engine.SleepPeriod) {
	if len(records) == 0 {
		return nil, nil
	}

	type rawPeriod struct {
		start time.Time
		end   time.Time
		deep  int
		rem   int
		light int
		awake int
	}

	raw := make([]rawPeriod, len(records))
	for i, r := range records {
		raw[i] = rawPeriod{
			start: r.GetDateTime("sleep_start").Time(),
			end:   r.GetDateTime("sleep_end").Time(),
			deep:  r.GetInt("deep_minutes"),
			rem:   r.GetInt("rem_minutes"),
			light: r.GetInt("light_minutes"),
			awake: r.GetInt("awake_minutes"),
		}
	}

	sort.Slice(raw, func(i, j int) bool {
		return raw[i].start.Before(raw[j].start)
	})

	groups := []rawPeriod{raw[0]}

	for _, p := range raw[1:] {
		last := &groups[len(groups)-1]
		if !p.start.After(last.end) {
			if p.end.After(last.end) {
				last.end = p.end
			}
			last.deep = max(last.deep, p.deep)
			last.rem = max(last.rem, p.rem)
			last.light = max(last.light, p.light)
			last.awake = max(last.awake, p.awake)
		} else {
			groups = append(groups, p)
		}
	}

	engineRecords := make([]engine.SleepRecord, len(groups))
	periods := make([]engine.SleepPeriod, len(groups))
	for i, g := range groups {
		engineRecords[i] = engine.SleepRecord{
			Date:            g.start,
			SleepStart:      g.start,
			SleepEnd:        g.end,
			DurationMinutes: int(g.end.Sub(g.start).Minutes()),
		}
		periods[i] = engine.SleepPeriod{Start: g.start, End: g.end}
	}

	return engineRecords, periods
}
