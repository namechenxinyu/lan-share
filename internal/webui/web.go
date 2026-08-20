package webui

import "embed"

// Assets contains the browser UI bundled into the executable.
//
//go:embed index.html style.css app.js v07.js
var Assets embed.FS
