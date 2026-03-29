package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

type Action struct {
	Type  string `json:"action"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

type Notification struct {
	Server      string
	Topic       string
	AccessToken string
	Title       string
	Message     string
	Priority    int
	Delay       time.Duration
	Tags        []string
	Click       string
	Actions     []Action
}

func SendNotification(n Notification) error {
	if n.Server == "" {
		n.Server = "https://ntfy.sh"
	}
	url := strings.TrimRight(n.Server, "/") + "/" + n.Topic

	req, err := http.NewRequest("POST", url, strings.NewReader(n.Message))
	if err != nil {
		return fmt.Errorf("create ntfy request: %w", err)
	}

	req.Header.Set("Title", n.Title)
	req.Header.Set("Priority", strconv.Itoa(n.Priority))

	if n.Delay > 0 {
		req.Header.Set("Delay", formatDuration(n.Delay))
	}
	if len(n.Tags) > 0 {
		req.Header.Set("Tags", strings.Join(n.Tags, ","))
	}
	if n.Click != "" {
		req.Header.Set("Click", n.Click)
	}
	if n.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+n.AccessToken)
	}
	if len(n.Actions) > 0 {
		b, err := json.Marshal(n.Actions)
		if err != nil {
			return fmt.Errorf("marshal ntfy actions: %w", err)
		}
		req.Header.Set("Actions", string(b))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("ntfy returned status %d", resp.StatusCode)
	}
	return nil
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return ""
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", m)
	}
}
