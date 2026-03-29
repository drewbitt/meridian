package ingest

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// appleHealthRecord represents a single <Record> element in the Apple Health export.
type appleHealthRecord struct {
	Type      string `xml:"type,attr"`
	Value     string `xml:"value,attr"`
	StartDate string `xml:"startDate,attr"`
	EndDate   string `xml:"endDate,attr"`
}

const (
	ahSleepAnalysis = "HKCategoryTypeIdentifierSleepAnalysis"
	// iOS 16+ sleep values
	ahInBed      = "HKCategoryValueSleepAnalysisInBed"
	ahAsleepCore = "HKCategoryValueSleepAnalysisAsleepCore"
	ahAsleepDeep = "HKCategoryValueSleepAnalysisAsleepDeep"
	ahAsleepREM  = "HKCategoryValueSleepAnalysisAsleepREM"
	ahAwake      = "HKCategoryValueSleepAnalysisAwake"
	// Pre-iOS 16
	ahAsleep = "HKCategoryValueSleepAnalysisAsleep"
	// iOS 16+ stage unknown
	ahAsleepUnspecified = "HKCategoryValueSleepAnalysisAsleepUnspecified"
)

// ParseAppleHealthZip reads an Apple Health export ZIP file and extracts sleep records.
func ParseAppleHealthZip(zipPath string) ([]SleepRecord, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if strings.HasSuffix(f.Name, "export.xml") || f.Name == "apple_health_export/export.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open export.xml: %w", err)
			}
			defer rc.Close()
			return parseAppleHealthXML(rc)
		}
	}

	return nil, fmt.Errorf("%w: export.xml not found in zip", ErrInvalidFile)
}

// ParseAppleHealthXML reads an Apple Health export.xml directly.
func ParseAppleHealthXML(r io.Reader) ([]SleepRecord, error) {
	return parseAppleHealthXML(r)
}

// ParseAppleHealthFile opens and parses an Apple Health export file.
// Supports both .zip and .xml files.
func ParseAppleHealthFile(path string) ([]SleepRecord, error) {
	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		return ParseAppleHealthZip(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseAppleHealthXML(f)
}

func parseAppleHealthXML(r io.Reader) ([]SleepRecord, error) {
	decoder := xml.NewDecoder(r)

	// Group sleep samples by night (date of sleep start).
	type nightData struct {
		sleepStart time.Time
		sleepEnd   time.Time
		deepMins   int
		remMins    int
		lightMins  int
		awakeMins  int
		totalMins  int
	}
	nights := make(map[string]*nightData) // keyed by date string

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse XML: %w", err)
		}

		se, ok := token.(xml.StartElement)
		if !ok || se.Name.Local != "Record" {
			continue
		}

		var rec appleHealthRecord
		for _, attr := range se.Attr {
			switch attr.Name.Local {
			case "type":
				rec.Type = attr.Value
			case "value":
				rec.Value = attr.Value
			case "startDate":
				rec.StartDate = attr.Value
			case "endDate":
				rec.EndDate = attr.Value
			}
		}

		if rec.Type != ahSleepAnalysis {
			continue
		}

		start, err := parseAHTime(rec.StartDate)
		if err != nil {
			continue
		}
		end, err := parseAHTime(rec.EndDate)
		if err != nil {
			continue
		}

		mins := int(end.Sub(start).Minutes())
		nightStart := start
		if start.Hour() < 12 {
			nightStart = start.AddDate(0, 0, -1)
		}
		dateKey := nightStart.Format("2006-01-02")

		nd, ok := nights[dateKey]
		if !ok {
			nd = &nightData{sleepStart: start, sleepEnd: end}
			nights[dateKey] = nd
		}
		if start.Before(nd.sleepStart) {
			nd.sleepStart = start
		}
		if end.After(nd.sleepEnd) {
			nd.sleepEnd = end
		}

		switch rec.Value {
		case ahAsleepDeep:
			nd.deepMins += mins
			nd.totalMins += mins
		case ahAsleepREM:
			nd.remMins += mins
			nd.totalMins += mins
		case ahAsleepCore, ahAsleep, ahAsleepUnspecified:
			nd.lightMins += mins
			nd.totalMins += mins
		case ahAwake:
			nd.awakeMins += mins
		case ahInBed:
			// InBed is the overall container; don't double-count.
		}
	}

	var records []SleepRecord
	for _, nd := range nights {
		total := nd.totalMins
		if total == 0 {
			total = int(nd.sleepEnd.Sub(nd.sleepStart).Minutes())
		}
		records = append(records, SleepRecord{
			Date:            sleepNightDate(nd.sleepStart),
			SleepStart:      nd.sleepStart,
			SleepEnd:        nd.sleepEnd,
			Source:          SourceAppleHealth,
			DurationMinutes: total,
			DeepMinutes:     nd.deepMins,
			REMMinutes:      nd.remMins,
			LightMinutes:    nd.lightMins,
			AwakeMinutes:    nd.awakeMins,
		})
	}

	return records, nil
}

func parseAHTime(s string) (time.Time, error) {
	// Apple Health uses format: "2023-01-15 23:30:00 -0800"
	formats := []string{
		"2006-01-02 15:04:05 -0700",
		time.RFC3339,
		"2006-01-02T15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse Apple Health time: %s", s)
}
