package main

import _ "embed"

// EmbeddedFS provides FS with default config files.
//go:embed templates/index.j2
var j2Index string

type paramsJ2Index struct {
	ReqHost string
	CaURL   string
}
