package ingest

import "errors"

var (
	// ErrNoSleepData is returned when a file is valid but contains no sleep records.
	ErrNoSleepData = errors.New("no sleep data found")

	// ErrParseFailed is returned when a file cannot be parsed due to format errors.
	ErrParseFailed = errors.New("parse failed")

	// ErrUnknownSource is returned when an unsupported source type is requested.
	ErrUnknownSource = errors.New("unknown source")

	// ErrInvalidFile is returned when a file cannot be opened or read.
	ErrInvalidFile = errors.New("invalid file format")
)
