// Package static embeds the vendored htmx assets and app stylesheet so the
// binary has no runtime dependency on a CDN or the local filesystem.
package static

import "embed"

//go:embed vendor app.css app.js
var FS embed.FS
