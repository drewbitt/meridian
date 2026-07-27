package services

import (
	"sort"
	"time"

	"github.com/drewbitt/meridian/internal/engine"
	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/pocketbase/pocketbase/core"
)

const (
	maxSleepFragmentGap = 4 * time.Hour
	napComparisonWindow = 20 * time.Hour
)

type mergedSleepPeriod struct {
	start        time.Time
	end          time.Time
	deep         int
	rem          int
	light        int
	awake        int
	sleepMinutes int
	measured     bool
	explicitMain bool
	explicitNap  bool
	inferredNap  bool
}

type sleepEpisode struct {
	indexes      []int
	start        time.Time
	end          time.Time
	sleepMinutes int
	explicitMain bool
	inferredOnly bool
}

// ConvertSleepRecords converts PocketBase records into engine records and
// periods, merging overlapping records and resolving ambiguous naps in the
// context of the surrounding sleep episodes. Optional loc is the user's
// timezone. It defaults to UTC.
func ConvertSleepRecords(records []*core.Record, loc ...*time.Location) ([]engine.SleepRecord, []engine.SleepPeriod) {
	userLoc := time.UTC
	if len(loc) > 0 && loc[0] != nil {
		userLoc = loc[0]
	}
	if len(records) == 0 {
		return nil, nil
	}

	var raw []mergedSleepPeriod
	for _, record := range records {
		start := record.GetDateTime("sleep_start").Time()
		end := record.GetDateTime("sleep_end").Time()
		if start.IsZero() || end.IsZero() || !end.After(start) {
			continue
		}

		sleepMinutes := record.GetInt("duration_minutes")
		if sleepMinutes <= 0 {
			sleepMinutes = record.GetInt("deep_minutes") +
				record.GetInt("rem_minutes") +
				record.GetInt("light_minutes")
		}
		if sleepMinutes <= 0 {
			sleepMinutes = int(end.Sub(start).Minutes())
		}

		isNap := record.GetBool("is_nap")
		napExplicit := record.GetBool("nap_explicit")
		raw = append(raw, mergedSleepPeriod{
			start:        start,
			end:          end,
			deep:         record.GetInt("deep_minutes"),
			rem:          record.GetInt("rem_minutes"),
			light:        record.GetInt("light_minutes"),
			awake:        record.GetInt("awake_minutes"),
			sleepMinutes: sleepMinutes,
			measured:     record.GetString("source") != ingest.SourceManual,
			explicitMain: napExplicit && !isNap,
			explicitNap:  napExplicit && isNap,
			inferredNap:  !napExplicit && isNap,
		})
	}
	if len(raw) == 0 {
		return nil, nil
	}

	sort.Slice(raw, func(i, j int) bool {
		return raw[i].start.Before(raw[j].start)
	})

	groups := []mergedSleepPeriod{raw[0]}
	for _, period := range raw[1:] {
		last := &groups[len(groups)-1]
		if period.start.After(last.end) {
			groups = append(groups, period)
			continue
		}

		touches := period.start.Equal(last.end)
		if period.end.After(last.end) {
			last.end = period.end
		}
		last.deep = max(last.deep, period.deep)
		last.rem = max(last.rem, period.rem)
		last.light = max(last.light, period.light)
		last.awake = max(last.awake, period.awake)
		last.explicitMain = last.explicitMain || period.explicitMain
		last.explicitNap = last.explicitNap || period.explicitNap
		last.inferredNap = last.inferredNap || period.inferredNap

		// Adjacent segments are distinct measured sleep. Overlapping sources
		// describe the same interval, so prefer a measured duration over manual
		// time-in-bed and otherwise keep the more complete value.
		if touches {
			last.sleepMinutes += period.sleepMinutes
			last.measured = last.measured || period.measured
		} else if (period.measured && !last.measured) ||
			(period.measured == last.measured && period.sleepMinutes > last.sleepMinutes) {
			last.sleepMinutes = period.sleepMinutes
			last.measured = period.measured
		}
	}

	napFlags := classifyNapGroups(groups, userLoc)
	engineRecords := make([]engine.SleepRecord, len(groups))
	periods := make([]engine.SleepPeriod, len(groups))
	for i, group := range groups {
		engineRecords[i] = engine.SleepRecord{
			Date:            ingest.SleepNightDate(group.start.In(userLoc)),
			SleepStart:      group.start,
			SleepEnd:        group.end,
			DurationMinutes: group.sleepMinutes,
			IsNap:           napFlags[i],
		}
		periods[i] = engine.SleepPeriod{
			Start: group.start,
			End:   group.end,
			IsNap: napFlags[i],
		}
	}

	return engineRecords, periods
}

// classifyNapGroups preserves source-provided classifications and resolves
// ambiguous imports in context. Fragments separated by up to four hours are
// treated as one episode so an interrupted night is not split into a main
// sleep and a false nap. Among separate ambiguous episodes in one waking
// cycle, a clearly dominant episode is main sleep and the shorter episode is a
// nap. A lone short nighttime sleep remains main sleep after an insomnia night.
func classifyNapGroups(groups []mergedSleepPeriod, loc *time.Location) []bool {
	isNap := make([]bool, len(groups))
	var episodes []sleepEpisode

	for i, group := range groups {
		if group.explicitNap && !group.explicitMain {
			isNap[i] = true
			continue
		}

		canJoin := len(episodes) > 0 &&
			!group.start.After(episodes[len(episodes)-1].end.Add(maxSleepFragmentGap))
		if !canJoin {
			episodes = append(episodes, sleepEpisode{
				indexes:      []int{i},
				start:        group.start,
				end:          group.end,
				sleepMinutes: group.sleepMinutes,
				explicitMain: group.explicitMain,
				inferredOnly: group.inferredNap,
			})
			continue
		}

		episode := &episodes[len(episodes)-1]
		episode.indexes = append(episode.indexes, i)
		if group.end.After(episode.end) {
			episode.end = group.end
		}
		episode.sleepMinutes += group.sleepMinutes
		episode.explicitMain = episode.explicitMain || group.explicitMain
		episode.inferredOnly = episode.inferredOnly && group.inferredNap
	}

	for i, episode := range episodes {
		if episode.explicitMain {
			continue
		}

		dominantNeighbor := false
		for j, other := range episodes {
			if i == j || other.sleepMinutes < 4*60 ||
				other.sleepMinutes < episode.sleepMinutes+30 {
				continue
			}

			gap := other.start.Sub(episode.end)
			if gap < 0 {
				gap = episode.start.Sub(other.end)
			}
			if gap < 0 {
				gap = 0
			}
			if gap <= napComparisonWindow {
				dominantNeighbor = true
				break
			}
		}

		localMidpoint := episode.start.In(loc).Add(episode.end.Sub(episode.start) / 2)
		hour := localMidpoint.Hour()
		shortDaytimeSleep := episode.sleepMinutes < 3*60 && hour >= 10 && hour < 21
		episodeIsNap := dominantNeighbor ||
			shortDaytimeSleep ||
			(episode.inferredOnly && episode.sleepMinutes < 3*60)
		for _, idx := range episode.indexes {
			isNap[idx] = episodeIsNap
		}
	}

	return isNap
}
