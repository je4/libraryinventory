package rest

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
)

// loadEnvFile reads a .env file and sets environment variables if not already set.
func loadEnvFile(envPath string) error {
	f, err := os.Open(envPath)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

func findAndLoadEnv() (string, bool) {
	candidates := []string{
		".env",
		"../.env",
		"../../.env",
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if _, err := os.Stat(abs); err == nil {
				if err := loadEnvFile(abs); err == nil {
					return abs, true
				}
			}
		}
	}
	return "", false
}

func generateIntegrationJWT(secret string, validitySec int64) string {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(time.Duration(validitySec) * time.Second).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte(secret))
	return tokenString
}

type columnMeta struct {
	Field   string
	Type    string
	Null    string
	Key     string
	Default sql.NullString
	Extra   string
}

func validateInventorySchema(t *testing.T, db *sql.DB) {
	rows, err := db.Query("SHOW COLUMNS FROM `inventory`")
	if err != nil {
		t.Fatalf("[SCHEMA ERROR] Failed to inspect columns of 'inventory' table (%v)", err)
	}
	defer rows.Close()

	cols := make(map[string]columnMeta)
	for rows.Next() {
		var col columnMeta
		if err := rows.Scan(&col.Field, &col.Type, &col.Null, &col.Key, &col.Default, &col.Extra); err != nil {
			t.Fatalf("[SCHEMA ERROR] Failed to scan column metadata (%v)", err)
		}
		cols[strings.ToLower(col.Field)] = col
	}

	requiredColumns := []string{
		"inventoryid",
		"uid",
		"version",
		"usagetype",
		"parts",
		"partno",
		"itemid",
		"country",
		"isil",
		"inventorytime",
		"marker",
		"sessionname",
		"raw",
	}

	var missing []string
	for _, reqCol := range requiredColumns {
		if _, ok := cols[reqCol]; !ok {
			missing = append(missing, reqCol)
		}
	}

	if len(missing) > 0 {
		t.Fatalf("[SCHEMA ERROR] Table 'inventory' is missing required columns: %s\n\n"+
			"To fix the table schema, please update the table definition:\n\n"+
			"CREATE TABLE `inventory` (\n"+
			"  `inventoryid` bigint(20) NOT NULL AUTO_INCREMENT,\n"+
			"  `uid` char(16) NOT NULL,\n"+
			"  `version` int(11) NOT NULL,\n"+
			"  `usagetype` int(11) NOT NULL,\n"+
			"  `parts` int(11) NOT NULL,\n"+
			"  `partno` int(11) NOT NULL,\n"+
			"  `itemid` char(16) NOT NULL,\n"+
			"  `country` char(2) NOT NULL,\n"+
			"  `isil` char(11) NOT NULL,\n"+
			"  `inventorytime` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),\n"+
			"  `marker` varchar(255) DEFAULT NULL,\n"+
			"  `sessionname` char(32) NOT NULL,\n"+
			"  `raw` varbinary(2048) DEFAULT NULL COMMENT 'Rohdaten',\n"+
			"  PRIMARY KEY (`inventoryid`)\n"+
			");\n", strings.Join(missing, ", "))
	}

	// Verify inventoryid has AUTO_INCREMENT enabled
	invIdCol := cols["inventoryid"]
	if !strings.Contains(strings.ToLower(invIdCol.Extra), "auto_increment") {
		t.Fatalf("[SCHEMA ERROR] Column 'inventoryid' in table 'inventory' must have AUTO_INCREMENT enabled.\n\n" +
			"To fix this, execute the following SQL statement:\n\n" +
			"ALTER TABLE `inventory` MODIFY COLUMN `inventoryid` bigint(20) NOT NULL AUTO_INCREMENT, ADD PRIMARY KEY (`inventoryid`);\n")
	}
}

func TestIntegration_Inventory(t *testing.T) {
	envPath, envFound := findAndLoadEnv()
	if envFound {
		t.Logf("[DEBUG] Loaded environment configuration from: %s", envPath)
	} else {
		t.Logf("[WARN] No .env file discovered in standard search paths (.env, ../.env, ../../.env)")
	}

	dbDSN := os.Getenv("DB")
	if dbDSN == "" {
		t.Skip("[WARN] Skipping integration test: 'DB' environment variable is not defined")
	}

	jwtKey := os.Getenv("JWTKEY")
	if jwtKey != "" {
		t.Logf("[DEBUG] JWT authentication configured (JWTKEY length: %d)", len(jwtKey))
	} else {
		t.Logf("[DEBUG] No JWTKEY configured; testing without JWT enforcement")
	}

	db, err := sql.Open("mysql", dbDSN)
	if err != nil {
		t.Skipf("[WARN] Skipping integration test: Failed to initialize MySQL driver connection (%v)", err)
	}
	defer db.Close()

	// Verify database connectivity with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Skipf("[WARN] Skipping integration test: MySQL database is unreachable (%v)", err)
	}
	t.Logf("[DEBUG] Successfully connected and pinged MySQL database")

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM `inventory`").Scan(&count); err != nil {
		t.Fatalf("[SCHEMA ERROR] Table 'inventory' does not exist or is not accessible (%v).\n\nTo create the required table schema, execute the following SQL:\n\n"+
			"CREATE TABLE `inventory` (\n"+
			"  `inventoryid` bigint(20) NOT NULL AUTO_INCREMENT,\n"+
			"  `uid` char(16) NOT NULL,\n"+
			"  `version` int(11) NOT NULL,\n"+
			"  `usagetype` int(11) NOT NULL,\n"+
			"  `parts` int(11) NOT NULL,\n"+
			"  `partno` int(11) NOT NULL,\n"+
			"  `itemid` char(16) NOT NULL,\n"+
			"  `country` char(2) NOT NULL,\n"+
			"  `isil` char(11) NOT NULL,\n"+
			"  `inventorytime` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),\n"+
			"  `marker` varchar(255) DEFAULT NULL,\n"+
			"  `sessionname` char(32) NOT NULL,\n"+
			"  `raw` varbinary(2048) DEFAULT NULL COMMENT 'Rohdaten',\n"+
			"  PRIMARY KEY (`inventoryid`)\n"+
			");\n", err)
	}
	t.Logf("[DEBUG] Current row count in 'inventory' table: %d", count)

	// Validate existing schema without altering or creating table
	validateInventorySchema(t, db)

	var tableName, createStmt string
	if err := db.QueryRow("SHOW CREATE TABLE `inventory`").Scan(&tableName, &createStmt); err == nil {
		t.Logf("[DEBUG] Table definition for '%s':\n%s", tableName, createStmt)
	}

	logger := zerolog.Nop()
	ctrl, err := NewController(":0", "http://localhost", nil, db, jwtKey, &logger)
	if err != nil {
		t.Skipf("[WARN] Skipping integration test: Controller initialization failed (%v)", err)
	}

	t.Run("GET /inventory with query parameters, raw hex data, marker and database verification", func(t *testing.T) {
		token := generateIntegrationJWT(jwtKey, 3600)
		sessionName := fmt.Sprintf("test_session_%d", time.Now().UnixNano())
		testUID := "E004015011223344"
		testItemID := "ITEM998877"

		// Construct Finnish RFID raw binary block (36 bytes)
		rawBytes := make([]byte, 36)
		rawBytes[0] = 0x11 // version 1, usagetype 1
		rawBytes[1] = 1    // 1 part
		rawBytes[2] = 1    // part 1
		copy(rawBytes[3:], []byte(testItemID))
		copy(rawBytes[21:], []byte("CH"))
		copy(rawBytes[23:], []byte("ISIL-1234"))
		rawHex := hex.EncodeToString(rawBytes)

		params := url.Values{}
		params.Set("uid", testUID)
		params.Set("itemid", testItemID)
		params.Set("country", "CH")
		params.Set("isil", "ISIL-1234")
		params.Set("version", "1")
		params.Set("usagetype", "1")
		params.Set("parts", "1")
		params.Set("partno", "1")
		params.Set("sessionname", sessionName)
		params.Set("marker", "test_marker_get")
		params.Set("raw", rawHex)
		if jwtKey != "" {
			params.Set("jwt", token)
		}

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/inventory?"+params.Encode(), nil)
		ctrl.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected HTTP 200, got %d. Body: %s", w.Code, w.Body.String())
		}

		var resp InventoryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response JSON: %v", err)
		}

		if resp.Status != "ok" {
			t.Errorf("Expected status ok, got %s", resp.Status)
		}
		if resp.InventoryID == 0 {
			t.Errorf("Expected non-zero InventoryID in response")
		}

		// Verify record in database
		var (
			uid         string
			itemID      string
			country     string
			isil        string
			session     string
			marker      sql.NullString
			version     int
			usageType   int
			partsCount  int
			partNoCount int
			rawInDB     []byte
		)
		row := db.QueryRow("SELECT uid, itemid, country, isil, sessionname, marker, version, usagetype, parts, partno, raw FROM `inventory` WHERE `inventoryid` = ?", resp.InventoryID)
		if err := row.Scan(&uid, &itemID, &country, &isil, &session, &marker, &version, &usageType, &partsCount, &partNoCount, &rawInDB); err != nil {
			t.Fatalf("Failed to query inserted row from MySQL: %v", err)
		}

		if uid != testUID {
			t.Errorf("DB UID mismatch: expected %s, got %s", testUID, uid)
		}
		if itemID != testItemID {
			t.Errorf("DB itemid mismatch: expected %s, got %s", testItemID, itemID)
		}
		if country != "CH" {
			t.Errorf("DB country mismatch: expected CH, got %s", country)
		}
		if isil != "ISIL-1234" {
			t.Errorf("DB isil mismatch: expected ISIL-1234, got %s", isil)
		}
		if session != sessionName {
			t.Errorf("DB sessionname mismatch: expected %s, got %s", sessionName, session)
		}
		if !marker.Valid || marker.String != "test_marker_get" {
			t.Errorf("DB marker mismatch: expected test_marker_get, got %v", marker)
		}
		if version != 1 || usageType != 1 || partsCount != 1 || partNoCount != 1 {
			t.Errorf("DB numeric fields mismatch: got version=%d, usageType=%d, parts=%d, partno=%d", version, usageType, partsCount, partNoCount)
		}
		if !bytes.Equal(rawInDB, rawBytes) {
			t.Errorf("DB raw bytes mismatch: expected %x, got %x", rawBytes, rawInDB)
		}
	})

	t.Run("POST /inventory with JSON body, marker and raw data into database", func(t *testing.T) {
		token := generateIntegrationJWT(jwtKey, 3600)
		sessionName := fmt.Sprintf("post_session_%d", time.Now().UnixNano())
		testUID := "E0040150AABBCCDD"

		rawBytes := make([]byte, 36)
		rawBytes[0] = 0x11
		rawBytes[1] = 2
		rawBytes[2] = 1
		copy(rawBytes[3:], []byte("30111222333"))
		copy(rawBytes[21:], []byte("CH"))
		copy(rawBytes[23:], []byte("ISIL-ABCDE"))

		payload := InventoryRequest{
			UID:         testUID,
			ItemID:      "30111222333",
			Country:     "CH",
			ISIL:        "ISIL-ABCDE",
			Version:     1,
			UsageType:   1,
			Parts:       2,
			PartNo:      1,
			SessionName: sessionName,
			Marker:      "test_marker_post_json",
			Raw:         hex.EncodeToString(rawBytes),
		}
		bodyBytes, _ := json.Marshal(payload)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/inventory", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		if jwtKey != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		ctrl.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected HTTP 200, got %d. Body: %s", w.Code, w.Body.String())
		}

		var resp InventoryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response JSON: %v", err)
		}

		if resp.InventoryID == 0 {
			t.Fatalf("Expected non-zero InventoryID in response")
		}

		// Verify that data fields and marker were correctly stored in database
		var (
			uid         string
			itemID      string
			country     string
			isil        string
			session     string
			marker      sql.NullString
			version     int
			usageType   int
			partsCount  int
			partNoCount int
			rawInDB     []byte
		)
		row := db.QueryRow("SELECT uid, itemid, country, isil, sessionname, marker, version, usagetype, parts, partno, raw FROM `inventory` WHERE `inventoryid` = ?", resp.InventoryID)
		if err := row.Scan(&uid, &itemID, &country, &isil, &session, &marker, &version, &usageType, &partsCount, &partNoCount, &rawInDB); err != nil {
			t.Fatalf("Failed to query inserted row from MySQL: %v", err)
		}

		if uid != testUID {
			t.Errorf("DB UID mismatch: expected %s, got %s", testUID, uid)
		}
		if itemID != "30111222333" {
			t.Errorf("DB itemid mismatch: expected 30111222333, got %s", itemID)
		}
		if country != "CH" {
			t.Errorf("DB country mismatch: expected CH, got %s", country)
		}
		if isil != "ISIL-ABCDE" {
			t.Errorf("DB isil mismatch: expected ISIL-ABCDE, got %s", isil)
		}
		if session != sessionName {
			t.Errorf("DB sessionname mismatch: expected %s, got %s", sessionName, session)
		}
		if !marker.Valid || marker.String != "test_marker_post_json" {
			t.Errorf("DB marker mismatch: expected test_marker_post_json, got %v", marker)
		}
		if version != 1 || usageType != 1 || partsCount != 2 || partNoCount != 1 {
			t.Errorf("DB numeric fields mismatch: version=%d, usageType=%d, parts=%d, partno=%d", version, usageType, partsCount, partNoCount)
		}
		if !bytes.Equal(rawInDB, rawBytes) {
			t.Errorf("DB raw bytes mismatch: expected %x, got %x", rawBytes, rawInDB)
		}
	})

	t.Run("Validation: Missing UID or missing ItemID returns 400 Bad Request", func(t *testing.T) {
		token := generateIntegrationJWT(jwtKey, 3600)

		// Missing UID
		{
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, "/inventory?itemid=ITEM123", nil)
			if jwtKey != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			ctrl.router.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for missing uid, got %d", w.Code)
			}
		}

		// Missing ItemID
		{
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, "/inventory?uid=E004015099990000", nil)
			if jwtKey != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			ctrl.router.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for missing itemid, got %d", w.Code)
			}
		}
	})

	t.Run("POST /inventory with Form URL Encoded body into database", func(t *testing.T) {
		token := generateIntegrationJWT(jwtKey, 3600)
		sessionName := fmt.Sprintf("form_session_%d", time.Now().UnixNano())
		testUID := "E004015099887766"

		formData := url.Values{}
		formData.Set("uid", testUID)
		formData.Set("itemid", "FORMITEM123")
		formData.Set("country", "DE")
		formData.Set("isil", "ISIL-DE1")
		formData.Set("version", "2")
		formData.Set("usagetype", "1")
		formData.Set("parts", "3")
		formData.Set("partno", "2")
		formData.Set("sessionname", sessionName)
		formData.Set("marker", "form_marker")
		if jwtKey != "" {
			formData.Set("jwt", token)
		}

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/inventory", strings.NewReader(formData.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		ctrl.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected HTTP 200, got %d. Body: %s", w.Code, w.Body.String())
		}

		var resp InventoryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response JSON: %v", err)
		}

		if resp.InventoryID == 0 {
			t.Fatalf("Expected non-zero InventoryID in response")
		}

		var (
			uid     string
			itemID  string
			country string
			isil    string
			session string
			marker  sql.NullString
			parts   int
			partNo  int
		)
		row := db.QueryRow("SELECT uid, itemid, country, isil, sessionname, marker, parts, partno FROM `inventory` WHERE `inventoryid` = ?", resp.InventoryID)
		if err := row.Scan(&uid, &itemID, &country, &isil, &session, &marker, &parts, &partNo); err != nil {
			t.Fatalf("Failed to query inserted form row: %v", err)
		}

		if uid != testUID || itemID != "FORMITEM123" || country != "DE" || isil != "ISIL-DE1" || session != sessionName || parts != 3 || partNo != 2 {
			t.Errorf("DB form record mismatch: uid=%s, itemid=%s, country=%s, isil=%s, session=%s, parts=%d, partno=%d",
				uid, itemID, country, isil, session, parts, partNo)
		}
		if !marker.Valid || marker.String != "form_marker" {
			t.Errorf("DB marker mismatch: expected form_marker, got %v", marker)
		}
	})

	t.Run("GET /api/inventory with expired JWT token returns 401", func(t *testing.T) {
		if jwtKey == "" {
			t.Skip("Skipping JWT test because JWTKEY is not set")
		}
		expiredToken := generateIntegrationJWT(jwtKey, -3600) // expired 1 hour ago
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/inventory?uid=E004015099998888&jwt="+expiredToken, nil)
		ctrl.router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected HTTP 401 Unauthorized for expired token, got %d", w.Code)
		}
	})

	t.Run("GET /api/inventory alias endpoint unauthorized without JWT", func(t *testing.T) {
		if jwtKey == "" {
			t.Skip("Skipping JWT test because JWTKEY is not set")
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/inventory?uid=E004015099998888", nil)
		ctrl.router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected HTTP 401 Unauthorized, got %d", w.Code)
		}
	})
}
