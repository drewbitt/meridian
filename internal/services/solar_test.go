package services

import (
	"math"
	"testing"
	"time"
)

func TestGetSolarTimes_NYC_SummerVsWinter(t *testing.T) {
	lat, lng := 40.71, -74.01 // New York City

	summer := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)  // summer solstice
	winter := time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC) // winter solstice

	sSummer := GetSolarTimes(lat, lng, summer, false)
	sWinter := GetSolarTimes(lat, lng, winter, false)

	t.Logf("NYC Summer solstice: sunrise=%s, sunset=%s, day=%.1fh",
		sSummer.Sunrise.Format("15:04"), sSummer.Sunset.Format("15:04"), sSummer.DayLength.Hours())
	t.Logf("NYC Winter solstice: sunrise=%s, sunset=%s, day=%.1fh",
		sWinter.Sunrise.Format("15:04"), sWinter.Sunset.Format("15:04"), sWinter.DayLength.Hours())

	// NYC summer day ~15h, winter ~9.3h
	if sSummer.DayLength.Hours() < 14 || sSummer.DayLength.Hours() > 16 {
		t.Errorf("summer day length %.1fh outside expected 14-16h", sSummer.DayLength.Hours())
	}
	if sWinter.DayLength.Hours() < 8.5 || sWinter.DayLength.Hours() > 10 {
		t.Errorf("winter day length %.1fh outside expected 8.5-10h", sWinter.DayLength.Hours())
	}

	// Summer days must be longer
	if sSummer.DayLength <= sWinter.DayLength {
		t.Error("summer day should be longer than winter")
	}
}

func TestGetSolarTimes_Equator(t *testing.T) {
	// Singapore (near equator): day length ~12h year-round
	lat, lng := 1.35, 103.82

	jan := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	jul := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	sJan := GetSolarTimes(lat, lng, jan, false)
	sJul := GetSolarTimes(lat, lng, jul, false)

	t.Logf("Singapore Jan: day=%.1fh, Jul: day=%.1fh", sJan.DayLength.Hours(), sJul.DayLength.Hours())

	// At equator, day length should be ~12h ± 0.5h all year
	if math.Abs(sJan.DayLength.Hours()-12) > 0.5 {
		t.Errorf("equator Jan day length %.1fh too far from 12h", sJan.DayLength.Hours())
	}
	if math.Abs(sJul.DayLength.Hours()-12) > 0.5 {
		t.Errorf("equator Jul day length %.1fh too far from 12h", sJul.DayLength.Hours())
	}
}

func TestSeasonalCAcrophaseShift(t *testing.T) {
	lat, lng := 40.71, -74.01 // NYC

	summer := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	winter := time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC)
	equinox := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	shiftSummer := SeasonalCAcrophaseShift(lat, lng, summer)
	shiftWinter := SeasonalCAcrophaseShift(lat, lng, winter)
	shiftEquinox := SeasonalCAcrophaseShift(lat, lng, equinox)

	t.Logf("NYC seasonal shift: summer=%.2fh, equinox=%.2fh, winter=%.2fh",
		shiftSummer, shiftEquinox, shiftWinter)

	// Summer: positive shift (later peak, longer days)
	if shiftSummer <= 0 {
		t.Errorf("summer shift should be positive, got %.2f", shiftSummer)
	}
	// Winter: negative shift (earlier peak, shorter days)
	if shiftWinter >= 0 {
		t.Errorf("winter shift should be negative, got %.2f", shiftWinter)
	}
	// Equinox: near zero
	if math.Abs(shiftEquinox) > 0.1 {
		t.Errorf("equinox shift should be near zero, got %.2f", shiftEquinox)
	}
	// Total range should be reasonable (~0.5-0.8h swing)
	totalSwing := shiftSummer - shiftWinter
	if totalSwing < 0.4 || totalSwing > 1.6 {
		t.Errorf("total summer-winter swing %.2fh outside expected 0.4-1.6h", totalSwing)
	}
}

func TestSeasonalCAcrophaseShift_Clamped(t *testing.T) {
	// Helsinki at midsummer: ~19h day length. Shift should be clamped at 0.8.
	lat, lng := 60.17, 24.94
	midsummer := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	shift := SeasonalCAcrophaseShift(lat, lng, midsummer)
	t.Logf("Helsinki midsummer shift: %.2fh (day=%.1fh)",
		shift, DayLengthHours(lat, lng, midsummer))

	if shift > 0.8 {
		t.Errorf("shift should be clamped at 0.8, got %.2f", shift)
	}
}

func TestCoordinatesFromSettings_NilFallback(t *testing.T) {
	lat, lng, isEst := CoordinatesFromSettings(nil)
	if !isEst {
		t.Error("nil settings should be an estimate")
	}
	if lat == 0 && lng == 0 {
		t.Error("nil settings should return non-zero fallback coordinates")
	}
}

func TestTimezoneCoords_Coverage(t *testing.T) {
	// Verify that common US timezones are in the map.
	for _, tz := range []string{
		"America/New_York", "America/Chicago", "America/Denver",
		"America/Los_Angeles", "Europe/London", "Asia/Tokyo",
		"Australia/Sydney", "Pacific/Auckland",
	} {
		if _, ok := timezoneCoords[tz]; !ok {
			t.Errorf("missing timezone %q in timezoneCoords map", tz)
		}
	}
}

func TestDayLengthHours(t *testing.T) {
	// Quick smoke test
	h := DayLengthHours(40.71, -74.01, time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC))
	if h < 10 || h > 20 {
		t.Errorf("day length %.1fh looks unreasonable for NYC summer", h)
	}
}
