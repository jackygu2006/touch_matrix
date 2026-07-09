package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"nhooyr.io/websocket"
	_ "modernc.org/sqlite"
)

var bgCtx = context.Background()

// ============================================================
// Database Layer
// ============================================================

var db *sql.DB

type Device struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	TokenHash  string  `json:"-"`
	Status     string  `json:"status"`
	Brand      string  `json:"brand"`
	Model      string  `json:"model"`
	Resolution string  `json:"resolution"`
	Battery    int     `json:"battery"`
	LastSeen   *string `json:"last_seen"`
	CreatedAt  string  `json:"created_at"`
}

func initDB(dbPath string) error {
	var err error
	db, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS devices (
			id          TEXT PRIMARY KEY,
			name        TEXT DEFAULT '',
			token_hash  TEXT,
			status      TEXT DEFAULT 'offline',
			brand       TEXT DEFAULT '',
			model       TEXT DEFAULT '',
			resolution  TEXT DEFAULT '',
			battery     INTEGER DEFAULT 0,
			last_seen   TEXT DEFAULT '',
			created_at  TEXT DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS device_codes (
			code        TEXT PRIMARY KEY,
			device_id   TEXT NOT NULL,
			expires_at  TEXT NOT NULL
		);
	`)
	return err
}

func upsertDevice(d Device) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO devices (id, name, token_hash, status, brand, model, resolution, battery, last_seen)
		VALUES (?, ?, ?, 'online', ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			token_hash=excluded.token_hash,
			status='online',
			brand=excluded.brand,
			model=excluded.model,
			resolution=excluded.resolution,
			battery=excluded.battery,
			last_seen=excluded.last_seen
	`, d.ID, d.Name, d.TokenHash, d.Brand, d.Model, d.Resolution, d.Battery, now)
	return err
}

func setDeviceOnline(deviceID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`UPDATE devices SET status='online', last_seen=? WHERE id=?`, now, deviceID)
	return err
}

func setDeviceOffline(deviceID string) error {
	_, err := db.Exec(`UPDATE devices SET status='offline' WHERE id=?`, deviceID)
	return err
}

func getDevices() ([]Device, error) {
	rows, err := db.Query(`SELECT id, name, status, brand, model, resolution, battery, last_seen, created_at FROM devices ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var d Device
		var lastSeen sql.NullString
		if err := rows.Scan(&d.ID, &d.Name, &d.Status, &d.Brand, &d.Model, &d.Resolution, &d.Battery, &lastSeen, &d.CreatedAt); err != nil {
			return nil, err
		}
		if lastSeen.Valid {
			d.LastSeen = &lastSeen.String
		}
		devices = append(devices, d)
	}
	if devices == nil {
		devices = []Device{}
	}
	return devices, nil
}

func getDevice(id string) (*Device, error) {
	var d Device
	var lastSeen sql.NullString
	err := db.QueryRow(`SELECT id, name, status, brand, model, resolution, battery, last_seen, created_at FROM devices WHERE id=?`, id).
		Scan(&d.ID, &d.Name, &d.Status, &d.Brand, &d.Model, &d.Resolution, &d.Battery, &lastSeen, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		d.LastSeen = &lastSeen.String
	}
	return &d, nil
}

func getDeviceByTokenHash(hash string) (*Device, error) {
	var d Device
	var lastSeen sql.NullString
	err := db.QueryRow(`SELECT id, name, status, brand, model, resolution, battery, last_seen, created_at FROM devices WHERE token_hash=?`, hash).
		Scan(&d.ID, &d.Name, &d.Status, &d.Brand, &d.Model, &d.Resolution, &d.Battery, &lastSeen, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		d.LastSeen = &lastSeen.String
	}
	return &d, nil
}

func createPairingCode(deviceID string) (string, error) {
	db.Exec(`DELETE FROM device_codes WHERE device_id=? AND expires_at < datetime('now')`, deviceID)

	code := generateCode()
	expiresAt := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO device_codes (code, device_id, expires_at) VALUES (?, ?, ?)`, code, deviceID, expiresAt)
	return code, err
}

func validatePairingCode(code string) (string, error) {
	var deviceID string
	err := db.QueryRow(`
		DELETE FROM device_codes WHERE code=? AND expires_at >= datetime('now')
		RETURNING device_id
	`, code).Scan(&deviceID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return deviceID, err
}

func cleanupExpiredCodes() {
	db.Exec(`DELETE FROM device_codes WHERE expires_at < datetime('now', '-1 hour')`)
}

func generateCode() string {
	code := rand.Intn(900000) + 100000
	return fmt.Sprintf("%d", code)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// ============================================================
// WebSocket Hub
// ============================================================

type WSMessage struct {
	Type     string          `json:"type"`
	DeviceID string          `json:"device_id,omitempty"`
	X        int             `json:"x,omitempty"`
	Y        int             `json:"y,omitempty"`
	X1       int             `json:"x1,omitempty"`
	Y1       int             `json:"y1,omitempty"`
	X2       int             `json:"x2,omitempty"`
	Y2       int             `json:"y2,omitempty"`
	Duration int             `json:"duration,omitempty"`
	Text     string          `json:"text,omitempty"`
	Prompt   string          `json:"prompt,omitempty"`
	Key      string          `json:"key,omitempty"`
	Token    string          `json:"token,omitempty"`
	Info     json.RawMessage `json:"info,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Devices  []Device        `json:"devices,omitempty"`
	Device   *Device         `json:"device,omitempty"`
}

type deviceConn struct {
	conn     *websocket.Conn
	deviceID string
	mu       sync.Mutex
}

type dashConn struct {
	conn     *websocket.Conn
	deviceID string
	watchAll bool
	mu       sync.Mutex
}

type Hub struct {
	mu      sync.RWMutex
	devices map[string]*deviceConn
	dash    map[*dashConn]bool
}

var hub = &Hub{
	devices: make(map[string]*deviceConn),
	dash:    make(map[*dashConn]bool),
}

func (h *Hub) registerDevice(deviceID string, dc *deviceConn) {
	h.mu.Lock()
	if old, ok := h.devices[deviceID]; ok {
		old.conn.Close(websocket.StatusNormalClosure, "replaced")
	}
	h.devices[deviceID] = dc
	h.mu.Unlock()

	setDeviceOnline(deviceID)
	h.broadcastDeviceList()
	log.Printf("[hub] device %s connected", deviceID)
}

func (h *Hub) unregisterDevice(deviceID string) {
	h.mu.Lock()
	delete(h.devices, deviceID)
	h.mu.Unlock()

	setDeviceOffline(deviceID)
	h.broadcastDeviceList()
	log.Printf("[hub] device %s disconnected", deviceID)
}

func (h *Hub) registerDash(dc *dashConn) {
	h.mu.Lock()
	h.dash[dc] = true
	h.mu.Unlock()
	log.Printf("[hub] dashboard connected (total: %d)", len(h.dash))
}

func (h *Hub) unregisterDash(dc *dashConn) {
	h.mu.Lock()
	delete(h.dash, dc)
	h.mu.Unlock()
	log.Printf("[hub] dashboard disconnected (total: %d)", len(h.dash))
}

func (h *Hub) sendToDevice(deviceID string, msg WSMessage) error {
	h.mu.RLock()
	dc, ok := h.devices[deviceID]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("device %s not connected", deviceID)
	}
	dc.mu.Lock()
	defer dc.mu.Unlock()
	data, _ := json.Marshal(msg)
	return dc.conn.Write(bgCtx, websocket.MessageText, data)
}

func (h *Hub) broadcastFrame(deviceID string, frameData []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	header, _ := json.Marshal(WSMessage{Type: "frame", DeviceID: deviceID})
	for dc := range h.dash {
		if dc.deviceID == deviceID || dc.watchAll {
			dc.mu.Lock()
			dc.conn.Write(bgCtx, websocket.MessageText, header)
			dc.conn.Write(bgCtx, websocket.MessageBinary, frameData)
			dc.mu.Unlock()
		}
	}
}

func (h *Hub) broadcastToDash(msg WSMessage) {
	data, _ := json.Marshal(msg)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for dc := range h.dash {
		dc.mu.Lock()
		dc.conn.Write(bgCtx, websocket.MessageText, data)
		dc.mu.Unlock()
	}
}

func (h *Hub) broadcastDeviceList() {
	devices, err := getDevices()
	if err != nil {
		log.Printf("[hub] error getting device list: %v", err)
		return
	}

	msg, _ := json.Marshal(WSMessage{Type: "device_list", Devices: devices})
	h.mu.RLock()
	defer h.mu.RUnlock()
	for dc := range h.dash {
		dc.mu.Lock()
		dc.conn.Write(bgCtx, websocket.MessageText, msg)
		dc.mu.Unlock()
	}
}

// ============================================================
// WebSocket Handlers
// ============================================================

func handleDeviceWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[ws/device] accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "")
	c.SetReadLimit(2 << 20) // 2MB for screen frames

	dc := &deviceConn{conn: c}
	var deviceID string

	_, msgBytes, err := c.Read(bgCtx)
	if err != nil {
		log.Printf("[ws/device] read auth error: %v", err)
		return
	}

	var authMsg WSMessage
	if err := json.Unmarshal(msgBytes, &authMsg); err != nil || authMsg.Type != "auth" {
		c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "auth_fail", Reason: "invalid auth message"}))
		return
	}

	tokenHash := hashToken(authMsg.Token)
	dev, err := getDeviceByTokenHash(tokenHash)
	if err != nil || dev == nil {
		c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "auth_fail", Reason: "invalid token"}))
		log.Printf("[ws/device] auth failed: token not found")
		return
	}

	deviceID = dev.ID
	dc.deviceID = deviceID

	if authMsg.Info != nil {
		var info struct {
			Brand      string `json:"brand"`
			Model      string `json:"model"`
			Resolution string `json:"resolution"`
			Battery    int    `json:"battery"`
		}
		json.Unmarshal(authMsg.Info, &info)
		dev.Brand = info.Brand
		dev.Model = info.Model
		dev.Resolution = info.Resolution
		dev.Battery = info.Battery
	}
	dev.TokenHash = tokenHash
	upsertDevice(*dev)

	c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "auth_ok"}))

	hub.registerDevice(deviceID, dc)
	defer hub.unregisterDevice(deviceID)

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			dc.mu.Lock()
			err := c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "ping"}))
			dc.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	for {
		msgType, data, err := c.Read(bgCtx)
		if err != nil {
			log.Printf("[ws/device] %s read error: %v", deviceID, err)
			return
		}

		if msgType == websocket.MessageBinary {
			hub.broadcastFrame(deviceID, data)
		} else {
			var msg WSMessage
			if json.Unmarshal(data, &msg) == nil {
				switch msg.Type {
				case "pong":
				case "task_status":
					hub.broadcastToDash(WSMessage{Type: "task_status", DeviceID: deviceID, Text: msg.Text})
				case "status":
					if msg.Info != nil {
						var info struct {
							Battery int `json:"battery"`
						}
						json.Unmarshal(msg.Info, &info)
						db.Exec(`UPDATE devices SET battery=?, last_seen=? WHERE id=?`,
							info.Battery, time.Now().UTC().Format(time.RFC3339), deviceID)
					}
				}
			}
		}
	}
}

func handleDashWS(w http.ResponseWriter, r *http.Request) {
	if !checkSession(r) && !checkAdminKey(r.URL.Query().Get("key")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[ws/dash] accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "")

	dc := &dashConn{conn: c}
	hub.registerDash(dc)
	defer hub.unregisterDash(dc)

	devices, _ := getDevices()
	c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: devices}))

	for {
		_, data, err := c.Read(bgCtx)
		if err != nil {
			log.Printf("[ws/dash] read error: %v", err)
			return
		}

		var msg WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "watch":
			dc.deviceID = msg.DeviceID
			dc.watchAll = false
			log.Printf("[ws/dash] watching device: %s", msg.DeviceID)
		case "watch_all":
			dc.watchAll = true
			log.Printf("[ws/dash] watching all devices")
		case "watch_one":
			dc.watchAll = false
			dc.deviceID = msg.DeviceID
			log.Printf("[ws/dash] watching one: %s", msg.DeviceID)
		case "cmd_tap", "cmd_swipe", "cmd_input", "cmd_task", "cmd_key":
			if msg.DeviceID == "" {
				msg.DeviceID = dc.deviceID
			}
			if msg.DeviceID != "" {
				if err := hub.sendToDevice(msg.DeviceID, msg); err != nil {
					c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{
						Type: "error", Reason: err.Error(),
					}))
				}
			}
		case "refresh":
			devices, _ := getDevices()
			c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: devices}))
		}
	}
}

// ============================================================
// REST API Handlers
// ============================================================

func checkAdminKey(key string) bool {
	return subtle.ConstantTimeCompare([]byte(key), []byte(adminKey)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func handleBind(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	if len(req.Code) != 6 {
		writeJSON(w, 400, map[string]string{"error": "invalid code format"})
		return
	}

	deviceID, err := validatePairingCode(req.Code)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
	if deviceID == "" {
		writeJSON(w, 400, map[string]string{"error": "invalid or expired code"})
		return
	}

	token := generateToken()
	tokenHash := hashToken(token)

	_, err = db.Exec(`UPDATE devices SET token_hash=? WHERE id=?`, tokenHash, deviceID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}

	dev, _ := getDevice(deviceID)
	writeJSON(w, 200, map[string]interface{}{
		"device_id": deviceID,
		"token":     token,
		"device":    dev,
	})
}

func handleGetDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := getDevices()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
	writeJSON(w, 200, devices)
}

func handleGetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, err := getDevice(id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
	if dev == nil {
		writeJSON(w, 404, map[string]string{"error": "device not found"})
		return
	}
	writeJSON(w, 200, dev)
}

func handlePostTask(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")

	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	if req.Prompt == "" {
		writeJSON(w, 400, map[string]string{"error": "prompt is required"})
		return
	}

	dev, _ := getDevice(deviceID)
	if dev == nil {
		writeJSON(w, 404, map[string]string{"error": "device not found"})
		return
	}

	err := hub.sendToDevice(deviceID, WSMessage{
		Type:   "cmd_task",
		Prompt: req.Prompt,
	})
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "device not connected"})
		return
	}

	writeJSON(w, 200, map[string]string{"status": "sent"})
}

func handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := db.Exec("DELETE FROM devices WHERE id=?", id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
	db.Exec("DELETE FROM device_codes WHERE device_id=?", id)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func handlePairingCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if req.DeviceID == "" {
		writeJSON(w, 400, map[string]string{"error": "device_id is required"})
		return
	}

	dev, _ := getDevice(req.DeviceID)
	exists := dev != nil

	if dev == nil {
		db.Exec(`INSERT OR IGNORE INTO devices (id, name, status, created_at) VALUES (?, ?, 'offline', datetime('now'))`,
			req.DeviceID, req.DeviceID)
	}

	code, err := createPairingCode(req.DeviceID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}

	resp := map[string]string{
		"code":    code,
		"expires": "10 minutes",
	}
	if exists {
		resp["warning"] = "该设备ID已被使用，继续将覆盖旧设备的绑定"
	}
	writeJSON(w, 200, resp)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func mustJSON(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}

// ============================================================
// Session / Auth
// ============================================================
func createSessionToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var sessions = sync.Map{} // token -> expiry time

func setSessionCookie(w http.ResponseWriter, r *http.Request) string {
	token := createSessionToken()
	sessions.Store(token, time.Now().Add(24*time.Hour))
	http.SetCookie(w, &http.Cookie{
		Name:     "nftouch_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	return token
}

func checkSession(r *http.Request) bool {
	cookie, err := r.Cookie("nftouch_session")
	if err != nil {
		return false
	}
	val, ok := sessions.Load(cookie.Value)
	if !ok {
		return false
	}
	expiry, ok := val.(time.Time)
	if !ok || time.Now().After(expiry) {
		sessions.Delete(cookie.Value)
		return false
	}
	return true
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if req.Username != "admin" || req.Password != adminPassword {
		writeJSON(w, 401, map[string]string{"error": "用户名或密码错误"})
		return
	}
	setSessionCookie(w, r)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	if checkSession(r) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	} else {
		writeJSON(w, 401, map[string]string{"status": "unauthorized"})
	}
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("nftouch_session")
	if err == nil {
		sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:   "nftouch_session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// ============================================================
// Middleware
// ============================================================

func adminAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkSession(r) && !checkAdminKey(r.URL.Query().Get("key")) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Admin-Key")
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		next(w, r)
	}
}

// ============================================================
// Main
// ============================================================

var (
	staticDir      string
	adminKey       string
	adminPassword  string
	listenAddr     string
	tlsCert        string
	tlsKey         string
	dbPath         string
	sessionSecret  string
)

func authStatic(fs http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		if checkSession(r) || r.URL.Query().Get("key") == adminKey {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, staticDir+"/login.html")
	}
}

func main() {
	adminKey = envOrDefault("ADMIN_KEY", "admin123")
	adminPassword = envOrDefault("ADMIN_PASSWORD", "nf123456")
	sessionSecret = envOrDefault("SESSION_SECRET", "change-me-please")
	listenAddr = envOrDefault("LISTEN_ADDR", ":8443")
	tlsCert = os.Getenv("TLS_CERT")
	tlsKey = os.Getenv("TLS_KEY")
	dbPath = envOrDefault("DB_PATH", "./nftouch.db")
	staticDir = envOrDefault("STATIC_DIR", "./static")

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("=== NFTouch Server MVP ===")
	log.Printf("Admin key: %s", adminKey)
	log.Printf("Listen: %s", listenAddr)

	if err := initDB(dbPath); err != nil {
		log.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()
	log.Printf("Database: %s", dbPath)

	go func() {
		for {
			time.Sleep(5 * time.Minute)
			cleanupExpiredCodes()
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /ws/device", handleDeviceWS)
	mux.HandleFunc("GET /ws/dash", handleDashWS)

	api := func(h http.HandlerFunc) http.HandlerFunc {
		return adminAuthMiddleware(corsMiddleware(h))
	}

	mux.HandleFunc("POST /api/login", corsMiddleware(handleLogin))
	mux.HandleFunc("POST /api/logout", handleLogout)
	mux.HandleFunc("GET /api/auth-check", handleAuthCheck)
	mux.HandleFunc("POST /api/bind", api(handleBind))
	mux.HandleFunc("GET /api/devices", api(handleGetDevices))
	mux.HandleFunc("GET /api/devices/{id}", api(handleGetDevice))
	mux.HandleFunc("DELETE /api/devices/{id}", api(handleDeleteDevice))
	mux.HandleFunc("POST /api/devices/{id}/task", api(handlePostTask))
	mux.HandleFunc("POST /api/pairing-code", handlePairingCode)
	mux.HandleFunc("GET /health", handleHealth)

	staticDir := envOrDefault("STATIC_DIR", "./static")
	fs := http.FileServer(http.Dir(staticDir))
	mux.Handle("GET /", authStatic(fs))

	log.Printf("Routes registered")
	log.Printf("Static files: %s", staticDir)

	if tlsCert != "" && tlsKey != "" {
		log.Printf("Starting HTTPS server on %s", listenAddr)
		log.Fatal(http.ListenAndServeTLS(listenAddr, tlsCert, tlsKey, mux))
	} else {
		log.Printf("Starting HTTP server on %s (no TLS)", listenAddr)
		log.Fatal(http.ListenAndServe(listenAddr, mux))
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
