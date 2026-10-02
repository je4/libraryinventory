package swaggerui

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed assets/*
var assetsFS embed.FS

//go:embed index.gohtml
var defaultIndexHTML string

type Config struct {
	Title   string
	URL     string
	DocFunc func() string
}

type Option func(*Config)

func WithTitle(title string) Option {
	return func(c *Config) {
		c.Title = title
	}
}

func WithURL(url string) Option {
	return func(c *Config) {
		c.URL = url
	}
}

func WithDocFunc(f func() string) Option {
	return func(c *Config) {
		c.DocFunc = f
	}
}

var indexTmpl = template.Must(template.New("index.html").Parse(defaultIndexHTML))

// CustomHandler returns a gin.HandlerFunc for Swagger UI
func CustomHandler(opts ...Option) gin.HandlerFunc {
	cfg := &Config{
		Title: "Swagger UI",
		URL:   "./doc.json",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	subFS, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	return func(c *gin.Context) {
		param := c.Param("any")
		filePath := strings.TrimPrefix(param, "/")

		if filePath == "" || filePath == "index.html" {
			var buf bytes.Buffer
			if err := indexTmpl.Execute(&buf, cfg); err != nil {
				c.String(http.StatusInternalServerError, "failed to render swagger ui: %v", err)
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", buf.Bytes())
			return
		}

		if filePath == "doc.json" || filePath == "openapi.json" || filePath == "swagger.json" {
			if cfg.DocFunc != nil {
				c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(cfg.DocFunc()))
				return
			}
			c.String(http.StatusNotFound, "openapi doc not configured")
			return
		}

		if f, err := subFS.Open(filePath); err == nil {
			_ = f.Close()
			c.Request.URL.Path = "/" + filePath
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		c.Status(http.StatusNotFound)
	}
}
