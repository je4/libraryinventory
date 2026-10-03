// Package config provides default configuration files embedded at compile time.
package config

import "embed"

// ConfigFS provides an embedded filesystem containing the default libraryinventory.toml file.
//
//go:embed libraryinventory.toml
var ConfigFS embed.FS

// DefaultConfig provides the raw string content of the default libraryinventory.toml file.
//
//go:embed libraryinventory.toml
var DefaultConfig string
