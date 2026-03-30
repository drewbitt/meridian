// Package schema creates PocketBase collections used by the application.
package schema

import (
	"github.com/pocketbase/pocketbase/core"
)

// EnsureCollections creates any missing application collections.
func EnsureCollections(app core.App) error {
	if err := ensureSleepRecords(app); err != nil {
		return err
	}
	if err := ensureEnergySchedules(app); err != nil {
		return err
	}
	return ensureSettings(app)
}

func ensureSleepRecords(app core.App) error {
	if _, err := app.FindCollectionByNameOrId("sleep_records"); err == nil {
		return nil
	}
	c := core.NewBaseCollection("sleep_records", "")
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
	)
	return app.Save(c)
}

func ensureEnergySchedules(app core.App) error {
	if _, err := app.FindCollectionByNameOrId("energy_schedules"); err == nil {
		return nil
	}
	c := core.NewBaseCollection("energy_schedules", "")
	authRule := "@request.auth.id != '' && user = @request.auth.id"
	c.ListRule = &authRule
	c.ViewRule = &authRule
	c.Fields.Add(
		&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1},
		&core.DateField{Name: "date", Required: true},
		&core.DateField{Name: "wake_time", Required: true},
		&core.JSONField{Name: "schedule_json", MaxSize: 1000000},
	)
	return app.Save(c)
}

func ensureSettings(app core.App) error {
	c, err := app.FindCollectionByNameOrId("settings")
	if err == nil {
		// Collection exists — ensure new fields are present.
		return ensureSettingsFields(app, c)
	}
	c = core.NewBaseCollection("settings", "")
	authRule := "@request.auth.id != '' && user = @request.auth.id"
	c.ListRule = &authRule
	c.ViewRule = &authRule
	c.CreateRule = &authRule
	c.UpdateRule = &authRule
	c.Fields.Add(
		&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1},
		&core.NumberField{Name: "sleep_need_hours"},
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
	)
	return app.Save(c)
}

// ensureSettingsFields adds any missing fields to an existing settings collection.
func ensureSettingsFields(app core.App, c *core.Collection) error {
	newFields := []core.Field{
		&core.DateField{Name: "fitbit_last_sync"},
		&core.TextField{Name: "timezone"},
	}

	changed := false
	for _, f := range newFields {
		if c.Fields.GetByName(f.GetName()) == nil {
			c.Fields.Add(f)
			changed = true
		}
	}

	if changed {
		return app.Save(c)
	}
	return nil
}
