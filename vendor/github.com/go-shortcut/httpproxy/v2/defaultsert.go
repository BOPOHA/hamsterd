package httpproxy

import _ "embed"

// DefaultCaCert provides default CA certificate.
//go:embed ca_cert.pem
var DefaultCaCert []byte

// DefaultCaKey provides default CA key.
//go:embed ca_key.pem
var DefaultCaKey []byte
