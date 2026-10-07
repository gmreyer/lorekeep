package editor

import "embed"

// staticFiles are served under /static/: the stylesheets, keys.js, the
// vendored htmx and the fonts. Nothing is fetched from the network, so the
// editor works offline from the single exe.
//
//go:embed static
var staticFiles embed.FS
