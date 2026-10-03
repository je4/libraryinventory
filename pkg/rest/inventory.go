package rest

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// InventoryRequest represents the incoming payload from GET query params or POST JSON body.
// It supports both camelCase and lowercase parameter variants for compatibility with different RFID scanner clients.
type InventoryRequest struct {
	// UID is the unique NFC/RFID tag identifier (e.g. 16 hex characters).
	UID string `json:"uid" form:"uid"`
	// Version is the RFID data model version number.
	Version int `json:"version" form:"version"`
	// UsageType specifies the library item usage category (e.g., circular, reference).
	UsageType int `json:"usageType" form:"usageType"`
	// UsageTypeAlt is an alias for UsageType ("usagetype").
	UsageTypeAlt int `json:"usagetype" form:"usagetype"`
	// Parts is the total number of parts for multi-volume items.
	Parts int `json:"parts" form:"parts"`
	// PartNo is the specific part number of this item.
	PartNo int `json:"partNo" form:"partNo"`
	// PartNoAlt is an alias for PartNo ("partno").
	PartNoAlt int `json:"partno" form:"partno"`
	// ItemID is the unique item barcode or accession identifier.
	ItemID string `json:"itemId" form:"itemId"`
	// ItemIDAlt is an alias for ItemID ("itemid").
	ItemIDAlt string `json:"itemid" form:"itemid"`
	// Country is the ISO 3166-1 alpha-2 country code (e.g., "CH").
	Country string `json:"country" form:"country"`
	// ISIL is the International Standard Identifier for Libraries (e.g., "CH-000008-7").
	ISIL string `json:"isil" form:"isil"`
	// Timestamp represents the scan time in milliseconds or seconds epoch.
	Timestamp int64 `json:"timestamp" form:"timestamp"`
	// TS is an alias for Timestamp ("ts").
	TS int64 `json:"ts" form:"ts"`
	// Text is a free-text field or location description, used as fallback for session name.
	Text string `json:"text" form:"text"`
	// Marker represents an optional shelf, rack, or tracking marker.
	Marker string `json:"marker" form:"marker"`
	// SessionName specifies the inventory session identifier.
	SessionName string `json:"sessionname" form:"sessionname"`
	// Session is an alias for SessionName ("session").
	Session string `json:"session" form:"session"`
	// AFI is the Application Family Identifier byte hex string.
	AFI string `json:"afi" form:"afi"`
	// Raw contains raw tag block bytes as hex or plain text.
	Raw string `json:"raw" form:"raw"`
	// JWT is an optional JWT token passed directly in request parameters.
	JWT string `json:"jwt" form:"jwt"`
	// IsCrcValid indicates whether tag CRC check succeeded.
	IsCrcValid *bool `json:"isCrcValid,omitempty" form:"isCrcValid"`
}

// InventoryResponse represents the standard JSON response returned upon successful record creation.
type InventoryResponse struct {
	// Status indicates success ("ok").
	Status string `json:"status" example:"ok"`
	// Message provides a human-readable confirmation message.
	Message string `json:"message" example:"inventory record created"`
	// InventoryID is the auto-increment database primary key of the inserted record.
	InventoryID int64 `json:"inventoryid,omitempty" example:"1"`
	// UID is the NFC tag UID associated with the record.
	UID string `json:"uid" example:"E00401501234ABCD"`
	// ItemID is the barcode/item identifier associated with the record.
	ItemID string `json:"itemid,omitempty" example:"30111234"`
}

// ErrorResponse represents an error response payload with status and descriptive message.
type ErrorResponse struct {
	// Status indicates failure ("error").
	Status string `json:"status" example:"error"`
	// Message contains the error description.
	Message string `json:"message" example:"error description"`
}

// InventoryRecord represents a normalized row ready for database insertion into the MySQL `inventory` table.
type InventoryRecord struct {
	UID           string
	Version       int
	UsageType     int
	Parts         int
	PartNo        int
	ItemID        string
	Country       string
	ISIL          string
	InventoryTime time.Time
	Marker        *string
	SessionName   string
	Raw           []byte
}

// verifyHS256Token validates that a JWT string is signed using HMAC (HS256, HS384, or HS512) with the provided secret.
func verifyHS256Token(tokenString, secret string) error {
	tokenString = strings.TrimSpace(tokenString)
	if tokenString == "" {
		return errors.New("empty token")
	}

	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{
		jwt.SigningMethodHS256.Alg(),
		jwt.SigningMethodHS384.Alg(),
		jwt.SigningMethodHS512.Alg(),
	}))
	if err != nil {
		return errors.Wrap(err, "invalid token")
	}

	if !token.Valid {
		return errors.New("token is invalid")
	}

	return nil
}

// checkAuth enforces JWT authentication if a jwtKey is configured on the controller.
// It searches for the JWT token in:
//  1. "Authorization: Bearer <token>" HTTP header
//  2. "jwt" URL query parameter
//  3. "jwt" field in the request payload
//
// Returns true if authenticated or if authentication is disabled, false otherwise.
func (ctrl *Controller) checkAuth(c *gin.Context, reqToken string) bool {
	if ctrl.jwtKey == "" {
		return true
	}

	token := ""
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			token = strings.TrimSpace(parts[1])
		} else {
			token = strings.TrimSpace(authHeader)
		}
	}
	if token == "" {
		token = strings.TrimSpace(c.Query("jwt"))
	}
	if token == "" {
		token = strings.TrimSpace(reqToken)
	}

	if token == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
			Status:  "error",
			Message: "unauthorized: missing authentication token",
		})
		return false
	}

	if err := verifyHS256Token(token, ctrl.jwtKey); err != nil {
		ctrl.logger.Warn().Err(err).Msg("JWT validation failed")
		c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
			Status:  "error",
			Message: fmt.Sprintf("unauthorized: %s", err.Error()),
		})
		return false
	}

	return true
}

// processInventoryRequest normalizes and validates incoming inventory scan requests.
// It applies field trimming, length limits matching the MySQL database schema, timestamp parsing,
// and raw payload hex-decoding.
func (ctrl *Controller) processInventoryRequest(req *InventoryRequest) (*InventoryRecord, error) {
	uid := strings.TrimSpace(req.UID)
	if uid == "" {
		return nil, errors.New("missing uid")
	}
	if len(uid) > 16 {
		uid = uid[:16]
	}

	version := req.Version
	usageType := req.UsageType
	if usageType == 0 && req.UsageTypeAlt != 0 {
		usageType = req.UsageTypeAlt
	}

	parts := req.Parts
	partNo := req.PartNo
	if partNo == 0 && req.PartNoAlt != 0 {
		partNo = req.PartNoAlt
	}

	itemID := strings.TrimSpace(req.ItemID)
	if itemID == "" {
		itemID = strings.TrimSpace(req.ItemIDAlt)
	}
	if itemID == "" {
		return nil, errors.New("missing itemid")
	}
	if len(itemID) > 16 {
		itemID = itemID[:16]
	}

	country := strings.TrimSpace(req.Country)
	if len(country) > 2 {
		country = country[:2]
	}

	isil := strings.TrimSpace(req.ISIL)
	if len(isil) > 11 {
		isil = isil[:11]
	}

	var rawBytes []byte
	rawStr := strings.TrimSpace(req.Raw)
	if rawStr != "" {
		decoded, err := hex.DecodeString(rawStr)
		if err == nil {
			rawBytes = decoded
		} else {
			rawBytes = []byte(rawStr)
		}
	}

	// Timestamp handling: support millisecond epoch (>100000000000), second epoch, or fallback to now.
	ts := req.Timestamp
	if ts == 0 && req.TS != 0 {
		ts = req.TS
	}

	var invTime time.Time
	if ts > 100000000000 {
		invTime = time.UnixMilli(ts)
	} else if ts > 0 {
		invTime = time.Unix(ts, 0)
	} else {
		invTime = time.Now()
	}

	// Session name & marker handling with length constraints.
	sessionName := strings.TrimSpace(req.SessionName)
	if sessionName == "" {
		sessionName = strings.TrimSpace(req.Session)
	}
	if sessionName == "" && req.Text != "" {
		sessionName = strings.TrimSpace(req.Text)
	}
	if len(sessionName) > 32 {
		sessionName = sessionName[:32]
	}

	var marker *string
	markerStr := strings.TrimSpace(req.Marker)
	if markerStr != "" {
		if len(markerStr) > 255 {
			markerStr = markerStr[:255]
		}
		marker = &markerStr
	}

	return &InventoryRecord{
		UID:           uid,
		Version:       version,
		UsageType:     usageType,
		Parts:         parts,
		PartNo:        partNo,
		ItemID:        itemID,
		Country:       country,
		ISIL:          isil,
		InventoryTime: invTime,
		Marker:        marker,
		SessionName:   sessionName,
		Raw:           rawBytes,
	}, nil
}

// insertInventory executes the SQL INSERT statement into the `inventory` table and returns the auto-generated ID.
func (ctrl *Controller) insertInventory(c *gin.Context, record *InventoryRecord) (int64, error) {
	if ctrl.db == nil {
		return 0, errors.New("database connection not configured")
	}

	query := "INSERT INTO `inventory` (`uid`, `version`, `usagetype`, `parts`, `partno`, `itemid`, `country`, `isil`, `inventorytime`, `marker`, `sessionname`, `raw`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	var markerVal sql.NullString
	if record.Marker != nil {
		markerVal = sql.NullString{String: *record.Marker, Valid: true}
	}

	res, err := ctrl.db.ExecContext(
		c.Request.Context(),
		query,
		record.UID,
		record.Version,
		record.UsageType,
		record.Parts,
		record.PartNo,
		record.ItemID,
		record.Country,
		record.ISIL,
		record.InventoryTime,
		markerVal,
		record.SessionName,
		record.Raw,
	)
	if err != nil {
		return 0, errors.Wrap(err, "failed to insert inventory record")
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, nil
	}
	return id, nil
}

// inventoryGet godoc
//
//	@Summary		Add inventory item via GET
//	@ID				get-inventory
//	@Description	Inserts NFC scan data into the inventory table via query parameters (requires JWT authentication if enabled)
//	@Tags			libraryinventory
//	@Security		BearerAuth
//	@Produce		json
//	@Param			uid			query		string	true	"NFC Tag UID (16 chars)"
//	@Param			itemid		query		string	true	"Item ID / Barcode (16 chars)"
//	@Param			country		query		string	false	"Country code (2 chars, e.g. CH)"
//	@Param			isil		query		string	false	"ISIL library code (11 chars)"
//	@Param			version		query		int		false	"Data model version"
//	@Param			usagetype	query		int		false	"Usage type"
//	@Param			parts		query		int		false	"Total parts"
//	@Param			partno		query		int		false	"Part number"
//	@Param			ts			query		int64	false	"Timestamp in milliseconds or seconds"
//	@Param			text		query		string	false	"User text / location / session name"
//	@Param			sessionname	query		string	false	"Session name (32 chars)"
//	@Param			marker		query		string	false	"Marker (255 chars)"
//	@Param			afi			query		string	false	"AFI byte hex"
//	@Param			raw			query		string	false	"Raw payload in hex or string format"
//	@Param			jwt			query		string	false	"JWT token for authentication (alternative to Bearer Authorization header)"
//	@Success		200			{object}	InventoryResponse
//	@Failure		400			{object}	ErrorResponse
//	@Failure		401			{object}	ErrorResponse
//	@Failure		500			{object}	ErrorResponse
//	@Router			/inventory [get]
func (ctrl *Controller) inventoryGet(c *gin.Context) {
	var req InventoryRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Status:  "error",
			Message: fmt.Sprintf("invalid query parameters: %s", err.Error()),
		})
		return
	}

	// Fallbacks for alternative query param names if not automatically bound
	if req.UID == "" {
		req.UID = c.Query("uid")
	}
	if req.ItemID == "" {
		req.ItemID = c.DefaultQuery("itemid", c.Query("itemId"))
	}
	if req.UsageType == 0 {
		if ut := c.DefaultQuery("usagetype", c.Query("usageType")); ut != "" {
			req.UsageType, _ = strconv.Atoi(ut)
		}
	}
	if req.PartNo == 0 {
		if pn := c.DefaultQuery("partno", c.Query("partNo")); pn != "" {
			req.PartNo, _ = strconv.Atoi(pn)
		}
	}
	if req.Timestamp == 0 && req.TS == 0 {
		if tsStr := c.DefaultQuery("ts", c.Query("timestamp")); tsStr != "" {
			req.Timestamp, _ = strconv.ParseInt(tsStr, 10, 64)
		}
	}

	if !ctrl.checkAuth(c, req.JWT) {
		return
	}

	record, err := ctrl.processInventoryRequest(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Status:  "error",
			Message: err.Error(),
		})
		return
	}

	id, err := ctrl.insertInventory(c, record)
	if err != nil {
		ctrl.logger.Error().Err(err).Msg("failed to insert inventory")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Status:  "error",
			Message: fmt.Sprintf("database error: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, InventoryResponse{
		Status:      "ok",
		Message:     "inventory record created",
		InventoryID: id,
		UID:         record.UID,
		ItemID:      record.ItemID,
	})
}

// inventoryPost godoc
//
//	@Summary		Add inventory item via POST
//	@ID				post-inventory
//	@Description	Inserts NFC scan data into the inventory table via JSON body or form/query parameters (requires JWT authentication if enabled)
//	@Tags			libraryinventory
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body		body		InventoryRequest	true	"Inventory scan payload"
//	@Success		200			{object}	InventoryResponse
//	@Failure		400			{object}	ErrorResponse
//	@Failure		401			{object}	ErrorResponse
//	@Failure		500			{object}	ErrorResponse
//	@Router			/inventory [post]
func (ctrl *Controller) inventoryPost(c *gin.Context) {
	var req InventoryRequest

	contentType := c.ContentType()
	if strings.Contains(contentType, "application/json") {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Status:  "error",
				Message: fmt.Sprintf("invalid json body: %s", err.Error()),
			})
			return
		}
	} else {
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Status:  "error",
				Message: fmt.Sprintf("invalid request body: %s", err.Error()),
			})
			return
		}
	}

	// Also merge any query parameters that may be present on the POST URL
	if req.UID == "" {
		req.UID = c.Query("uid")
	}
	if req.ItemID == "" {
		req.ItemID = c.DefaultQuery("itemid", c.Query("itemId"))
	}
	if req.Country == "" {
		req.Country = c.Query("country")
	}
	if req.ISIL == "" {
		req.ISIL = c.Query("isil")
	}
	if req.Text == "" {
		req.Text = c.Query("text")
	}
	if req.Raw == "" {
		req.Raw = c.Query("raw")
	}
	if req.JWT == "" {
		req.JWT = c.Query("jwt")
	}

	if !ctrl.checkAuth(c, req.JWT) {
		return
	}

	record, err := ctrl.processInventoryRequest(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Status:  "error",
			Message: err.Error(),
		})
		return
	}

	id, err := ctrl.insertInventory(c, record)
	if err != nil {
		ctrl.logger.Error().Err(err).Msg("failed to insert inventory")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Status:  "error",
			Message: fmt.Sprintf("database error: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, InventoryResponse{
		Status:      "ok",
		Message:     "inventory record created",
		InventoryID: id,
		UID:         record.UID,
		ItemID:      record.ItemID,
	})
}
