package config

import "embed"

//go:embed libraryinventory.toml
var ConfigFS embed.FS

//go:embed libraryinventory.toml
var DefaultConfig string
