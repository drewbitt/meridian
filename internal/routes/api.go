package routes

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drewbitt/circadian/internal/engine"
	"github.com/drewbitt/circadian/internal/ingest"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

func registerAPIRoutes(se *core.ServeEvent, app *pocketbase.PocketBase) {
	// Get today's energy schedule.
	se.Router.GET("/api/schedule", func(re *core.RequestEvent) error {
		info, _ := re.RequestInfo()
		if info.Auth == nil {
			return re.UnauthorizedError("", nil)
		}

		schedule, debt, err := loadTodayData(app, info.Auth.Id)
		if err != nil {
			return re.InternalServerError("Failed to load schedule", err)
		}

		return re.JSON(http.StatusOK, map[string]any{
			"schedule":   schedule,
			"sleep_debt": debt,
		})
	})

	// Import health data file.
	se.Router.POST("/api/import", func(re *core.RequestEvent) error {
		info, _ := re.RequestInfo()
		if info.Auth == nil {
			return re.UnauthorizedError("", nil)
		}

		source := re.Request.URL.Query().Get("source")
		if source == "" {
			return re.BadRequestError("Missing source parameter", nil)
		}

		file, header, err := re.Request.FormFile("file")
		if err != nil {
			return re.BadRequestError("Missing file", err)
		}
		defer file.Close()

		var records []ingest.SleepRecord

		switch source {
		case "healthconnect":
			records, err = ingest.ParseHealthConnect(file)
		case "applehealth":
			// Apple Health needs a file on disk (ZIP handling).
			tmpDir := os.TempDir()
			tmpPath := filepath.Join(tmpDir, header.Filename)
			tmp, err2 := os.Create(tmpPath)
			if err2 != nil {
				return re.InternalServerError("", err2)
			}
			io.Copy(tmp, file)
			tmp.Close()
			defer os.Remove(tmpPath)

			if strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
				records, err = ingest.ParseAppleHealthZip(tmpPath)
			} else {
				f, _ := os.Open(tmpPath)
				defer f.Close()
				records, err = ingest.ParseAppleHealthXML(f)
			}
		case "gadgetbridge":
			tmpDir := os.TempDir()
			tmpPath := filepath.Join(tmpDir, header.Filename)
			tmp, err2 := os.Create(tmpPath)
			if err2 != nil {
				return re.InternalServerError("", err2)
			}
			io.Copy(tmp, file)
			tmp.Close()
			defer os.Remove(tmpPath)
			records, err = ingest.ParseGadgetbridge(tmpPath)
		default:
			return re.BadRequestError("Unknown source: "+source, nil)
		}

		if err != nil {
			return re.BadRequestError("Failed to parse file", err)
		}

		// Upsert records.
		collection, err := app.FindCollectionByNameOrId("sleep_records")
		if err != nil {
			return re.InternalServerError("", err)
		}

		imported := 0
		for _, rec := range records {
			// Check for existing record with same date and source.
			dateStr := rec.Date.Format("2006-01-02")
			existing, _ := app.FindFirstRecordByFilter("sleep_records",
				"user = {:user} && date = {:date} && source = {:source}",
				map[string]any{"user": info.Auth.Id, "date": dateStr, "source": rec.Source},
			)

			var record *core.Record
			if existing != nil {
				record = existing
			} else {
				record = core.NewRecord(collection)
				record.Set("user", info.Auth.Id)
			}

			record.Set("date", rec.Date)
			record.Set("sleep_start", rec.SleepStart)
			record.Set("sleep_end", rec.SleepEnd)
			record.Set("source", rec.Source)
			record.Set("duration_minutes", rec.DurationMinutes)
			record.Set("deep_minutes", rec.DeepMinutes)
			record.Set("rem_minutes", rec.REMMinutes)
			record.Set("light_minutes", rec.LightMinutes)
			record.Set("awake_minutes", rec.AwakeMinutes)

			if err := app.Save(record); err == nil {
				imported++
			}
		}

		return re.JSON(http.StatusOK, map[string]any{
			"imported": imported,
			"total":    len(records),
		})
	})

	// Get sleep history.
	se.Router.GET("/api/sleep", func(re *core.RequestEvent) error {
		info, _ := re.RequestInfo()
		if info.Auth == nil {
			return re.UnauthorizedError("", nil)
		}

		days := 14
		since := time.Now().AddDate(0, 0, -days).Format("2006-01-02 00:00:00")
		records, err := app.FindRecordsByFilter(
			"sleep_records",
			"user = {:user} && date >= {:since}",
			"-date", 0, 0,
			map[string]any{"user": info.Auth.Id, "since": since},
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

		sleepNeed := 8.0
		settings, err := app.FindFirstRecordByFilter("settings", "user = {:user}", map[string]any{"user": info.Auth.Id})
		if err == nil {
			if sn := settings.GetFloat("sleep_need_hours"); sn > 0 {
				sleepNeed = sn
			}
		}

		var engineRecords []engine.SleepRecord
		for _, r := range records {
			engineRecords = append(engineRecords, engine.SleepRecord{
				Date:            r.GetDateTime("date").Time(),
				DurationMinutes: r.GetInt("duration_minutes"),
			})
		}
		debt := engine.CalculateSleepDebt(engineRecords, sleepNeed, time.Now())

		return re.JSON(http.StatusOK, map[string]any{
			"records":    result,
			"sleep_debt": debt,
		})
	})
}
