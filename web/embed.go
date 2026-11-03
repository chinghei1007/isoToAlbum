package web

import "embed"

// Dist contains the production Vite build. Run the frontend build before
// compiling the Go executable.
//
//go:embed dist
var Dist embed.FS
