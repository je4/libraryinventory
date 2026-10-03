package main

import (
	"io/fs"

	"emperror.dev/errors"
	"github.com/BurntSushi/toml"
	localconfig "github.com/je4/libraryinventory/config"
	"github.com/je4/utils/v2/pkg/config"
	"go.ub.unibas.ch/cloud/certloader/v2/pkg/loader"
)

// LibraryInventoryConfig holds the configuration settings for the service.
type LibraryInventoryConfig struct {
	// LocalAddr is the local network address the HTTP server listens on (e.g., ":8080").
	LocalAddr string `toml:"localaddr"`
	// ExternalAddr is the publicly accessible URL advertised in Swagger docs (e.g., "http://localhost:8080").
	ExternalAddr string `toml:"externaladdr"`
	// LogLevel sets the zerolog logging level (e.g. "DEBUG", "INFO", "WARN", "ERROR").
	LogLevel string `toml:"loglevel"`
	// LogFile specifies an optional log output file path (empty for console output).
	LogFile string `toml:"logfile"`
	// MySQLDSN specifies the data source name for MySQL connection, supporting environment variable expansion (e.g. "%%DB%%").
	MySQLDSN config.EnvString `toml:"mysqldsn"`
	// RESTTLS configures optional TLS certificate loading (e.g. "DEV", "CERT", "LETSENCRYPT").
	RESTTLS *loader.Config `toml:"resttls"`
	// JWTKey contains the HMAC secret key for JWT authentication verification, supporting environment expansion (e.g. "%%JWTKEY%%").
	JWTKey config.EnvString `toml:"jwtkey"`
}

// LoadLibraryInventoryConfig decodes default embedded TOML configuration and merges optional external TOML settings.
func LoadLibraryInventoryConfig(fSys fs.FS, fp string, conf *LibraryInventoryConfig) error {
	// First decode default embedded config
	if _, err := toml.Decode(localconfig.DefaultConfig, conf); err != nil {
		return errors.Wrap(err, "error decoding default config")
	}
	if fp == "" {
		return nil
	}
	// Read and overlay external configuration file
	data, err := fs.ReadFile(fSys, fp)
	if err != nil {
		return errors.Wrapf(err, "cannot read file [%v] %s", fSys, fp)
	}
	if _, err := toml.Decode(string(data), conf); err != nil {
		return errors.Wrapf(err, "error loading config file %v", fp)
	}
	if conf.RESTTLS != nil && conf.RESTTLS.Type == "" {
		conf.RESTTLS = nil
	}
	return nil
}
