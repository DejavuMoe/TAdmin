package web

import "embed"

// Files contains the native Mini App assets. No frontend build step is needed.
//
//go:embed index.html app.css app.js
var Files embed.FS
