package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/je4/libraryinventory/config"
	"github.com/je4/libraryinventory/pkg/rest"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.ub.unibas.ch/cloud/certloader/v2/pkg/loader"
)

var configfile = flag.String("config", "", "location of toml configuration file")

func main() {
	flag.Parse()

	var cfgFS fs.FS
	var cfgFile string
	if *configfile != "" {
		cfgFS = os.DirFS(filepath.Dir(*configfile))
		cfgFile = filepath.Base(*configfile)
	} else {
		cfgFS = config.ConfigFS
		cfgFile = "libraryinventory.toml"
	}

	conf := &LibraryInventoryConfig{
		LocalAddr:    ":8080",
		ExternalAddr: "http://localhost:8080",
		LogLevel:     "DEBUG",
	}
	if err := LoadLibraryInventoryConfig(cfgFS, cfgFile, conf); err != nil {
		log.Fatal().Err(err).Msgf("cannot load toml from [%v] %s", cfgFS, cfgFile)
	}

	level, err := zerolog.ParseLevel(conf.LogLevel)
	if err != nil {
		level = zerolog.DebugLevel
	}
	zerolog.SetGlobalLevel(level)
	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	var restTLSConfig *tls.Config
	if conf.RESTTLS != nil {
		var restLoader io.Closer
		restTLSConfig, restLoader, err = loader.CreateServerLoader(false, conf.RESTTLS, nil, &logger)
		if err != nil {
			logger.Fatal().Err(err).Msg("cannot create server loader")
		}
		defer restLoader.Close()
	}

	ctrl, err := rest.NewController(
		conf.LocalAddr,
		conf.ExternalAddr,
		restTLSConfig,
		&logger,
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("cannot create controller")
	}

	var wg = &sync.WaitGroup{}
	ctrl.Start(wg)

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
	fmt.Println("press ctrl+c to stop server")
	s := <-done
	fmt.Println("got signal:", s)

	if err := ctrl.GracefulStop(); err != nil {
		logger.Error().Err(err).Msg("graceful stop failed")
	} else {
		wg.Wait()
	}
}
