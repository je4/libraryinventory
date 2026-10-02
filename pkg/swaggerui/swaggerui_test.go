package swaggerui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestSwaggerUIHandler(t *testing.T) {
	r := gin.New()
	sampleDoc := `{"openapi":"3.1.0","info":{"title":"Test API","version":"1.0"}}`

	r.GET("/swagger/*any", CustomHandler(
		WithTitle("My API Docs"),
		WithDocFunc(func() string {
			return sampleDoc
		}),
	))

	tests := []struct {
		name         string
		path         string
		expectedCode int
		expectedType string
		bodyContains string
	}{
		{
			name:         "Index HTML root",
			path:         "/swagger/",
			expectedCode: http.StatusOK,
			expectedType: "text/html",
			bodyContains: "<title>My API Docs</title>",
		},
		{
			name:         "Index HTML explicit",
			path:         "/swagger/index.html",
			expectedCode: http.StatusOK,
			expectedType: "text/html",
			bodyContains: "SwaggerUIBundle",
		},
		{
			name:         "Doc JSON",
			path:         "/swagger/doc.json",
			expectedCode: http.StatusOK,
			expectedType: "application/json",
			bodyContains: `"openapi":"3.1.0"`,
		},
		{
			name:         "OpenAPI JSON",
			path:         "/swagger/openapi.json",
			expectedCode: http.StatusOK,
			expectedType: "application/json",
			bodyContains: `"openapi":"3.1.0"`,
		},
		{
			name:         "Swagger JSON",
			path:         "/swagger/swagger.json",
			expectedCode: http.StatusOK,
			expectedType: "application/json",
			bodyContains: `"openapi":"3.1.0"`,
		},
		{
			name:         "CSS asset",
			path:         "/swagger/swagger-ui.css",
			expectedCode: http.StatusOK,
			expectedType: "text/css",
			bodyContains: ".swagger-ui",
		},
		{
			name:         "JS bundle asset",
			path:         "/swagger/swagger-ui-bundle.js",
			expectedCode: http.StatusOK,
			expectedType: "text/javascript",
			bodyContains: "SwaggerUIBundle",
		},
		{
			name:         "JS standalone preset asset",
			path:         "/swagger/swagger-ui-standalone-preset.js",
			expectedCode: http.StatusOK,
			expectedType: "text/javascript",
			bodyContains: "SwaggerUIStandalonePreset",
		},
		{
			name:         "Favicon 32",
			path:         "/swagger/favicon-32x32.png",
			expectedCode: http.StatusOK,
			expectedType: "image/png",
		},
		{
			name:         "Favicon 16",
			path:         "/swagger/favicon-16x16.png",
			expectedCode: http.StatusOK,
			expectedType: "image/png",
		},
		{
			name:         "OAuth2 redirect",
			path:         "/swagger/oauth2-redirect.html",
			expectedCode: http.StatusOK,
			expectedType: "text/html",
		},
		{
			name:         "Nonexistent file",
			path:         "/swagger/unknown.xyz",
			expectedCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, tt.path, nil)
			r.ServeHTTP(w, req)

			if w.Code != tt.expectedCode {
				t.Fatalf("path %s: expected status %d, got %d", tt.path, tt.expectedCode, w.Code)
			}

			if tt.expectedType != "" {
				contentType := w.Header().Get("Content-Type")
				if !strings.Contains(contentType, tt.expectedType) {
					t.Errorf("path %s: expected content type containing %s, got %s", tt.path, tt.expectedType, contentType)
				}
			}

			if tt.bodyContains != "" {
				body := w.Body.String()
				if !strings.Contains(body, tt.bodyContains) {
					t.Errorf("path %s: expected body containing %q, got: %s", tt.path, tt.bodyContains, body)
				}
			}
		})
	}
}

func TestSwaggerUIHandler_NoDocFunc(t *testing.T) {
	r := gin.New()
	r.GET("/swagger/*any", CustomHandler(WithURL("./custom.json")))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"./custom.json"`) {
		t.Errorf("expected custom URL in index.html, got: %s", w.Body.String())
	}

	wDoc := httptest.NewRecorder()
	reqDoc, _ := http.NewRequest(http.MethodGet, "/swagger/doc.json", nil)
	r.ServeHTTP(wDoc, reqDoc)

	if wDoc.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 when no doc func configured, got %d", wDoc.Code)
	}
}
