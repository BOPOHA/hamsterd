package proxyconfig

import _ "embed"

// DefaultConfigContent provides default json unitConfig file.
//go:embed files/config.json
var DefaultConfigContent []byte

var DefaultHttpScoket = ":8081"
