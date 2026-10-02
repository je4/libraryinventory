package rest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestController_InitAndPing(t *testing.T) {
	logger := zerolog.Nop()
	ctrl, err := NewController(":0", "http://localhost:8080", nil, &logger)
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/ping", nil)
	ctrl.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestController_Swagger(t *testing.T) {
	logger := zerolog.Nop()
	ctrl, err := NewController(":0", "http://localhost:8080", nil, &logger)
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	req.RequestURI = "/swagger/index.html"
	ctrl.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d for swagger UI, got %d", http.StatusOK, w.Code)
	}

	indexHtml := w.Body.String()
	if strings.Contains(indexHtml, "unpkg.com") {
		t.Errorf("expected swagger UI to not use external CDN, but found unpkg.com in index.html")
	}
	if !strings.Contains(indexHtml, "./swagger-ui-bundle.js") {
		t.Errorf("expected swagger UI to reference local swagger-ui-bundle.js")
	}

	wDoc := httptest.NewRecorder()
	reqDoc, _ := http.NewRequest(http.MethodGet, "/swagger/doc.json", nil)
	reqDoc.RequestURI = "/swagger/doc.json"
	ctrl.router.ServeHTTP(wDoc, reqDoc)

	if wDoc.Code != http.StatusOK {
		t.Errorf("expected status %d for swagger doc.json, got %d", http.StatusOK, wDoc.Code)
	}

	body := wDoc.Body.String()
	if !strings.Contains(body, `"openapi": "3.1.0"`) {
		t.Errorf("expected doc.json to contain openapi 3.1.0, got: %s", body)
	}

	wOpenApi := httptest.NewRecorder()
	reqOpenApi, _ := http.NewRequest(http.MethodGet, "/swagger/openapi.json", nil)
	reqOpenApi.RequestURI = "/swagger/openapi.json"
	ctrl.router.ServeHTTP(wOpenApi, reqOpenApi)

	if wOpenApi.Code != http.StatusOK {
		t.Errorf("expected status %d for swagger openapi.json, got %d", http.StatusOK, wOpenApi.Code)
	}

	wJs := httptest.NewRecorder()
	reqJs, _ := http.NewRequest(http.MethodGet, "/swagger/swagger-ui-bundle.js", nil)
	reqJs.RequestURI = "/swagger/swagger-ui-bundle.js"
	ctrl.router.ServeHTTP(wJs, reqJs)

	if wJs.Code != http.StatusOK {
		t.Errorf("expected status %d for swagger-ui-bundle.js, got %d", http.StatusOK, wJs.Code)
	}

	wCss := httptest.NewRecorder()
	reqCss, _ := http.NewRequest(http.MethodGet, "/swagger/swagger-ui.css", nil)
	reqCss.RequestURI = "/swagger/swagger-ui.css"
	ctrl.router.ServeHTTP(wCss, reqCss)

	if wCss.Code != http.StatusOK {
		t.Errorf("expected status %d for swagger-ui.css, got %d", http.StatusOK, wCss.Code)
	}
}

func TestController_StartAndStop(t *testing.T) {
	logger := zerolog.Nop()
	ctrl, err := NewController("127.0.0.1:0", "http://127.0.0.1:0", nil, &logger)
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	wg := &sync.WaitGroup{}
	ctrl.Start(wg)

	time.Sleep(50 * time.Millisecond)

	if err := ctrl.GracefulStop(); err != nil {
		t.Errorf("graceful stop failed: %v", err)
	}

	wg.Wait()
}
