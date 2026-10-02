package rest

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
)

type testDriver struct {
	mu       sync.Mutex
	lastExec struct {
		query string
		args  []driver.NamedValue
	}
}

var globalTestDriver = &testDriver{}

func init() {
	sql.Register("test_mock", globalTestDriver)
}

func (d *testDriver) Open(name string) (driver.Conn, error) {
	return &testConn{driver: d}, nil
}

type testConn struct {
	driver *testDriver
}

func (c *testConn) Prepare(query string) (driver.Stmt, error) {
	return &testStmt{conn: c, query: query}, nil
}

func (c *testConn) Close() error {
	return nil
}

func (c *testConn) Begin() (driver.Tx, error) {
	return &testTx{}, nil
}

type testStmt struct {
	conn  *testConn
	query string
}

func (s *testStmt) Close() error {
	return nil
}

func (s *testStmt) NumInput() int {
	return -1
}

func (s *testStmt) Exec(args []driver.Value) (driver.Result, error) {
	named := make([]driver.NamedValue, len(args))
	for i, arg := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: arg}
	}
	return s.ExecContext(context.Background(), named)
}

func (s *testStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.conn.driver.mu.Lock()
	defer s.conn.driver.mu.Unlock()
	s.conn.driver.lastExec.query = s.query
	s.conn.driver.lastExec.args = args
	return driver.RowsAffected(1), nil
}

func (s *testStmt) Query(args []driver.Value) (driver.Rows, error) {
	return &testRows{}, nil
}

type testRows struct{}

func (r *testRows) Columns() []string              { return []string{"1"} }
func (r *testRows) Close() error                   { return nil }
func (r *testRows) Next(dest []driver.Value) error { return io.EOF }

type testTx struct{}

func (t *testTx) Commit() error   { return nil }
func (t *testTx) Rollback() error { return nil }

func generateTestJWT(secret string, validitySec int64) string {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(time.Duration(validitySec) * time.Second).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte(secret))
	return tokenString
}

func TestController_InitAndPing(t *testing.T) {
	logger := zerolog.Nop()
	ctrl, err := NewController(":0", "http://localhost:8080", nil, nil, "", &logger)
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
	ctrl, err := NewController(":0", "http://localhost:8080", nil, nil, "", &logger)
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
	ctrl, err := NewController("127.0.0.1:0", "http://127.0.0.1:0", nil, nil, "", &logger)
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

func getArgString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	if ns, ok := v.(sql.NullString); ok && ns.Valid {
		return ns.String
	}
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func TestController_Inventory_Get_Success(t *testing.T) {
	logger := zerolog.Nop()
	db, err := sql.Open("test_mock", "")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}
	defer db.Close()

	ctrl, err := NewController(":0", "http://localhost:8080", nil, db, "", &logger)
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	rawBytes := make([]byte, 36)
	rawBytes[0] = 0x11
	rawBytes[1] = 0x01
	rawBytes[2] = 0x01
	copy(rawBytes[3:], []byte("30111000"))
	copy(rawBytes[21:], []byte("CH"))
	copy(rawBytes[23:], []byte("ISIL-123"))
	rawHex := hex.EncodeToString(rawBytes)

	urlStr := "/inventory?uid=E00401501234ABCD&itemid=30111000&country=CH&isil=ISIL-123&parts=1&partno=1&usagetype=1&version=1&marker=Regal%20A1&raw=" + rawHex + "&afi=C7&ts=1727886720000"
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, urlStr, nil)
	ctrl.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp InventoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got '%s'", resp.Status)
	}
	if resp.UID != "E00401501234ABCD" {
		t.Errorf("expected uid 'E00401501234ABCD', got '%s'", resp.UID)
	}
	if resp.ItemID != "30111000" {
		t.Errorf("expected itemid '30111000', got '%s'", resp.ItemID)
	}

	// Verify SQL query executed and marker / raw values in args
	globalTestDriver.mu.Lock()
	defer globalTestDriver.mu.Unlock()
	if !strings.Contains(globalTestDriver.lastExec.query, "INSERT INTO `inventory`") {
		t.Errorf("expected INSERT query, got: %s", globalTestDriver.lastExec.query)
	}
	if len(globalTestDriver.lastExec.args) >= 12 {
		markerStr := getArgString(globalTestDriver.lastExec.args[9].Value)
		if markerStr != "Regal A1" {
			t.Errorf("expected marker 'Regal A1', got: '%s' (%+v)", markerStr, globalTestDriver.lastExec.args[9].Value)
		}
		if rawArg, ok := globalTestDriver.lastExec.args[11].Value.([]byte); !ok || !bytes.Equal(rawArg, rawBytes) {
			t.Errorf("expected raw bytes %x, got: %+v", rawBytes, globalTestDriver.lastExec.args[11].Value)
		}
	}
}

func TestController_Inventory_Post_JSON_Success(t *testing.T) {
	logger := zerolog.Nop()
	db, err := sql.Open("test_mock", "")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}
	defer db.Close()

	ctrl, err := NewController(":0", "http://localhost:8080", nil, db, "", &logger)
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	bodyData := map[string]interface{}{
		"marker":     "Standort Regal 3",
		"uid":        "E00401509999ABCD",
		"timestamp":  1727886720000,
		"itemId":     "30119999",
		"country":    "CH",
		"isil":       "ISIL-456",
		"parts":      2,
		"partNo":     1,
		"usageType":  1,
		"version":    1,
		"afi":        "C7",
		"isCrcValid": true,
	}
	jsonBytes, _ := json.Marshal(bodyData)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/inventory", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	ctrl.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp InventoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.UID != "E00401509999ABCD" || resp.ItemID != "30119999" {
		t.Errorf("unexpected response: %+v", resp)
	}

	globalTestDriver.mu.Lock()
	defer globalTestDriver.mu.Unlock()
	if !strings.Contains(globalTestDriver.lastExec.query, "INSERT INTO `inventory`") {
		t.Errorf("expected INSERT query, got: %s", globalTestDriver.lastExec.query)
	}
	if len(globalTestDriver.lastExec.args) >= 10 {
		markerStr := getArgString(globalTestDriver.lastExec.args[9].Value)
		if markerStr != "Standort Regal 3" {
			t.Errorf("expected marker 'Standort Regal 3', got: '%s' (%+v)", markerStr, globalTestDriver.lastExec.args[9].Value)
		}
	}
}

func TestController_Inventory_RequiredFieldsAndEmptyNonEssential(t *testing.T) {
	logger := zerolog.Nop()
	db, err := sql.Open("test_mock", "")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}
	defer db.Close()

	ctrl, err := NewController(":0", "http://localhost:8080", nil, db, "", &logger)
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	// 1. Missing UID -> 400
	{
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/inventory?itemid=30118888", nil)
		ctrl.router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for missing uid, got %d", w.Code)
		}
	}

	// 2. Missing ItemID (even if raw is present) -> 400
	{
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/inventory?uid=E00401508888ABCD&raw=0102030405", nil)
		ctrl.router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for missing itemid, got %d", w.Code)
		}
	}

	// 3. Valid with empty optional fields (country, isil, sessionname, marker empty) -> 200
	{
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/inventory?uid=E00401508888ABCD&itemid=30118888&raw=AABBCC", nil)
		ctrl.router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 with empty optional fields, got %d: %s", w.Code, w.Body.String())
		}

		var resp InventoryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.ItemID != "30118888" || resp.UID != "E00401508888ABCD" {
			t.Errorf("unexpected response: %+v", resp)
		}
	}
}

func TestController_Inventory_JWT_Auth(t *testing.T) {
	logger := zerolog.Nop()
	db, err := sql.Open("test_mock", "")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}
	defer db.Close()

	secret := "secret-test-key-12345"
	ctrl, err := NewController(":0", "http://localhost:8080", nil, db, secret, &logger)
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	// 1. Missing token -> 401
	wMissing := httptest.NewRecorder()
	reqMissing, _ := http.NewRequest(http.MethodGet, "/inventory?uid=E00401501234ABCD&itemid=30111234", nil)
	ctrl.router.ServeHTTP(wMissing, reqMissing)
	if wMissing.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing token, got %d", wMissing.Code)
	}

	// 2. Expired token -> 401
	expiredToken := generateTestJWT(secret, -10)
	wExp := httptest.NewRecorder()
	reqExp, _ := http.NewRequest(http.MethodGet, "/inventory?uid=E00401501234ABCD&itemid=30111234", nil)
	reqExp.Header.Set("Authorization", "Bearer "+expiredToken)
	ctrl.router.ServeHTTP(wExp, reqExp)
	if wExp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d", wExp.Code)
	}

	// 3. Valid token in Header -> 200
	validToken := generateTestJWT(secret, 60)
	wValid := httptest.NewRecorder()
	reqValid, _ := http.NewRequest(http.MethodGet, "/inventory?uid=E00401501234ABCD&itemid=30111234", nil)
	reqValid.Header.Set("Authorization", "Bearer "+validToken)
	ctrl.router.ServeHTTP(wValid, reqValid)
	if wValid.Code != http.StatusOK {
		t.Errorf("expected 200 for valid token header, got %d: %s", wValid.Code, wValid.Body.String())
	}

	// 4. Valid token in query param -> 200
	wQuery := httptest.NewRecorder()
	reqQuery, _ := http.NewRequest(http.MethodGet, "/inventory?uid=E00401501234ABCD&itemid=30111234&jwt="+validToken, nil)
	ctrl.router.ServeHTTP(wQuery, reqQuery)
	if wQuery.Code != http.StatusOK {
		t.Errorf("expected 200 for valid token query, got %d: %s", wQuery.Code, wQuery.Body.String())
	}
}

func TestController_Inventory_MissingUID(t *testing.T) {
	logger := zerolog.Nop()
	db, err := sql.Open("test_mock", "")
	if err != nil {
		t.Fatalf("failed to open mock db: %v", err)
	}
	defer db.Close()

	ctrl, err := NewController(":0", "http://localhost:8080", nil, db, "", &logger)
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/inventory?itemid=30111000", nil)
	ctrl.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing uid, got %d", w.Code)
	}
}
