package services

import (
	"sort"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/drewbitt/meridian/internal/ingest"
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
		start        time.Time
		end          time.Time
		deep         int
		rem          int
		light        int
		awake        int
		isNap        bool
		sleepMinutes int
		measured     bool
	}

	var raw []rawPeriod
	for _, r := range records {
		start := r.GetDateTime("sleep_start").Time()
		end := r.GetDateTime("sleep_end").Time()
		// Skip invalid records: zero times or end not after start.
		if start.IsZero() || end.IsZero() || !end.After(start) {
			continue
		}
		sleepMinutes := r.GetInt("duration_minutes")
		if sleepMinutes <= 0 {
			sleepMinutes = r.GetInt("deep_minutes") + r.GetInt("rem_minutes") + r.GetInt("light_minutes")
		}
		if sleepMinutes <= 0 {
			sleepMinutes = int(end.Sub(start).Minutes())
		}
		raw = append(raw, rawPeriod{
			start:        start,
			end:          end,
			deep:         r.GetInt("deep_minutes"),
			rem:          r.GetInt("rem_minutes"),
			light:        r.GetInt("light_minutes"),
			awake:        r.GetInt("awake_minutes"),
			isNap:        r.GetBool("is_nap"),
			sleepMinutes: sleepMinutes,
			measured:     r.GetString("source") != ingest.SourceManual,
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
			touches := p.start.Equal(last.end)
			if p.end.After(last.end) {
				last.end = p.end
			}
			last.deep = max(last.deep, p.deep)
			last.rem = max(last.rem, p.rem)
			last.light = max(last.light, p.light)
			last.awake = max(last.awake, p.awake)
			last.isNap = last.isNap || p.isNap
			// Prefer a tracker/importer's measured sleep duration over an
			// overlapping manual time-in-bed span. Between equally measured
			// sources, keep the larger value to avoid losing partial data.
			if touches {
				last.sleepMinutes += p.sleepMinutes
				last.measured = last.measured || p.measured
			} else if (p.measured && !last.measured) || p.measured == last.measured && p.sleepMinutes > last.sleepMinutes {
				last.sleepMinutes = p.sleepMinutes
				last.measured = p.measured
			}
		} else {
			groups = append(groups, p)
		}
	}

	engineRecords := make([]engine.SleepRecord, len(groups))
	periods := make([]engine.SleepPeriod, len(groups))
	for i, g := range groups {
		dur := g.end.Sub(g.start)
		engineRecords[i] = engine.SleepRecord{
			// The debt model groups records by the night they belong to, not
			// the calendar date of the raw timestamp. This matters for common
			// after-midnight bedtimes such as 1am or 2am.
			Date:            ingest.SleepNightDate(g.start.In(userLoc)),
			SleepStart:      g.start,
			SleepEnd:        g.end,
			DurationMinutes: g.sleepMinutes,
		}
		// Auto-detect naps: sleep starting after 10am local time and shorter than 2 hours.
		localStartHour := g.start.In(userLoc).Hour()
		isNap := g.isNap || (localStartHour >= 10 && dur < 2*time.Hour)
		periods[i] = engine.SleepPeriod{Start: g.start, End: g.end, IsNap: isNap}
	}

	return engineRecords, periods
}
