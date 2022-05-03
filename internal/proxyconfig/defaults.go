package proxyconfig

import "embed"

// EmbeddedFS provides FS with default config files.
//go:embed files/*.json
var EmbeddedFS embed.FS

var DefaultHttpScoket = ":8081"
