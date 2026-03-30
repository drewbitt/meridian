package routes

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/drewbitt/meridian/internal/ingest"
	"github.com/drewbitt/meridian/internal/services"
	"github.com/pocketbase/pocketbase/core"
)

const maxUploadSize = 100 << 20

func importFileToDisk(r io.Reader, filename string, parse func(string) ([]ingest.SleepRecord, error)) ([]ingest.SleepRecord, error) {
	safeName := filepath.Base(filename)
	tmp, err := os.CreateTemp("", "meridian-import-*-"+safeName)
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("flush temp file: %w", err)
	}

	return parse(tmpPath)
}

func parseImportSource(r io.Reader, filename, source string) ([]ingest.SleepRecord, error) {
	switch source {
	case "healthconnect":
		return ingest.ParseHealthConnect(io.LimitReader(r, maxUploadSize))
	case "applehealth":
		return importFileToDisk(io.LimitReader(r, maxUploadSize), filename, ingest.ParseAppleHealthFile)
	case "gadgetbridge":
		return importFileToDisk(io.LimitReader(r, maxUploadSize), filename, ingest.ParseGadgetbridge)
	default:
		return nil, fmt.Errorf("%w: %s", ingest.ErrUnknownSource, source)
	}
}

// importAndUpsert parses the uploaded file and upserts all records for the user.
func importAndUpsert(app core.App, userID string, file io.Reader, filename, source string) (imported, total int, err error) {
	records, err := parseImportSource(file, filename, source)
	if err != nil {
		return 0, 0, err
	}
	for _, rec := range records {
		if _, err := services.UpsertSleepRecord(app, userID, rec); err == nil {
			imported++
		}
	}
	return imported, len(records), nil
}

func registerAPIRoutes(se *core.ServeEvent, app core.App) {
	se.Router.GET("/api/schedule", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.UnauthorizedError("", nil)
		}

		schedule, debt, err := loadTodayData(app, userID)
		if err != nil {
			return re.InternalServerError("Failed to load schedule", err)
		}

		return re.JSON(http.StatusOK, map[string]any{
			"schedule":   schedule,
			"sleep_debt": debt,
		})
	})

	se.Router.POST("/api/import", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.UnauthorizedError("", nil)
		}

		source := re.Request.URL.Query().Get("source")
		if source == "" {
			return re.BadRequestError("Missing source parameter", nil)
		}

		re.Request.Body = http.MaxBytesReader(re.Response, re.Request.Body, maxUploadSize)

		file, header, err := re.Request.FormFile("file")
		if err != nil {
			return re.BadRequestError("Missing file", err)
		}
		defer file.Close()

		imported, total, err := importAndUpsert(app, userID, file, header.Filename, source)
		if err != nil {
			return re.BadRequestError("Failed to parse file", err)
		}

		return re.JSON(http.StatusOK, map[string]any{
			"imported": imported,
			"total":    total,
		})
	})

	se.Router.GET("/api/sleep", func(re *core.RequestEvent) error {
		userID, err := authedUserID(re)
		if err != nil {
			return re.UnauthorizedError("", nil)
		}

		loc := services.UserLocation(app, userID)
		days := 14
		since := time.Now().In(loc).AddDate(0, 0, -days).Format("2006-01-02 00:00:00")
		records, err := app.FindRecordsByFilter(
			"sleep_records",
			"user = {:user} && date >= {:since}",
			"-date", 0, 0,
			map[string]any{"user": userID, "since": since},
		)
		if err != nil {
			return re.InternalServerError("", err)
		}

		var result []map[string]any
		for _, r := range records {
			result = append(result, map[string]any{
				"id":               r.Id,
				"date":             r.GetDateTime("date").Time(),
				"sleep_start":      r.GetDateTime("sleep_start").Time(),
				"sleep_end":        r.GetDateTime("sleep_end").Time(),
				"source":           r.GetString("source"),
				"duration_minutes": r.GetInt("duration_minutes"),
			})
		}

		debt := services.ComputeUserDebt(app, userID)

		return re.JSON(http.StatusOK, map[string]any{
			"records":    result,
			"sleep_debt": debt,
		})
	})
}
