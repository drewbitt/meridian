package services

import (
	"testing"

	"github.com/drewbitt/meridian/internal/schema"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestGoogleHealthConfigFromSettings_LocalAndDeployedRedirects(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Cleanup() })
	if err := schema.EnsureCollections(app); err != nil {
		t.Fatal(err)
	}
	collection, err := app.FindCollectionByNameOrId("settings")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("local loopback HTTP", func(t *testing.T) {
		t.Setenv("GOOGLE_HEALTH_CLIENT_ID", "")
		t.Setenv("GOOGLE_HEALTH_CLIENT_SECRET", "")
		settings := core.NewRecord(collection)
		settings.Set("site_url", "http://127.0.0.1:8090")
		settings.Set("google_health_client_id", "stored-client")
		settings.Set("google_health_client_secret", "stored-secret")

		cfg := GoogleHealthConfigFromSettings(app, settings)
		if cfg == nil {
			t.Fatal("config is nil")
		}
		if cfg.RedirectURL != "http://127.0.0.1:8090/auth/google-health/callback" {
			t.Errorf("local redirect = %q", cfg.RedirectURL)
		}
	})

	t.Run("deployed HTTPS with environment credentials", func(t *testing.T) {
		t.Setenv("GOOGLE_HEALTH_CLIENT_ID", "environment-client")
		t.Setenv("GOOGLE_HEALTH_CLIENT_SECRET", "environment-secret")
		settings := core.NewRecord(collection)
		settings.Set("site_url", "https://meridian.example.com/")
		settings.Set("google_health_client_id", "stored-client")
		settings.Set("google_health_client_secret", "stored-secret")

		cfg := GoogleHealthConfigFromSettings(app, settings)
		if cfg == nil {
			t.Fatal("config is nil")
		}
		if cfg.ClientID != "environment-client" ||
			cfg.ClientSecret != "environment-secret" {
			t.Errorf("environment credentials not preferred: %#v", cfg)
		}
		if cfg.RedirectURL != "https://meridian.example.com/auth/google-health/callback" {
			t.Errorf("deployed redirect = %q", cfg.RedirectURL)
		}
		if !GoogleHealthCredentialsManaged() {
			t.Fatal("environment credentials not reported as managed")
		}
	})
}

func TestGoogleHealthConfigFromSettings_RequiresCredentialPair(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Cleanup() })
	if err := schema.EnsureCollections(app); err != nil {
		t.Fatal(err)
	}
	collection, err := app.FindCollectionByNameOrId("settings")
	if err != nil {
		t.Fatal(err)
	}
	settings := core.NewRecord(collection)
	settings.Set("site_url", "http://127.0.0.1:8090")
	settings.Set("google_health_client_id", "stored-client")
	settings.Set("google_health_client_secret", "stored-secret")

	t.Setenv("GOOGLE_HEALTH_CLIENT_ID", "partial-environment-client")
	t.Setenv("GOOGLE_HEALTH_CLIENT_SECRET", "")
	if cfg := GoogleHealthConfigFromSettings(app, settings); cfg != nil {
		t.Fatalf("partial environment configuration should fail closed: %#v", cfg)
	}
	if GoogleHealthCredentialsManaged() {
		t.Fatal("partial environment configuration reported as managed")
	}
}

func TestGoogleHealthRedirectURL_EnforcesLoopbackHTTPAndDeploymentHTTPS(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		siteURL string
		want    string
		ok      bool
	}{
		{
			name:    "IPv4 loopback HTTP",
			siteURL: "http://127.0.0.1:8090",
			want:    "http://127.0.0.1:8090/auth/google-health/callback",
			ok:      true,
		},
		{
			name:    "localhost HTTP",
			siteURL: "http://localhost:8090/",
			want:    "http://localhost:8090/auth/google-health/callback",
			ok:      true,
		},
		{
			name:    "deployed HTTPS",
			siteURL: "https://meridian.example.com",
			want:    "https://meridian.example.com/auth/google-health/callback",
			ok:      true,
		},
		{
			name:    "IPv6 loopback HTTP",
			siteURL: "http://[::1]:8090",
			want:    "http://[::1]:8090/auth/google-health/callback",
			ok:      true,
		},
		{name: "deployed HTTP rejected", siteURL: "http://meridian.example.com", ok: false},
		{name: "raw deployment IP rejected", siteURL: "https://192.0.2.1", ok: false},
		{name: "relative URL rejected", siteURL: "meridian.example.com", ok: false},
		{name: "subpath rejected", siteURL: "https://meridian.example.com/app", ok: false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := GoogleHealthRedirectURL(tt.siteURL)
			if ok != tt.ok || got != tt.want {
				t.Errorf("GoogleHealthRedirectURL(%q) = %q, %t; want %q, %t",
					tt.siteURL, got, ok, tt.want, tt.ok)
			}
		})
	}
}
