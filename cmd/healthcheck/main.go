package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://localhost:8090/api/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
	resp.Body.Close()
}
