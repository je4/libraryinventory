// Package rest provides HTTP handlers, router configuration, and middleware for the Library Inventory service.
package rest

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/je4/libraryinventory/pkg/rest/docs"
	"github.com/rs/zerolog"
	"go.ub.unibas.ch/cloud/swaggerui"
)

//	@title			Library Inventory API
//	@version		1.0
//	@description	Library Inventory Web Service
//	@termsOfService	http://swagger.io/terms/

//	@contact.name	Jürgen Enge
//	@contact.url	https://ub.unibas.ch
//	@contact.email	juergen.enge@unibas.ch

//	@license.name	Apache 2.0
//	@license.url	http://www.apache.org/licenses/LICENSE-2.0.html

//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization

// Controller manages HTTP routing, server lifecycle, database access, and authentication for REST endpoints.
type Controller struct {
	server  http.Server
	router  *gin.Engine
	addr    string
	extAddr string
	subpath string
	db      *sql.DB
	logger  *zerolog.Logger
	jwtKey  string
}

// NewController instantiates and initializes a new REST Controller with Swagger metadata, logging, and router middleware.
func NewController(addr, extAddr string, tlsConfig *tls.Config, db *sql.DB, jwtKey string, logger *zerolog.Logger) (*Controller, error) {
	u, err := url.Parse(extAddr)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid external address '%s'", extAddr)
	}
	subpath := "/" + strings.Trim(u.Path, "/")

	// programmatically set swagger info
	docs.SwaggerInfoLibraryInventory.Host = strings.TrimRight(fmt.Sprintf("%s:%s", u.Hostname(), u.Port()), " :")
	docs.SwaggerInfoLibraryInventory.BasePath = "/" + strings.Trim(subpath, "/")
	if u.Scheme != "" {
		docs.SwaggerInfoLibraryInventory.Schemes = []string{u.Scheme}
	} else {
		docs.SwaggerInfoLibraryInventory.Schemes = []string{"http", "https"}
	}

	router := gin.Default()

	var subLogger zerolog.Logger
	if logger != nil {
		subLogger = logger.With().Str("component", "restController").Logger()
	} else {
		subLogger = zerolog.Nop()
	}

	c := &Controller{
		addr:    addr,
		extAddr: extAddr,
		subpath: subpath,
		router:  router,
		db:      db,
		jwtKey:  jwtKey,
		logger:  &subLogger,
	}

	if err := c.Init(tlsConfig); err != nil {
		return nil, errors.Wrap(err, "cannot initialize rest controller")
	}

	return c, nil
}

// Init configures middleware (CORS), registers API endpoints, mounts Swagger UI, and prepares the underlying http.Server.
func (ctrl *Controller) Init(tlsConfig *tls.Config) error {
	// CORS Middleware Configuration
	ctrl.router.Use(cors.Default())

	// Route registrations
	ctrl.router.GET("/ping", ctrl.ping)
	ctrl.router.GET("/inventory", ctrl.inventoryGet)
	ctrl.router.POST("/inventory", ctrl.inventoryPost)
	ctrl.router.GET("/api/inventory", ctrl.inventoryGet)
	ctrl.router.POST("/api/inventory", ctrl.inventoryPost)

	ctrl.router.GET("/swagger/*any", swaggerui.CustomHandler(
		swaggerui.WithTitle("Library Inventory API"),
		swaggerui.WithDocFunc(func() string {
			return docs.SwaggerInfoLibraryInventory.ReadDoc()
		}),
	))

	ctrl.server = http.Server{
		Addr:      ctrl.addr,
		Handler:   ctrl.router,
		TLSConfig: tlsConfig,
	}

	return nil
}

// ping godoc
//
//	@Summary		does ping
//	@ID				get-ping
//	@Description	for testing if server is running
//	@Tags			libraryinventory
//	@Produce		json
//	@Success		200	{object}	map[string]string
//	@Router			/ping [get]
func (ctrl *Controller) ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

// Start launches the HTTP or HTTPS server asynchronously and tracks its completion with the provided WaitGroup.
func (ctrl *Controller) Start(wg *sync.WaitGroup) {
	go func() {
		wg.Add(1)
		defer wg.Done()

		if ctrl.server.TLSConfig == nil {
			ctrl.logger.Info().Msgf("starting server at http://%s", ctrl.addr)
			if err := ctrl.server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				ctrl.logger.Error().Err(err).Msgf("server on '%s' ended unexpectedly", ctrl.addr)
			}
		} else {
			ctrl.logger.Info().Msgf("starting server at https://%s", ctrl.addr)
			if err := ctrl.server.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
				ctrl.logger.Error().Err(err).Msgf("server on '%s' ended unexpectedly", ctrl.addr)
			}
		}
	}()
}

// Stop shuts down the HTTP server immediately.
func (ctrl *Controller) Stop() {
	_ = ctrl.server.Shutdown(context.Background())
}

// GracefulStop gracefully drains existing connections and shuts down the HTTP server.
func (ctrl *Controller) GracefulStop() error {
	return errors.WithStack(ctrl.server.Shutdown(context.Background()))
}
