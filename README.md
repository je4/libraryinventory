# Library Inventory Service

A lightweight, robust REST web service written in Go for capturing and managing library inventory data from RFID/NFC handheld scanners, mobile readers, and automated barcode inventory tools.

The service receives scan records containing item identifiers (barcodes), RFID tag unique identifiers (UIDs), data model metadata, timestamps, session labels, and raw tag data, validates and normalizes the payload, and persists the records to a MySQL/MariaDB database.

---

## Table of Contents

- [Features](#features)
- [Architecture & Directory Structure](#architecture--directory-structure)
- [Prerequisites](#prerequisites)
- [Installation & Building](#installation--building)
- [Configuration](#configuration)
  - [Configuration File (`libraryinventory.toml`)](#configuration-file-libraryinventorytoml)
  - [Configuration Parameters](#configuration-parameters)
  - [Environment Variable Expansion](#environment-variable-expansion)
- [Database Setup](#database-setup)
  - [Table Schema](#table-schema)
- [API Reference](#api-reference)
  - [Health Check (`GET /ping`)](#health-check-get-ping)
  - [Record Inventory via GET (`GET /inventory`, `GET /api/inventory`)](#record-inventory-via-get-get-inventory-get-apiinventory)
  - [Record Inventory via POST (`POST /inventory`, `POST /api/inventory`)](#record-inventory-via-post-post-inventory-post-apiinventory)
  - [Interactive Swagger UI (`GET /swagger/index.html`)](#interactive-swagger-ui-get-swaggerindexhtml)
- [Authentication](#authentication)
  - [JWT Token Format](#jwt-token-format)
  - [Supplying the Token](#supplying-the-token)
- [Swagger / OpenAPI Documentation](#swagger--openapi-documentation)
- [Testing](#testing)
  - [Unit Tests](#unit-tests)
  - [Integration Tests](#integration-tests)
- [Detailed Documentation](#detailed-documentation)
- [License](#license)

---

## Features

- **Multi-Protocol Ingestion**: Supports both HTTP `GET` (query parameters for lightweight handheld scanners) and HTTP `POST` (JSON payload or form encoded).
- **RFID & Barcode Metadata Support**: Captures tag UID, Barcode / Item ID, ISIL library code, country code, data model version, usage type, multi-volume parts/part numbers, timestamp, session name, custom markers, and raw tag byte payloads.
- **Data Normalization & Resilience**: Handles field name aliases (e.g., `itemId`/`itemid`, `partNo`/`partno`, `ts`/`timestamp`), millisecond and second epoch timestamps, string truncation matching database column limits, and hex/binary raw data decoding.
- **JWT HMAC Authentication**: Optional HMAC authentication supporting `HS256`, `HS384`, and `HS512` algorithms passed via `Authorization: Bearer <token>`, URL parameter `?jwt=<token>`, or inside JSON body.
- **Embedded Default Configuration**: Self-contained defaults with `go:embed` and support for external TOML configuration files.
- **Certloader TLS Support**: Seamless TLS / HTTPS support via `certloader` (supporting dev certificates, local certificates, or ACME / Let's Encrypt).
- **Connection-Pooled MySQL Database**: Robust database access using `database/sql` connection pooling and parameterized queries for SQL injection safety.
- **Interactive Swagger UI**: Built-in Swagger UI documentation hosted directly at `/swagger/index.html`.
- **Graceful Shutdown**: Handles OS termination signals (`SIGINT`, `SIGTERM`) to cleanly finish in-flight requests and close database connections.

---

## Architecture & Directory Structure

```text
libraryinventory/
├── cmd/
│   └── libraryinventory/
│       ├── config.go         # Configuration structs and TOML loader
│       └── main.go           # Application entrypoint, DB init, signal handling
├── config/
│   ├── embed.go              # go:embed bindings for default configuration
│   ├── libraryinventory.toml # Production default configuration template
│   └── libraryinventory_dev.toml # Development configuration template
├── docs/
│   ├── FUNCTIONALITY.md      # Detailed functional and architectural guide
│   ├── LibraryInventory_docs.go        # Swagger doc generator bindings
│   ├── LibraryInventory_swagger.json  # OpenAPI JSON spec
│   └── LibraryInventory_swagger.yaml  # OpenAPI YAML spec
├── pkg/
│   └── rest/
│       ├── docs/             # Embedded Swagger documentation package
│       ├── integration_test.go # Integration tests against MySQL
│       ├── inventory.go      # Handlers, data normalization, DB persistence, JWT auth
│       ├── swagger.md        # Swag CLI generation command reference
│       ├── web.go            # Gin router initialization and HTTP server lifecycle
│       └── web_test.go       # Mock-driver unit tests for REST handlers
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```

---

## Prerequisites

- **Go**: Version 1.27 or higher
- **MySQL / MariaDB**: Version 5.7+ / 8.0+ / 10.3+ (for database persistence)

---

## Installation & Building

Clone the repository and build the binary:

```bash
# Clone the repository
git clone https://github.com/je4/libraryinventory.git
cd libraryinventory

# Download dependencies
go mod download

# Build executable binary
go build -o libraryinventory.exe ./cmd/libraryinventory
```

To run the service using default embedded configuration:

```bash
./libraryinventory.exe
```

To run with a custom configuration file:

```bash
./libraryinventory.exe -config /path/to/libraryinventory.toml
```

---

## Configuration

### Configuration File (`libraryinventory.toml`)

A typical configuration file:

```toml
localaddr = ":8080"
externaladdr = "http://localhost:8080"
loglevel = "INFO"
logfile = ""
mysqldsn = "inventory_user:secret_password@tcp(127.0.0.1:3306)/inventory_db?charset=utf8mb4&parseTime=true"
jwtkey = "your-256-bit-secret-key-change-in-production"

# Optional TLS configuration
# [resttls]
# type = "DEV" # Options: DEV, CERT, LETSENCRYPT
```

### Configuration Parameters

| Parameter | Type | Default | Description |
|---|---|---|---|
| `localaddr` | `string` | `:8080` | Bind address and port for the HTTP server |
| `externaladdr` | `string` | `http://localhost:8080` | External URL used in Swagger UI documentation metadata |
| `loglevel` | `string` | `ERROR` | Zerolog logging level (`DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL`) |
| `logfile` | `string` | `""` | File path for log output (empty string logs to stderr/console) |
| `mysqldsn` | `string` | `%%DB%%` | MySQL Data Source Name (DSN) connection string |
| `jwtkey` | `string` | `%%JWTKEY%%` | HMAC secret key for JWT verification (leave empty to disable auth) |
| `resttls` | `table` | `nil` | Optional TLS certificate loader configuration block |

### Environment Variable Expansion

Configuration string values support `%%ENV_VAR%%` expansion (via `github.com/je4/utils/v2/pkg/config`). For example:

- `mysqldsn = "%%DB%%"` expands the value of the `DB` environment variable.
- `jwtkey = "%%JWTKEY%%"` expands the value of the `JWTKEY` environment variable.

---

## Database Setup

### Table Schema

Create the database and the `inventory` table in MySQL/MariaDB:

```sql
CREATE DATABASE IF NOT EXISTS `inventory_db` 
  CHARACTER SET utf8mb4 
  COLLATE utf8mb4_unicode_ci;

USE `inventory_db`;

CREATE TABLE IF NOT EXISTS `inventory` (
  `inventoryid` BIGINT NOT NULL AUTO_INCREMENT,
  `uid` VARCHAR(16) NOT NULL COMMENT 'NFC/RFID tag UID',
  `version` INT DEFAULT 0 COMMENT 'RFID data model version',
  `usagetype` INT DEFAULT 0 COMMENT 'Item usage category',
  `parts` INT DEFAULT 1 COMMENT 'Total parts in set',
  `partno` INT DEFAULT 1 COMMENT 'Part number in set',
  `itemid` VARCHAR(16) NOT NULL COMMENT 'Item barcode/accession number',
  `country` VARCHAR(2) DEFAULT NULL COMMENT 'ISO 3166-1 alpha-2 country code',
  `isil` VARCHAR(11) DEFAULT NULL COMMENT 'ISIL library identifier',
  `inventorytime` DATETIME NOT NULL COMMENT 'Timestamp when item was scanned',
  `marker` VARCHAR(255) DEFAULT NULL COMMENT 'Shelf/shelfmark or tracking marker',
  `sessionname` VARCHAR(32) DEFAULT NULL COMMENT 'Inventory session or location name',
  `raw` BLOB DEFAULT NULL COMMENT 'Raw tag data payload',
  PRIMARY KEY (`inventoryid`),
  INDEX `idx_itemid` (`itemid`),
  INDEX `idx_uid` (`uid`),
  INDEX `idx_inventorytime` (`inventorytime`),
  INDEX `idx_sessionname` (`sessionname`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

---

## API Reference

### Health Check (`GET /ping`)

Verifies service liveness.

```http
GET /ping HTTP/1.1
Host: localhost:8080
```

**Response (`200 OK`):**
```json
{
  "status": "ok"
}
```

---

### Record Inventory via GET (`GET /inventory`, `GET /api/inventory`)

Inserts a scan record via query parameters.

**Query Parameters:**

| Parameter | Type | Required | Description | Example |
|---|---|---|---|---|
| `uid` | string | **Yes** | NFC/RFID tag UID (max 16 chars) | `E00401501234ABCD` |
| `itemid` / `itemId` | string | **Yes** | Item ID or barcode (max 16 chars) | `30111234` |
| `country` | string | No | Country code (max 2 chars) | `CH` |
| `isil` | string | No | ISIL code (max 11 chars) | `CH-000008-7` |
| `version` | int | No | RFID data model version | `1` |
| `usagetype` / `usageType` | int | No | Media usage type identifier | `1` |
| `parts` | int | No | Total number of parts | `1` |
| `partno` / `partNo` | int | No | Part number | `1` |
| `ts` / `timestamp` | int64 | No | Scan time (epoch in ms or seconds) | `1696348800000` |
| `sessionname` / `session` / `text` | string | No | Session name / location (max 32 chars) | `Shelf_A1` |
| `marker` | string | No | Marker note (max 255 chars) | `Section-4-Top` |
| `raw` | string | No | Raw tag data in hex or ASCII | `010203040506` |
| `jwt` | string | Conditional | JWT token if authentication is enabled | `<token>` |

**Example Request:**
```bash
curl -X GET "http://localhost:8080/inventory?uid=E00401501234ABCD&itemid=30111234&country=CH&isil=CH-000008-7&sessionname=Main_Library&marker=Shelf_12" \
  -H "Authorization: Bearer <your-jwt-token>"
```

**Response (`200 OK`):**
```json
{
  "status": "ok",
  "message": "inventory record created",
  "inventoryid": 1042,
  "uid": "E00401501234ABCD",
  "itemid": "30111234"
}
```

---

### Record Inventory via POST (`POST /inventory`, `POST /api/inventory`)

Inserts a scan record via JSON payload.

**Request Body (`application/json`):**

```json
{
  "uid": "E00401501234ABCD",
  "itemId": "30111234",
  "country": "CH",
  "isil": "CH-000008-7",
  "version": 1,
  "usageType": 1,
  "parts": 2,
  "partNo": 1,
  "timestamp": 1696348800000,
  "sessionname": "Inventory_2026_Q4",
  "marker": "Section-B-Row-3",
  "raw": "48656c6c6f20576f726c64"
}
```

**Example Request:**
```bash
curl -X POST "http://localhost:8080/inventory" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-jwt-token>" \
  -d '{
    "uid": "E00401501234ABCD",
    "itemId": "30111234",
    "country": "CH",
    "isil": "CH-000008-7",
    "version": 1,
    "usageType": 1,
    "parts": 1,
    "partNo": 1,
    "sessionname": "Stack_A"
  }'
```

**Response (`200 OK`):**
```json
{
  "status": "ok",
  "message": "inventory record created",
  "inventoryid": 1043,
  "uid": "E00401501234ABCD",
  "itemid": "30111234"
}
```

---

### Interactive Swagger UI (`GET /swagger/index.html`)

Open your browser at `http://localhost:8080/swagger/index.html` to explore the OpenAPI documentation and test API endpoints interactively.

---

## Authentication

When `jwtkey` is configured (non-empty) in the configuration file or environment, all inventory endpoints enforce JWT authentication.

### JWT Token Format

- **Signing Algorithms**: `HS256`, `HS384`, `HS512` (HMAC with shared secret key).
- **Standard Claims**: May include `iat` (issued at), `exp` (expiration timestamp), `nbf` (not before), etc.

### Supplying the Token

Clients can pass the JWT token in any of the following locations:

1. **HTTP Authorization Header** (recommended):
   ```http
   Authorization: Bearer <jwt-token>
   ```
2. **Query Parameter**:
   ```http
   GET /inventory?uid=...&itemid=...&jwt=<jwt-token>
   ```
3. **JSON Body Field**:
   ```json
   {
     "uid": "E00401501234ABCD",
     "itemId": "30111234",
     "jwt": "<jwt-token>"
   }
   ```

If the token is missing or invalid, the service returns `401 Unauthorized`:
```json
{
  "status": "error",
  "message": "unauthorized: invalid token"
}
```

---

## Swagger / OpenAPI Documentation

OpenAPI specification files are located in `docs/` and `pkg/rest/docs/`.

To regenerate Swagger documentation after modifying route annotations or model definitions:

```bash
go run github.com/swaggo/swag/v2/cmd/swag init \
  --v3.1 \
  --instanceName LibraryInventory \
  --parseDependency \
  --parseInternal \
  -g pkg/rest/web.go \
  -o pkg/rest/docs
```

---

## Testing

### Unit Tests

Unit tests use an in-memory SQL mock driver to test HTTP routing, query and JSON parameter parsing, timestamp conversion, data clamping, and JWT authentication without requiring a running database instance:

```bash
go test -v ./pkg/rest -run TestController_
```

### Integration Tests

Integration tests validate real database schema compatibility, column types, and CRUD operations against a live MySQL instance.

1. Set up test environment variables in a `.env` file or export them in your terminal:
   ```env
   DB="inventory_user:secret@tcp(127.0.0.1:3306)/inventory_test?charset=utf8mb4&parseTime=true"
   JWTKEY="test-secret-key-32-bytes-long!!"
   ```
2. Run the integration test suite:
   ```bash
   go test -v ./pkg/rest -run TestIntegration_Inventory
   ```

---

## Detailed Documentation

For an in-depth explanation of the domain model, RFID data mapping (ISO 28560), normalization pipelines, database design decisions, and production deployment recommendations, refer to [docs/FUNCTIONALITY.md](docs/FUNCTIONALITY.md).

---

## License

This project is licensed under the Apache License 2.0. See the [LICENSE](LICENSE) file for details.
