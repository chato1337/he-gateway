// Package web holds the administration dashboard embedded in the gateway binary.
package web

import _ "embed"

// IndexHTML is the single-page dashboard served at the gateway root.
//
//go:embed index.html
var IndexHTML []byte
