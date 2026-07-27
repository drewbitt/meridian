package routes

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/a-h/templ"
	"github.com/drewbitt/meridian/internal/templates"
	"github.com/pocketbase/pocketbase/core"
)

type registrationMode int

const (
	registrationFirstUser registrationMode = iota
	registrationOpen
	registrationClosed
)

var (
	errRegistrationClosed = errors.New("registration is closed")
	errAccountExists      = errors.New("account already exists")
)

func configuredRegistrationMode() registrationMode {
	switch strings.ToLower(os.Getenv("ALLOW_REGISTRATION")) {
	case "true", "1", "yes", "on":
		return registrationOpen
	case "false", "0", "no", "off":
		return registrationClosed
	default:
		return registrationFirstUser
	}
}

func registrationEnabled(app core.App) bool {
	switch configuredRegistrationMode() {
	case registrationOpen:
		return true
	case registrationClosed:
		return false
	default:
		total, err := app.CountRecords("users")
		return err == nil && total == 0
	}
}

func secureRequest(re *core.RequestEvent) bool {
	return re.Request.TLS != nil ||
		strings.EqualFold(re.Request.Header.Get("X-Forwarded-Proto"), "https")
}

func setAuthCookie(re *core.RequestEvent, token string, maxAge int) {
	http.SetCookie(re.Response, &http.Cookie{ //nolint:gosec // Secure is enabled for TLS and trusted HTTPS proxy requests.
		Name:     "pb_auth",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(re),
		MaxAge:   maxAge,
	})
}

func registerAuthRoutes(se *core.ServeEvent, app core.App) {
	se.Router.GET("/login", func(re *core.RequestEvent) error {
		return render(re, templates.Login(registrationEnabled(app), ""))
	})

	se.Router.POST("/login", func(re *core.RequestEvent) error {
		data := struct {
			Identity string `form:"identity"`
			Password string `form:"password"`
			Redirect string `form:"redirect"`
		}{}
		if err := re.BindBody(&data); err != nil {
			return renderLoginError(re, app, "Invalid form data")
		}

		user, err := app.FindAuthRecordByEmail("users", strings.TrimSpace(data.Identity))
		if err != nil {
			if dummy, dummyErr := app.FindFirstRecordByFilter("users", "id != ''", nil); dummyErr == nil {
				_ = dummy.ValidatePassword("")
			}
			return renderLoginError(re, app, "Invalid email or password")
		}
		if !user.ValidatePassword(data.Password) {
			return renderLoginError(re, app, "Invalid email or password")
		}

		token, err := user.NewAuthToken()
		if err != nil {
			slog.Error("failed to create auth token", "error", err)
			return renderLoginError(re, app, "Sign in unavailable")
		}
		setAuthCookie(re, token, 7*24*60*60)

		redirect := data.Redirect
		if redirect == "" || !strings.HasPrefix(redirect, "/") || strings.HasPrefix(redirect, "//") {
			redirect = "/"
		}
		return re.Redirect(http.StatusSeeOther, redirect)
	})

	se.Router.GET("/register", func(re *core.RequestEvent) error {
		if !registrationEnabled(app) {
			return re.Redirect(http.StatusTemporaryRedirect, "/login")
		}
		return render(re, templates.Register(""))
	})

	se.Router.POST("/register", func(re *core.RequestEvent) error {
		if !registrationEnabled(app) {
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

		var user *core.Record
		err := app.RunInTransaction(func(txApp core.App) error {
			if configuredRegistrationMode() == registrationFirstUser {
				total, err := txApp.CountRecords("users")
				if err != nil || total != 0 {
					return errRegistrationClosed
				}
			}

			if existing, _ := txApp.FindAuthRecordByEmail("users", data.Email); existing != nil {
				return errAccountExists
			}

			usersCol, err := txApp.FindCollectionByNameOrId("users")
			if err != nil {
				return err
			}
			user = core.NewRecord(usersCol)
			user.Set("email", data.Email)
			user.Set("password", data.Password)
			if err := txApp.Save(user); err != nil {
				return err
			}

			settingsCol, err := txApp.FindCollectionByNameOrId("settings")
			if err != nil {
				return err
			}
			settings := core.NewRecord(settingsCol)
			settings.Set("user", user.Id)
			settings.Set("sleep_need_hours", 8.0)
			settings.Set("notifications_enabled", false)
			if loc := locationFromCookie(re); loc != nil {
				settings.Set("timezone", loc.String())
			}
			return txApp.Save(settings)
		})
		if errors.Is(err, errRegistrationClosed) {
			return re.Redirect(http.StatusSeeOther, "/login")
		}
		if errors.Is(err, errAccountExists) {
			return renderRegisterError(re, "An account with this email already exists")
		}
		if err != nil {
			slog.Error("failed to create account", "error", err)
			return renderRegisterError(re, "Failed to create account")
		}

		token, err := user.NewAuthToken()
		if err != nil {
			slog.Error("failed to create auth token", "error", err)
			return re.Redirect(http.StatusSeeOther, "/login")
		}
		setAuthCookie(re, token, 7*24*60*60)
		return re.Redirect(http.StatusSeeOther, "/settings?welcome=1")
	})

	se.Router.POST("/logout", func(re *core.RequestEvent) error {
		setAuthCookie(re, "", -1)
		return re.Redirect(http.StatusSeeOther, "/login")
	})
}

func render(re *core.RequestEvent, comp templ.Component) error {
	return renderWithStatus(re, http.StatusOK, comp)
}

func renderWithStatus(re *core.RequestEvent, status int, comp templ.Component) error {
	var buf bytes.Buffer
	if err := comp.Render(re.Request.Context(), &buf); err != nil {
		return re.InternalServerError("render failed", err)
	}
	re.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		re.Response.WriteHeader(status)
	}
	_, err := re.Response.Write(buf.Bytes())
	return err
}

func renderRegisterError(re *core.RequestEvent, errMsg string) error {
	return renderWithStatus(re, http.StatusBadRequest, templates.Register(errMsg))
}

func renderLoginError(re *core.RequestEvent, app core.App, errMsg string) error {
	return renderWithStatus(re, http.StatusUnauthorized, templates.Login(registrationEnabled(app), errMsg))
}
