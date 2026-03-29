package routes

import (
	"bytes"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/a-h/templ"
	"github.com/drewbitt/circadian/internal/templates"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

func registrationEnabled() bool {
	v := strings.ToLower(os.Getenv("ALLOW_REGISTRATION"))
	return v != "false" && v != "0"
}

func registerAuthRoutes(se *core.ServeEvent, app *pocketbase.PocketBase) {
	enabled := registrationEnabled()

	se.Router.GET("/login", func(re *core.RequestEvent) error {
		return render(re, templates.Login(enabled))
	})

	se.Router.GET("/register", func(re *core.RequestEvent) error {
		if !enabled {
			return re.Redirect(http.StatusTemporaryRedirect, "/login")
		}
		return render(re, templates.Register(""))
	})

	se.Router.POST("/register", func(re *core.RequestEvent) error {
		if !enabled {
			return re.Redirect(http.StatusTemporaryRedirect, "/login")
		}

		data := struct {
			Email           string `form:"email"`
			Password        string `form:"password"`
			PasswordConfirm string `form:"password_confirm"`
		}{}
		if err := re.BindBody(&data); err != nil {
			return renderRegisterError(re, "Invalid form data")
		}

		data.Email = strings.TrimSpace(data.Email)
		if data.Email == "" || data.Password == "" {
			return renderRegisterError(re, "Email and password are required")
		}
		if data.Password != data.PasswordConfirm {
			return renderRegisterError(re, "Passwords do not match")
		}
		if len(data.Password) < 8 {
			return renderRegisterError(re, "Password must be at least 8 characters")
		}

		usersCol, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			slog.Error("failed to find users collection", "error", err)
			return renderRegisterError(re, "Registration unavailable")
		}

		existing, _ := app.FindAuthRecordByEmail("users", data.Email)
		if existing != nil {
			return renderRegisterError(re, "An account with this email already exists")
		}

		user := core.NewRecord(usersCol)
		user.Set("email", data.Email)
		user.Set("password", data.Password)

		if err := app.Save(user); err != nil {
			slog.Error("failed to create user", "error", err)
			return renderRegisterError(re, "Failed to create account")
		}

		settingsCol, err := app.FindCollectionByNameOrId("settings")
		if err == nil {
			settings := core.NewRecord(settingsCol)
			settings.Set("user", user.Id)
			settings.Set("sleep_need_hours", 8.0)
			settings.Set("notifications_enabled", false)
			if err := app.Save(settings); err != nil {
				slog.Error("failed to create default settings", "user_id", user.Id, "error", err)
			}
		}

		return re.Redirect(http.StatusSeeOther, "/login?registered=1")
	})

	se.Router.POST("/logout", func(re *core.RequestEvent) error {
		http.SetCookie(re.Response, &http.Cookie{
			Name:     "pb_auth",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
		return re.Redirect(http.StatusSeeOther, "/login")
	})
}

func render(re *core.RequestEvent, comp templ.Component) error {
	var buf bytes.Buffer
	if err := comp.Render(re.Request.Context(), &buf); err != nil {
		return re.InternalServerError("render failed", err)
	}
	re.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
	re.Response.Write(buf.Bytes())
	return nil
}

func renderRegisterError(re *core.RequestEvent, errMsg string) error {
	var buf bytes.Buffer
	_ = templates.Register(errMsg).Render(re.Request.Context(), &buf)
	re.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
	re.Response.WriteHeader(http.StatusBadRequest)
	re.Response.Write(buf.Bytes())
	return nil
}
