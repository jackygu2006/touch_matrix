# NFTouch Cloud Control Platform

## Directory Structure

```
├── server/                  # Go server (current directory)
│   ├── main.go              # HTTP routes + middleware
│   ├── db.go                # Database operations
│   ├── hub.go               # WebSocket message hub
│   ├── static/              # Frontend static files
│   ├── test.sh              # One-click test script
│   ├── *_test.go            # Unit tests
│   └── README.md
├── nf-touch/                # Android client
└── docker-compose.yml
```

---

## 1. Build

```bash
# macOS local (arm64)
go build -o nftouch-server-macos .

# Linux deployment (x86-64)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o nftouch-server .
```

> `nftouch-server-macos` and `nftouch-server` are in `.gitignore` and will not be committed.

---

## 2. Start / Stop

```bash
# === Local Development ===
# Version auto-increments on each start (format 2026.7.13.1), exposed via GET /api/version
export $(grep -v '^#' .env | xargs) && go run .

# === Server Deployment ===
# 1) Upload binary and static files
# 2) Start (server-side .env is pre-configured)
set -a && source .env && set +a
LISTEN_ADDR=":8443" DB_PATH="./nftouch.db" \
  nohup ./nftouch-server > nftouch.log 2>&1 &

# With TLS certificate (optional)
set -a && source .env && set +a
TLS_CERT="/path/to/cert.pem" TLS_KEY="/path/to/key.pem" \
  LISTEN_ADDR=":8443" DB_PATH="./nftouch.db" \
  nohup ./nftouch-server > nftouch.log 2>&1 &

# Verify
curl -s http://localhost:8443/health
# → {"status":"ok"}

curl -s http://localhost:8443/api/version
# → {"version":"2026.7.13.3"}

# Stop
pkill -f nftouch-server
lsof -i :8443 2>/dev/null || echo "Port released"
```

> **Note**: `source .env` in zsh does not auto-export variables to child processes. Use `set -a; source .env; set +a` or `export $(grep -v '^#' .env | xargs)`.

---

## 3. Test

```bash
# One-click: unit tests + static analysis + build verification
./test.sh

# Go tests only
go test -v ./...
```

> **Rule**: When adding new APIs, you **must** add corresponding test cases in `*_test.go`. Always run `./test.sh` before committing.

### Coverage

| File | Count | APIs Covered |
|-----|-------|-------------|
| `auth_test.go` | 12 | Login, JWT, Middleware |
| `device_test.go` | 10 | Device list/details/delete/task |
| `user_test.go` | 10 | User CRUD, toggle |
| `pairing_test.go` | 9 | Pairing code generate/bind |
| `api_test.go` | 3 | Profile, Health |
| **Total** | **44** | Full REST API coverage |

---

## 4. REST API Reference

### Public Endpoints

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/auth/login` | Login | None |
| `GET` | `/api/auth/check` | Check login status | JWT |
| `POST` | `/api/pairing-code` | Generate pairing code | None |
| `GET` | `/health` | Health check | None |
| `GET` | `/api/version` | Server version | None |

### Authenticated Endpoints (JWT required)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/profile` | Current user info |
| `POST` | `/api/bind` | Bind device |
| `GET` | `/api/devices` | Device list (user-scoped) |
| `GET` | `/api/devices/{id}` | Device details |
| `DELETE` | `/api/devices/{id}` | Delete device |
| `POST` | `/api/devices/{id}/task` | Dispatch AI task |

### Admin Endpoints (JWT + admin role)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/admin/users` | List users |
| `POST` | `/api/admin/users` | Create user |
| `PUT` | `/api/admin/users/{id}` | Update user |
| `DELETE` | `/api/admin/users/{id}` | Delete user |
| `PUT` | `/api/admin/users/{id}/toggle` | Enable/disable user |

### Request Examples

```bash
# Login
curl -X POST http://localhost:8443/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@nftouch.local","password":"[redacted]"}'

# Get device list
curl http://localhost:8443/api/devices \
  -H "Authorization: Bearer [redacted]"

# Get pairing code
curl -X POST http://localhost:8443/api/pairing-code \
  -H "Content-Type: application/json" \
  -d '{"device_id":"my-phone-001"}'

# Bind device
curl -X POST http://localhost:8443/api/bind \
  -H "Authorization: Bearer [redacted]" \
  -H "Content-Type: application/json" \
  -d '{"code":"123456"}'

# Dispatch task
curl -X POST http://localhost:8443/api/devices/my-phone-001/task \
  -H "Authorization: Bearer [redacted]" \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Open WeChat"}'

# Admin: create user
curl -X POST http://localhost:8443/api/admin/users \
  -H "Authorization: Bearer [redacted]" \
  -H "Content-Type: application/json" \
  -d '{"email":"user@test.com","password":"[redacted]","nickname":"Test","max_devices":5}'
```

### WebSocket Endpoints

| Path | Purpose | Auth |
|------|---------|------|
| `GET /ws/device` | Device connection | token |
| `GET /ws/dash` | Dashboard panel | JWT (query `?token=<token>`) |

---

## 5. Default Accounts

| Role | Email | Password | Quota |
|------|-------|----------|-------|
| Admin | `admin@nftouch.local` | `nf123456` | 999 |
| Test user | `test@nextflow.com` | `123456` | 5 |

---

## 6. Device Binding Flow

1. **Phone**: Install NF Touch app, grant accessibility service permission
2. **Phone**: Settings → Configure AI model (DeepSeek API recommended)
3. **Phone**: Cloud Bind → enter server address `ws://<IP>:8443` → enter device ID → **get pairing code**
4. **Dashboard**: Click "+ Bind Device" → enter 6-digit pairing code → copy Token to phone
5. **Phone**: Enter Token → binding complete

---

## 7. Environment Variables

`.env` file:

> - `JWT_SECRET` **must be configured** — do not use the default value, or the server will fatalf on startup.
> - `.env` file **must not be committed** to Git. Always change secrets for production.
> - Use `export $(grep -v '^#' .env | xargs)` to ensure variables are correctly exported.
