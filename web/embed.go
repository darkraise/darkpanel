// Package web embeds the built web UI.
package web

import "embed"

// Dist holds the Vite build output, rooted at dist/.
//
//go:embed all:dist
var Dist embed.FS
