// Package schema creates or updates PocketBase collections used by the application.
package schema

import (
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
	return ensureHabits(app)
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
