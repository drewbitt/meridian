// Package routes registers all HTTP route groups on PocketBase.
package routes

import (
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// Register binds all application routes to the PocketBase router.
func Register(app *pocketbase.PocketBase) {
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Id: "circadian-routes",
		Func: func(se *core.ServeEvent) error {
			registerDashboardRoutes(se, app)
			registerSleepRoutes(se, app)
			registerSettingsRoutes(se, app)
			registerFitbitAuthRoutes(se, app)
			registerAPIRoutes(se, app)
			return se.Next()
		},
	})
}
