// Package schema creates or updates PocketBase collections used by the application.
package schema

import (
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// EnsureCollections creates or updates all application collections.
func EnsureCollections(app core.App) error {
	if err := ensureSleepRecords(app); err != nil {
		return err
	}
	if err := ensureEnergySchedules(app); err != nil {
		return err
	}
	if err := ensureSettings(app); err != nil {
		return err
	}
	if err := ensureHabits(app); err != nil {
		return err
	}
	if err := deduplicateEnergySchedules(app); err != nil {
		return err
	}
	return deduplicateExactSleepRecords(app)
}

// upsertCollection finds an existing collection by name or creates a new one.
func upsertCollection(app core.App, name string) *core.Collection {
	if c, err := app.FindCollectionByNameOrId(name); err == nil {
		return c
	}
	return core.NewBaseCollection(name, "")
}

func ensureSleepRecords(app core.App) error {
	c := upsertCollection(app, "sleep_records")
	authRule := "@request.auth.id != '' && user = @request.auth.id"
	c.ListRule = &authRule
	c.ViewRule = &authRule
	c.CreateRule = &authRule
	c.UpdateRule = &authRule
	c.DeleteRule = &authRule
	c.Fields.Add(
		&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1},
		&core.DateField{Name: "date", Required: true},
		&core.DateField{Name: "sleep_start", Required: true},
		&core.DateField{Name: "sleep_end", Required: true},
		&core.TextField{Name: "source", Required: true},
		&core.NumberField{Name: "duration_minutes", Required: true},
		&core.NumberField{Name: "deep_minutes"},
		&core.NumberField{Name: "rem_minutes"},
		&core.NumberField{Name: "light_minutes"},
		&core.NumberField{Name: "awake_minutes"},
		&core.BoolField{Name: "is_nap"},
	)
	return app.Save(c)
}

func ensureEnergySchedules(app core.App) error {
	c := upsertCollection(app, "energy_schedules")
	authRule := "@request.auth.id != '' && user = @request.auth.id"
	c.ListRule = &authRule
	c.ViewRule = &authRule
	c.Fields.Add(
		&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1},
		&core.DateField{Name: "date", Required: true},
		&core.DateField{Name: "wake_time", Required: true},
		&core.DateField{Name: "morning_wake_time"},
		&core.JSONField{Name: "schedule_json", MaxSize: 1000000},
		&core.JSONField{Name: "notifications_sent", MaxSize: 10000},
	)
	return app.Save(c)
}

func ensureSettings(app core.App) error {
	c := upsertCollection(app, "settings")
	authRule := "@request.auth.id != '' && user = @request.auth.id"
	c.ListRule = &authRule
	c.ViewRule = &authRule
	c.CreateRule = &authRule
	c.UpdateRule = &authRule
	c.Fields.Add(
		&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1},
		&core.NumberField{Name: "sleep_need_hours"},
		&core.NumberField{Name: "chronotype_shift"},
		&core.TextField{Name: "ntfy_topic"},
		&core.TextField{Name: "ntfy_server"},
		&core.TextField{Name: "ntfy_access_token"},
		&core.TextField{Name: "site_url"},
		&core.TextField{Name: "timezone"},
		&core.TextField{Name: "fitbit_client_id"},
		&core.TextField{Name: "fitbit_client_secret"},
		&core.TextField{Name: "fitbit_access_token"},
		&core.TextField{Name: "fitbit_refresh_token"},
		&core.DateField{Name: "fitbit_token_expiry"},
		&core.DateField{Name: "fitbit_last_sync"},
		&core.BoolField{Name: "notifications_enabled"},
		&core.TextField{Name: "location_name"},
		&core.NumberField{Name: "latitude"},
		&core.NumberField{Name: "longitude"},
	)
	return app.Save(c)
}

func ensureHabits(app core.App) error {
	c := upsertCollection(app, "habits")
	authRule := "@request.auth.id != '' && user = @request.auth.id"
	c.ListRule = &authRule
	c.ViewRule = &authRule
	c.CreateRule = &authRule
	c.UpdateRule = &authRule
	c.DeleteRule = &authRule
	c.Fields.Add(
		&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1},
		&core.TextField{Name: "name", Required: true},
		&core.SelectField{Name: "anchor", Required: true, Values: []string{
			"morning_wake", "best_focus", "morning_peak", "afternoon_dip",
			"nap_window", "evening_peak", "caffeine_cutoff",
			"sunset", "sunrise", "melatonin_window", "custom",
		}, MaxSelect: 1},
		&core.NumberField{Name: "offset_minutes"},
		&core.TextField{Name: "custom_time"},
		&core.BoolField{Name: "notify"},
		&core.BoolField{Name: "enabled"},
	)
	return app.Save(c)
}

func deduplicateEnergySchedules(app core.App) error {
	records, err := app.FindAllRecords("energy_schedules")
	if err != nil {
		return err
	}
	kept := make(map[string]*core.Record)
	for _, record := range records {
		key := record.GetString("user") + "\x00" + record.GetDateTime("date").Time().UTC().Format(time.DateOnly)
		existing := kept[key]
		if existing == nil {
			kept[key] = record
			continue
		}
		// Preserve the row with the most cached/deduplication state.
		if recordStateScore(record) > recordStateScore(existing) {
			if err := app.Delete(existing); err != nil {
				return err
			}
			kept[key] = record
		} else if err := app.Delete(record); err != nil {
			return err
		}
	}
	return nil
}

func recordStateScore(record *core.Record) int {
	return len(record.GetString("schedule_json")) + 10*len(record.GetString("notifications_sent"))
}

func deduplicateExactSleepRecords(app core.App) error {
	records, err := app.FindAllRecords("sleep_records")
	if err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for _, record := range records {
		key := record.GetString("user") + "\x00" +
			record.GetString("source") + "\x00" +
			record.GetDateTime("sleep_start").Time().UTC().Format(time.RFC3339Nano)
		if _, duplicate := seen[key]; !duplicate {
			seen[key] = struct{}{}
			continue
		}
		if err := app.Delete(record); err != nil {
			return err
		}
	}
	return nil
}
