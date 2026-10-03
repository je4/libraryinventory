# Functional and Technical Specification

This document provides a detailed overview of the functionality, system architecture, data normalization rules, security model, and database design of the **Library Inventory Service**.

---

## 1. Domain Context & Purpose

The **Library Inventory Service** is a dedicated REST microservice engineered for modern library inventory tracking, collection audits, and item localization.

In modern libraries, RFID (Radio Frequency Identification) tags based on ISO/IEC 15693 and data models like ISO 28560 are affixed to physical books, audiovisual media, and bound serials. Handheld scanning devices (or portable wand readers) sweep across library shelves, reading NFC/RFID tag UIDs and their encoded item barcodes at high speed.

The Library Inventory Service acts as the centralized data ingestion point for these readers. It provides:
1. **Low-Latency Ingestion**: Rapidly captures RFID tag scans via lightweight GET query parameters or structured POST JSON bodies.
2. **Data Model Normalization**: Adapts disparate client payload formats, handles aliases, clamps string bounds according to storage constraints, and parses variable-precision timestamps.
3. **Audit Trail & Localization**: Records shelf markers, inventory sessions, multi-volume part indices, and raw tag memory bytes alongside canonical item IDs and tag UIDs.
4. **Security & Authentication**: Protects ingestion endpoints using HMAC-signed JWT tokens to ensure only authorized scanning hardware and staff devices can submit inventory records.

---

## 2. System Architecture

The service is composed of modular components designed for high performance, ease of configuration, and operational reliability:

```text
+-------------------------------------------------------------------------+
|                              Clients                                    |
|   (RFID Handheld Scanner / Mobile App / Barcode Wand / Web Interface)   |
+-------------------------------------------------------------------------+
                                     |
                       HTTP / HTTPS (TLS via certloader)
                                     v
+-------------------------------------------------------------------------+
|                         REST Web Service (Gin)                          |
|                                                                         |
|  +--------------------+   +---------------------+   +----------------+  |
|  |   CORS Middleware  |-->|  JWT Authentication |-->| Route Handlers |  |
|  +--------------------+   +---------------------+   +----------------+  |
|                                                              |          |
|                                                              v          |
|                                                   +------------------+  |
|                                                   | Request          |  |
|                                                   | Normalization    |  |
|                                                   +------------------+  |
|                                                              |          |
|                                                              v          |
|                                                   +------------------+  |
|                                                   | MySQL ExecContext|  |
|                                                   +------------------+  |
+-------------------------------------------------------------------------+
                                     |
                        SQL Protocol (Connection Pool)
                                     v
+-------------------------------------------------------------------------+
|                       MySQL / MariaDB Database                          |
|                          Table: `inventory`                             |
+-------------------------------------------------------------------------+
```

### Core Components

1. **Configuration Loader (`cmd/libraryinventory/config.go` & `config/embed.go`)**:
   - Embeds the default TOML configuration into the compiled binary via `go:embed`.
   - Supports external configuration files specified via `-config <path>`.
   - Dynamically resolves environment variables enclosed in `%%...%%` (e.g. `%%DB%%`, `%%JWTKEY%%`).

2. **TLS Certificate Loader (`go.ub.unibas.ch/cloud/certloader/v2`)**:
   - Manages secure HTTPS endpoints with support for dynamic reloading and multiple certificate providers (`DEV`, `CERT`, `LETSENCRYPT`).

3. **REST Controller & Router (`pkg/rest/web.go`)**:
   - Built on the high-performance `gin-gonic/gin` HTTP framework.
   - Configured with CORS support for cross-origin browser applications.
   - Embeds Swagger UI documentation (`go.ub.unibas.ch/cloud/swaggerui`) for interactive API testing.

4. **Business Logic & Normalizer (`pkg/rest/inventory.go`)**:
   - Implements authentication checking (`checkAuth`), payload normalization (`processInventoryRequest`), and MySQL statement execution (`insertInventory`).

---

## 3. Data Model & Field Normalization

### RFID Data Elements (ISO 28560 Alignment)

The service maps library RFID tag data elements to standardized fields:

| Field Name | Type | DB Column | Constraints | Description |
|---|---|---|---|---|
| `uid` | `string` | `uid` | Max 16 chars, NOT NULL | Unique identifier of the RFID/NFC tag chip (e.g., 64-bit ISO 15693 UID in hex). |
| `itemid` / `itemId` | `string` | `itemid` | Max 16 chars, NOT NULL | Primary item barcode or accession identifier assigned by the Integrated Library System (ILS/LMS). |
| `country` | `string` | `country` | Max 2 chars | ISO 3166-1 alpha-2 country code (e.g., `CH`, `DE`, `FR`). |
| `isil` | `string` | `isil` | Max 11 chars | International Standard Identifier for Libraries and Related Organizations (e.g., `CH-000008-7`). |
| `version` | `int` | `version` | Integer | RFID data model version (e.g., `1` for ISO 28560 standard). |
| `usagetype` / `usageType` | `int` | `usagetype` | Integer | Media category or usage policy (e.g., circulating book, reference only, restricted). |
| `parts` | `int` | `parts` | Integer | Total number of parts/volumes in a multi-part item set. |
| `partno` / `partNo` | `int` | `partno` | Integer | Ordinal sequence number of the specific part scanned. |
| `timestamp` / `ts` | `int64` | `inventorytime` | DATETIME | Timestamp of the scan event. |
| `sessionname` / `session` / `text` | `string` | `sessionname` | Max 32 chars | Name or identifier of the audit session, shelf location, or operator workstation. |
| `marker` | `string` | `marker` | Max 255 chars, NULL | Optional shelf marker, RFID wand bookmark, or shelf location flag. |
| `raw` | `string` / `[]byte` | `raw` | BLOB / VARBINARY | Raw byte stream or hex-encoded block data read directly from the RFID tag chip. |
| `afi` | `string` | - | Form/Query only | Application Family Identifier byte (ISO 15693 standard, e.g., security flag). |
| `isCrcValid` | `*bool` | - | Form/Query only | Verification flag indicating whether tag CRC / checksum validation passed on the scanner. |

### Normalization Logic

When an incoming request is received, `processInventoryRequest` performs the following steps:

1. **Mandatory Field Validation**:
   - `uid`: Stripped of leading/trailing whitespace. If empty, an error `missing uid` (HTTP 400) is returned.
   - `itemid`: Evaluates `req.ItemID`, falling back to `req.ItemIDAlt`. Stripped of whitespace. If empty, `missing itemid` (HTTP 400) is returned.
2. **Length Clamping**:
   - `uid` is truncated to 16 characters.
   - `itemid` is truncated to 16 characters.
   - `country` is truncated to 2 characters.
   - `isil` is truncated to 11 characters.
   - `sessionname` is truncated to 32 characters.
   - `marker` is truncated to 255 characters.
3. **Alias Resolution**:
   - `UsageType`: checks `usageType`, then falls back to `usagetype`.
   - `PartNo`: checks `partNo`, then falls back to `partno`.
   - `SessionName`: checks `sessionname`, then falls back to `session`, then `text`.
4. **Timestamp Parsing**:
   - If timestamp > `100,000,000,000` (e.g., `1696348800000`), treated as Unix epoch in milliseconds (`time.UnixMilli`).
   - If timestamp > `0`, treated as Unix epoch in seconds (`time.Unix(ts, 0)`).
   - If missing or zero, defaults to the current server time (`time.Now()`).
5. **Raw Payload Decoding**:
   - If `raw` is a valid hex-encoded string (e.g. `48656c6c6f`), it is decoded into raw binary bytes.
   - If `raw` is not valid hex, the ASCII string bytes are preserved.

---

## 4. Authentication & Security

### HMAC JWT Verification

When the `jwtkey` configuration setting is present:
- Incoming requests must supply a valid JWT signed with HMAC-SHA256 (`HS256`), HMAC-SHA384 (`HS384`), or HMAC-SHA512 (`HS512`).
- Other algorithms (such as RSA or `none`) are explicitly rejected.

### Token Resolution Order

The service extracts the JWT from three locations in order of preference:
1. `Authorization` header with format `Bearer <token>` (or raw `<token>`).
2. URL Query parameter `?jwt=<token>`.
3. Request body / form field `jwt: "<token>"`.

If all token sources are empty, the request immediately terminates with HTTP 401:
```json
{
  "status": "error",
  "message": "unauthorized: missing authentication token"
}
```

If the signature or expiration verification fails:
```json
{
  "status": "error",
  "message": "unauthorized: token is invalid"
}
```

---

## 5. Database Interaction

### SQL Execution

Database insertions are executed using parameterized queries through `sql.DB.ExecContext`:

```sql
INSERT INTO `inventory` (
  `uid`, 
  `version`, 
  `usagetype`, 
  `parts`, 
  `partno`, 
  `itemid`, 
  `country`, 
  `isil`, 
  `inventorytime`, 
  `marker`, 
  `sessionname`, 
  `raw`
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
```

- **Marker NULL Handling**: If no marker is supplied, it is written as SQL `NULL` (`sql.NullString{Valid: false}`).
- **Auto-Increment ID**: The newly generated auto-increment primary key (`inventoryid`) is returned in the JSON response payload.

### Connection Pool Configuration

The service configures standard database connection pooling defaults in `main.go`:
- `SetConnMaxLifetime(3 * time.Minute)`: Discards stale connections before TCP timeouts occur.
- `SetMaxOpenConns(10)`: Limits simultaneous database connections.
- `SetMaxIdleConns(10)`: Retains pooled connections for reuse.

---

## 6. Endpoints Reference

### Summary of Routes

| HTTP Method | Route | Description |
|---|---|---|
| `GET` | `/ping` | Service health check |
| `GET` | `/inventory` | Ingestion endpoint via query parameters |
| `POST` | `/inventory` | Ingestion endpoint via JSON body or form |
| `GET` | `/api/inventory` | Alias for `/inventory` |
| `POST` | `/api/inventory` | Alias for `/inventory` |
| `GET` | `/swagger/*any` | Swagger UI static assets and HTML viewer |

### Response Schema

#### Success Response (`200 OK`)
```json
{
  "status": "ok",
  "message": "inventory record created",
  "inventoryid": 42,
  "uid": "E00401501234ABCD",
  "itemid": "30111234"
}
```

#### Client Error Response (`400 Bad Request`)
```json
{
  "status": "error",
  "message": "missing itemid"
}
```

#### Authentication Error Response (`401 Unauthorized`)
```json
{
  "status": "error",
  "message": "unauthorized: missing authentication token"
}
```

#### Server Error Response (`500 Internal Server Error`)
```json
{
  "status": "error",
  "message": "database error: Error 1146 (42S02): Table 'inventory_db.inventory' doesn't exist"
}
```

---

## 7. Graceful Shutdown & Lifecycle

The service listens for OS termination signals (`SIGINT`, `SIGTERM`):
1. On receiving an interrupt signal, `main()` calls `ctrl.GracefulStop()`.
2. `http.Server.Shutdown(ctx)` stops accepting new TCP connections and waits for active HTTP requests to complete.
3. The background listener goroutine exits and signals the `sync.WaitGroup`.
4. The database connection pool `db.Close()` executes cleanly, closing all pooled MySQL connections before the process terminates.
