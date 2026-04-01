package services

import (
	"sort"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/pocketbase/pocketbase/core"
)

// ConvertSleepRecords converts PocketBase records into engine SleepRecords and
// SleepPeriods, merging overlapping records.
// ConvertSleepRecords converts PocketBase records into engine SleepRecords and
// SleepPeriods, merging overlapping records. Optional loc is the user's timezone
// for nap detection (sleep starting after 10am local). Defaults to UTC.
func ConvertSleepRecords(records []*core.Record, loc ...*time.Location) ([]engine.SleepRecord, []engine.SleepPeriod) {
	userLoc := time.UTC
	if len(loc) > 0 && loc[0] != nil {
		userLoc = loc[0]
	}
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

	var raw []rawPeriod
	for _, r := range records {
		start := r.GetDateTime("sleep_start").Time()
		end := r.GetDateTime("sleep_end").Time()
		// Skip invalid records: zero times or end not after start.
		if start.IsZero() || end.IsZero() || !end.After(start) {
			continue
		}
		raw = append(raw, rawPeriod{
			start: start,
			end:   end,
			deep:  r.GetInt("deep_minutes"),
			rem:   r.GetInt("rem_minutes"),
			light: r.GetInt("light_minutes"),
			awake: r.GetInt("awake_minutes"),
		})
	}
	if len(raw) == 0 {
		return nil, nil
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
		dur := g.end.Sub(g.start)
		engineRecords[i] = engine.SleepRecord{
			Date:            g.start,
			SleepStart:      g.start,
			SleepEnd:        g.end,
			DurationMinutes: int(dur.Minutes()),
		}
		// Auto-detect naps: sleep starting after 10am local time and shorter than 2 hours.
		localStartHour := g.start.In(userLoc).Hour()
		isNap := localStartHour >= 10 && dur < 2*time.Hour
		periods[i] = engine.SleepPeriod{Start: g.start, End: g.end, IsNap: isNap}
	}

	return engineRecords, periods
}
