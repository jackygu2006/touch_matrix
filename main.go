package main

import (
	"context"
	"database/sql"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"strings"
	"time"
	"github.com/golang-jwt/jwt/v5"
	"github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)



var bgCtx = context.Background()
var db *sql.DB

// REST API Handlers
// ============================================================


func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	u, _ := getUserByEmail(req.Email)
	if u == nil || u.Status == "disabled" {
		writeJSON(w, 401, map[string]string{"error": "user not found or disabled"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)) != nil {
		writeJSON(w, 401, map[string]string{"error": "wrong password"})
		return
	}
	token, _ := generateJWT(u)
	http.SetCookie(w, &http.Cookie{
		Name: "nftouch_token", Value: token, Path: "/",
		HttpOnly: false, MaxAge: 604800,
	})
	writeJSON(w, 200, map[string]interface{}{
		"token": token, "email": u.Email, "nickname": u.Nickname, "role": u.Role,
	})
}

func handleAuthCheckNew(w http.ResponseWriter, r *http.Request) {
	u := getUserFromRequest(r)
	if u != nil && u.Status == "active" {
		writeJSON(w, 200, map[string]interface{}{
			"status": "ok", "email": u.Email, "nickname": u.Nickname, "role": u.Role, "max_devices": u.MaxDevices,
		})
	} else {
		writeJSON(w, 401, map[string]string{"status": "unauthorized"})
	}
}

// Admin: list users
func handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		users, _ := getUsersAll()
		writeJSON(w, 200, users)
		return
	}
	// POST: create user
	var req struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		Nickname   string `json:"nickname"`
		MaxDevices int    `json:"max_devices"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Email == "" || req.Password == "" {
		writeJSON(w, 400, map[string]string{"error": "email and password required"})
		return
	}
	if req.MaxDevices <= 0 { req.MaxDevices = 5 }
	if existing, _ := getUserByEmail(req.Email); existing != nil {
		writeJSON(w, 400, map[string]string{"error": "email already exists"})
		return
	}
	id, err := createUserDB(req.Email, req.Password, req.Nickname, "user", "", req.MaxDevices)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
	writeJSON(w, 200, map[string]string{"id": id, "status": "created"})
}

func handleAdminUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u, _ := getUserByID(id)
	if u == nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	switch r.Method {
	case "PUT":
		var req struct {
			Password   string `json:"password"`
			Nickname   string `json:"nickname"`
			MaxDevices int    `json:"max_devices"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Password != "" {
			hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
			db.Exec("UPDATE users SET password=? WHERE id=?", string(hash), id)
		}
		if req.Nickname != "" {
			db.Exec("UPDATE users SET nickname=? WHERE id=?", req.Nickname, id)
		}
		if req.MaxDevices > 0 {
			db.Exec("UPDATE users SET max_devices=? WHERE id=?", req.MaxDevices, id)
		}
		writeJSON(w, 200, map[string]string{"status": "updated"})
	case "DELETE":
		// Also delete user's devices
		db.Exec("DELETE FROM devices WHERE user_id=?", id)
		db.Exec("DELETE FROM users WHERE id=? AND role!='admin'", id)
		writeJSON(w, 200, map[string]string{"status": "deleted"})
	default:
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
	}
}

func handleAdminUserToggle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u, _ := getUserByID(id)
	if u == nil || u.Role == "admin" {
		writeJSON(w, 400, map[string]string{"error": "cannot toggle admin"})
		return
	}
	newStatus := "active"
	if u.Status == "active" { newStatus = "disabled" }
	db.Exec("UPDATE users SET status=? WHERE id=?", newStatus, id)
	writeJSON(w, 200, map[string]string{"status": newStatus})
}

func handleProfile(w http.ResponseWriter, r *http.Request) {
	u := getUserFromRequest(r)
	if u == nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	writeJSON(w, 200, u)
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

	// Quota check
	u := getUserFromRequest(r)
	if u != nil && u.Role != "admin" {
		current := countDevicesForUser(u.ID)
		if current >= u.MaxDevices {
			writeJSON(w, 400, map[string]string{"error": fmt.Sprintf("设备配额已满（%d/%d）", current, u.MaxDevices)})
			return
		}
	}

	token := generateToken()
	tokenHash := hashToken(token)

	userID := ""
	if u != nil { userID = u.ID }

	// 删除旧设备记录（如果有），确保重新配对能写入新 Token
	db.Exec("DELETE FROM devices WHERE id=?", deviceID)

	_, err = db.Exec(`INSERT INTO device_codes (code, device_id, user_id, token_hash, expires_at) VALUES (?, ?, ?, ?, datetime('now', '+10 minutes'))`, req.Code, deviceID, userID, tokenHash)
	if err != nil {
		log.Printf("[bind] update device_codes error: %v", err)
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"device_id": deviceID,
		"token":     token,
		"status":    "waiting_for_device",
	})
}

func handleGetDevices(w http.ResponseWriter, r *http.Request) {
	u := getUserFromRequest(r)
	devices, err := getDevices()
	if err != nil {
		log.Printf("[api] getDevices error: %v", err)
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
	// Filter: everyone sees only their own devices
	if u != nil {
		var filtered []Device
		for _, d := range devices {
			if d.UserID == u.ID || d.UserID == "" {
				filtered = append(filtered, d)
			}
		}
		devices = filtered
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

	code, err := createPairingCode(req.DeviceID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}

	writeJSON(w, 200, map[string]string{
		"code":    code,
		"expires": "10 minutes",
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handleQRCode(w http.ResponseWriter, r *http.Request) {
	scheme := "ws"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "wss"
	}
	wsURL := scheme + "://" + r.Host + "/ws/device"
	png, err := qrcode.Encode(wsURL, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "qr error", 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
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
// ============================================================
// JWT Auth
// ============================================================
var jwtSecret []byte

type JWTClaims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func generateJWT(user *User) (string, error) {
	claims := JWTClaims{
		UserID: user.ID, Email: user.Email, Role: user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

func validateJWT(tokenStr string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &JWTClaims{}, func(t *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, err
	}
	claims, ok := token.Claims.(*JWTClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims")
	}
	return claims, nil
}

func jwtFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	cookie, _ := r.Cookie("nftouch_token")
	if cookie != nil {
		return cookie.Value
	}
	return ""
}

func getUserFromRequest(r *http.Request) *User {
	token := jwtFromRequest(r)
	if token == "" {
		return nil
	}
	claims, err := validateJWT(token)
	if err != nil {
		return nil
	}
	u, _ := getUserByID(claims.UserID)
	return u
}

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
		if getUserFromRequest(r) == nil && !checkSession(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := getUserFromRequest(r)
		if u == nil || u.Role != "admin" {
			writeJSON(w, 403, map[string]string{"error": "admin only"})
			return
		}
		next(w, r)
	}
}

func jwtAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := getUserFromRequest(r)
		if u == nil || u.Status != "active" {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
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
		// CSS/JS 不需要鉴权
		if strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".js") {
			fs.ServeHTTP(w, r)
			return
		}
		// JWT token (new auth)
		if getUserFromRequest(r) != nil {
			fs.ServeHTTP(w, r)
			return
		}
		// Old session cookie (compat)
		if checkSession(r) {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, staticDir+"/login.html")
	}
}

func main() {
	jwtSecret = []byte(envOrDefault("JWT_SECRET", "change-me"))
	adminPassword = envOrDefault("ADMIN_PASSWORD", "nf123456")
	sessionSecret = envOrDefault("SESSION_SECRET", "change-me-please")
	listenAddr = envOrDefault("LISTEN_ADDR", ":8443")
	tlsCert = os.Getenv("TLS_CERT")
	tlsKey = os.Getenv("TLS_KEY")
	dbPath = envOrDefault("DB_PATH", "./nftouch.db")
	staticDir = envOrDefault("STATIC_DIR", "./static")

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("=== NFTouch Server MVP ===")
	log.Printf("Admin password configured")
	log.Printf("Listen: %s", listenAddr)

	if err := initDB(dbPath); err != nil {
		log.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	// Initialize admin user
	adminEmail := envOrDefault("ADMIN_EMAIL", "admin@nftouch.local")
	if !strings.Contains(adminEmail, "@") { log.Fatalf("Invalid ADMIN_EMAIL: %s", adminEmail) }
	adminPass := envOrDefault("ADMIN_PASSWORD", "nf123456")
	if existing, _ := getUserByEmail(adminEmail); existing == nil {
		createUserDB(adminEmail, adminPass, "Admin", "admin", "", 999)
		log.Printf("Admin user created: %s", adminEmail)
	}

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
	mux.HandleFunc("POST /api/auth/login", corsMiddleware(handleAuthLogin))
	mux.HandleFunc("GET /api/auth/check", corsMiddleware(handleAuthCheckNew))
	mux.HandleFunc("GET /api/profile", corsMiddleware(jwtAuth(handleProfile)))
	// Admin routes
	mux.HandleFunc("GET /api/admin/users", corsMiddleware(jwtAuth(adminOnly(handleAdminUsers))))
	mux.HandleFunc("POST /api/admin/users", corsMiddleware(jwtAuth(adminOnly(handleAdminUsers))))
	mux.HandleFunc("PUT /api/admin/users/{id}", corsMiddleware(jwtAuth(adminOnly(handleAdminUser))))
	mux.HandleFunc("DELETE /api/admin/users/{id}", corsMiddleware(jwtAuth(adminOnly(handleAdminUser))))
	mux.HandleFunc("PUT /api/admin/users/{id}/toggle", corsMiddleware(jwtAuth(adminOnly(handleAdminUserToggle))))
	mux.HandleFunc("POST /api/bind", api(handleBind))
	mux.HandleFunc("GET /api/devices", api(handleGetDevices))
	mux.HandleFunc("GET /api/devices/{id}", api(handleGetDevice))
	mux.HandleFunc("DELETE /api/devices/{id}", api(handleDeleteDevice))
	mux.HandleFunc("POST /api/devices/{id}/task", api(handlePostTask))
	mux.HandleFunc("POST /api/pairing-code", handlePairingCode)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /api/qrcode", handleQRCode)

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
