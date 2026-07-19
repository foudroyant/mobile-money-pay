// Package templates embeds the server-rendered HTML templates (pages +
// HTMX partials) so the binary has no runtime dependency on the local
// filesystem.
package templates

import "embed"

//go:embed layout.html pages/*.html partials/*.html
var FS embed.FS
