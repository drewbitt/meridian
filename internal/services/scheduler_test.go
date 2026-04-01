package services

import (
	"encoding/json"
	"testing"
)

func TestLoadSentKeysRoundTrip(t *testing.T) {
	// Test that our JSON key encoding/decoding is consistent.
	keys := []string{"caffeine_cutoff", "melatonin_window", "habit_abc123"}
	b, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []string
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}

	if len(decoded) != len(keys) {
		t.Fatalf("expected %d keys, got %d", len(keys), len(decoded))
	}

	sent := make(map[string]bool)
	for _, k := range decoded {
		sent[k] = true
	}

	for _, k := range keys {
		if !sent[k] {
			t.Errorf("key %q not found after round-trip", k)
		}
	}
}

func TestDashboardURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com", "https://example.com/"},
		{"https://example.com/", "https://example.com/"},
		{"", ""},
		{"http://localhost:8090/", "http://localhost:8090/"},
		{"http://localhost:8090", "http://localhost:8090/"},
	}
	for _, tt := range tests {
		got := dashboardURL(tt.input)
		if got != tt.want {
			t.Errorf("dashboardURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDashboardAction(t *testing.T) {
	// Empty URL should return nil actions
	actions := dashboardAction("")
	if actions != nil {
		t.Errorf("dashboardAction(\"\") = %v, want nil", actions)
	}

	// Valid URL should return a single "view" action
	actions = dashboardAction("https://example.com")
	if len(actions) != 1 {
		t.Fatalf("dashboardAction: expected 1 action, got %d", len(actions))
	}
	if actions[0].Type != "view" {
		t.Errorf("action type = %q, want \"view\"", actions[0].Type)
	}
	if actions[0].Label != "Dashboard" {
		t.Errorf("action label = %q, want \"Dashboard\"", actions[0].Label)
	}
	if actions[0].URL != "https://example.com/" {
		t.Errorf("action URL = %q, want \"https://example.com/\"", actions[0].URL)
	}
}

func TestLocationFromSettings_NilRecord(t *testing.T) {
	// LocationFromSettings should handle nil without panicking.
	loc := LocationFromSettings(nil)
	if loc == nil {
		t.Fatal("LocationFromSettings(nil) returned nil, want non-nil fallback")
	}
}
