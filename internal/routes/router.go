// Package routes registers all HTTP route groups on PocketBase.
package routes

import (
	"errors"
	"net/url"
	"time"

	"github.com/drewbitt/meridian/internal/services"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// resolveUserLocation returns the best available timezone for the current
// request: user settings → browser cookie → time.Local (server TZ).
func resolveUserLocation(app core.App, re *core.RequestEvent) *time.Location {
	if userID, err := authedUserID(re); err == nil {
		loc := services.UserLocation(app, userID)
		if loc != time.Local {
			return loc
		}
	}
	if loc := locationFromCookie(re); loc != nil {
		return loc
	}
	return time.Local
}

// userLocationFromForm reads the IANA timezone from a hidden "tz" form field
// first (most reliable for form submissions), then falls back through the
// standard chain: user settings → browser cookie → time.Local.
func userLocationFromForm(app core.App, re *core.RequestEvent) *time.Location {
	if tz := re.Request.FormValue("tz"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return resolveUserLocation(app, re)
}

// locationFromCookie reads the IANA timezone from the browser "tz" cookie.
// Returns nil if absent or invalid.
func locationFromCookie(re *core.RequestEvent) *time.Location {
	cookie, err := re.Request.Cookie("tz")
	if err != nil {
		return nil
	}
	name, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return loc
}

var errNotAuthenticated = errors.New("not authenticated")

// authedUserID extracts the authenticated user's ID from the request,
// returning errNotAuthenticated if the request has no valid session.
func authedUserID(re *core.RequestEvent) (string, error) {
	info, err := re.RequestInfo()
	if err != nil {
		return "", err
	}
	if info.Auth == nil {
		return "", errNotAuthenticated
	}
	return info.Auth.Id, nil
}

// Register binds all application routes to the PocketBase router.
func Register(app *pocketbase.PocketBase) {
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Id: "meridian-routes",
		Func: func(se *core.ServeEvent) error {
			registerAuthRoutes(se, app)
			registerDashboardRoutes(se, app)
			registerSleepRoutes(se, app)
			registerSettingsRoutes(se, app)
			registerHabitRoutes(se, app)
			registerFitbitAuthRoutes(se, app)
			registerAPIRoutes(se, app)
			return se.Next()
		},
	})
}
