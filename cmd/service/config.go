package main

import (
	"io/fs"

	"emperror.dev/errors"
	"github.com/BurntSushi/toml"
	localconfig "github.com/je4/libraryinventory/config"
	"github.com/je4/utils/v2/pkg/config"
	"go.ub.unibas.ch/cloud/certloader/v2/pkg/loader"
)

type LibraryInventoryConfig struct {
	LocalAddr    string           `toml:"localaddr"`
	ExternalAddr string           `toml:"externaladdr"`
	LogLevel     string           `toml:"loglevel"`
	LogFile      string           `toml:"logfile"`
	MySQLDSN     config.EnvString `toml:"mysqldsn"`
	RESTTLS      *loader.Config   `toml:"resttls"`
	JWTKey       config.EnvString `toml:"jwtkey"`
}

func LoadLibraryInventoryConfig(fSys fs.FS, fp string, conf *LibraryInventoryConfig) error {
	if _, err := toml.Decode(localconfig.DefaultConfig, conf); err != nil {
		return errors.Wrap(err, "error decoding default config")
	}
	if fp == "" {
		return nil
	}
	data, err := fs.ReadFile(fSys, fp)
	if err != nil {
		return errors.Wrapf(err, "cannot read file [%v] %s", fSys, fp)
	}
	if _, err := toml.Decode(string(data), conf); err != nil {
		return errors.Wrapf(err, "error loading config file %v", fp)
	}
	return nil
}
