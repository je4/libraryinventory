package main

import (
	"io/fs"
	"os"

	"emperror.dev/errors"
	"github.com/BurntSushi/toml"
)

type LibraryInventoryConfig struct {
	LocalAddr    string `toml:"localaddr"`
	ExternalAddr string `toml:"externaladdr"`
	LogLevel     string `toml:"loglevel"`
	LogFile      string `toml:"logfile"`
}

func LoadLibraryInventoryConfig(fSys fs.FS, fp string, conf *LibraryInventoryConfig) error {
	if _, err := fs.Stat(fSys, fp); err != nil {
		path, err := os.Getwd()
		if err != nil {
			return errors.Wrap(err, "cannot get current working directory")
		}
		fSys = os.DirFS(path)
		fp = "libraryinventory.toml"
	}
	data, err := fs.ReadFile(fSys, fp)
	if err != nil {
		return errors.Wrapf(err, "cannot read file [%v] %s", fSys, fp)
	}
	_, err = toml.Decode(string(data), conf)
	if err != nil {
		return errors.Wrapf(err, "error loading config file %v", fp)
	}
	return nil
}
