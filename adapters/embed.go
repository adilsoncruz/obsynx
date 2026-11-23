package adapters

import "embed"

// EmbeddedCapabilities holds the built-in capability contracts for all official adapters.
//
//go:embed */capabilities.yaml
var EmbeddedCapabilities embed.FS
