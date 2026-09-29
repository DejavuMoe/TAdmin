// Package web embeds the verification Mini App front end (plain HTML, CSS and
// JavaScript; no build step). The page is served at /v/{token} and the other
// files under /assets/<name>.
package web

import "embed"

// FS holds index.html, app.css and app.js at its root.
//
//go:embed index.html app.css app.js
var FS embed.FS
