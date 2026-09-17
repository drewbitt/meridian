// Package templates contains the application's generated templ components.
//
// The blank import keeps the templ runtime visible to Go module tooling when
// generated *_templ.go files are absent, such as during Renovate artifact
// updates.
package templates

import (
	// Import templ so module tooling retains the generated code's runtime dependency.
	_ "github.com/a-h/templ"
)
