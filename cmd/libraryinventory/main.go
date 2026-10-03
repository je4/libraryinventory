// Package main provides the entry point for the Library Inventory REST web service.
package main

import (
	"crypto/tls"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/je4/libraryinventory/config"
	"github.com/je4/libraryinventory/pkg/rest"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.ub.unibas.ch/cloud/certloader/v2/pkg/loader"
)

// configfile defines the command-line flag for an optional custom TOML configuration file path.
var configfile = flag.String("config", "", "location of toml configuration file")

func main() {
	flag.Parse()

	// Determine filesystem and file name for loading configuration (external file or embedded default).
	var cfgFS fs.FS
	var cfgFile string
	if *configfile != "" {
		cfgFS = os.DirFS(filepath.Dir(*configfile))
		cfgFile = filepath.Base(*configfile)
	} else {
		cfgFS = config.ConfigFS
		cfgFile = "libraryinventory.toml"
	}

	conf := &LibraryInventoryConfig{}
	if err := LoadLibraryInventoryConfig(cfgFS, cfgFile, conf); err != nil {
		log.Fatal().Err(err).Msgf("cannot load toml from [%v] %s", cfgFS, cfgFile)
	}

	// Initialize zerolog structured logging with configured log level.
	level, err := zerolog.ParseLevel(conf.LogLevel)
	if err != nil {
		level = zerolog.DebugLevel
	}
	zerolog.SetGlobalLevel(level)
	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Configure TLS certificate loader if TLS configuration is provided.
	var restTLSConfig *tls.Config
	if conf.RESTTLS != nil {
		var restLoader io.Closer
		restTLSConfig, restLoader, err = loader.CreateServerLoader(false, conf.RESTTLS, nil, &logger)
		if err != nil {
			logger.Fatal().Err(err).Msg("cannot create server loader")
		}
		defer restLoader.Close()
	}

	// Initialize MySQL database connection pool.
	db, err := sql.Open("mysql", conf.MySQLDSN.String())
	if err != nil {
		logger.Fatal().Err(err).Msg("cannot open database")
	}
	db.SetConnMaxLifetime(time.Minute * 3)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)

	// Verify database connectivity.
	if err := db.Ping(); err != nil {
		logger.Fatal().Err(err).Msg("cannot ping database")
	}

	// Create and initialize the REST API controller.
	ctrl, err := rest.NewController(
		conf.LocalAddr,
		conf.ExternalAddr,
		restTLSConfig,
		db,
		conf.JWTKey.String(),
		&logger,
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("cannot create controller")
	}
	defer func(db *sql.DB) {
		if err := db.Close(); err != nil {
			logger.Error().Err(err).Msg("cannot close database")
		}
	}(db)

	// Start the REST server in a background goroutine.
	var wg = &sync.WaitGroup{}
	ctrl.Start(wg)

	// Wait for OS termination signals (SIGINT / SIGTERM) for graceful shutdown.
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
	fmt.Println("press ctrl+c to stop server")
	s := <-done
	fmt.Println("got signal:", s)

	// Perform graceful shutdown of the HTTP server.
	if err := ctrl.GracefulStop(); err != nil {
		logger.Error().Err(err).Msg("graceful stop failed")
	} else {
		wg.Wait()
	}
}
