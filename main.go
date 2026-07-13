package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

var bgCtx = context.Background()
var db *sql.DB

// 版本自增：格式 2026.7.13.1（年.月.日.当日序号），每次启动自动递增
func bumpVersion() string {
	today := time.Now().Format("2006.1.2")
	data, _ := os.ReadFile("VERSION")
	current := strings.TrimSpace(string(data))
	newVer := today + ".1"
	if strings.HasPrefix(current, today+".") {
		parts := strings.Split(current, ".")
		if seq, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			newVer = today + "." + strconv.Itoa(seq+1)
		}
	}
	_ = os.WriteFile("VERSION", []byte(newVer+"\n"), 0644)
	return newVer
}

func getVersion() string {
	data, _ := os.ReadFile("VERSION")
	return strings.TrimSpace(string(data))
}

// REST API Handlers
// ============================================================

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[api] json encode error: %v", err)
	}
}

func handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	u, err := getUserByEmail(req.Email)
	if err != nil {
		log.Printf("[auth] getUserByEmail error: %v", err)
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
	if u == nil || u.Status == "disabled" {
		writeJSON(w, 401, map[string]string{"error": "user not found or disabled"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)) != nil {
		writeJSON(w, 401, map[string]string{"error": "wrong password"})
		return
	}
	token, err := generateJWT(u)
	if err != nil {
		log.Printf("[auth] generateJWT error: %v", err)
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "nftouch_token", Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 604800,
		Secure: r.TLS != nil,
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
		users, err := getUsersAll()
		if err != nil {
			log.Printf("[admin] getUsersAll error: %v", err)
			writeJSON(w, 500, map[string]string{"error": "server error"})
			return
		}
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if req.Email == "" || req.Password == "" {
		writeJSON(w, 400, map[string]string{"error": "email and password required"})
		return
	}
	if len(req.Password) < 6 {
		writeJSON(w, 400, map[string]string{"error": "password must be at least 6 characters"})
		return
	}
	if req.MaxDevices <= 0 {
		req.MaxDevices = 5
	}
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid request"})
			return
		}
		if req.Password != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
			if err != nil {
				log.Printf("[admin] bcrypt error: %v", err)
				writeJSON(w, 500, map[string]string{"error": "server error"})
				return
			}
			if _, err := db.Exec("UPDATE users SET password=? WHERE id=?", string(hash), id); err != nil {
				log.Printf("[admin] update password error: %v", err)
				writeJSON(w, 500, map[string]string{"error": "server error"})
				return
			}
		}
		if req.Nickname != "" {
			if _, err := db.Exec("UPDATE users SET nickname=? WHERE id=?", req.Nickname, id); err != nil {
				log.Printf("[admin] update nickname error: %v", err)
				writeJSON(w, 500, map[string]string{"error": "server error"})
				return
			}
		}
		if req.MaxDevices > 0 {
			if _, err := db.Exec("UPDATE users SET max_devices=? WHERE id=?", req.MaxDevices, id); err != nil {
				log.Printf("[admin] update max_devices error: %v", err)
				writeJSON(w, 500, map[string]string{"error": "server error"})
				return
			}
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
	if u.Status == "active" {
		newStatus = "disabled"
	}
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
		current := countDevicesForUser(u.ID) + countPendingBindsForUser(u.ID)
		if current >= u.MaxDevices {
			writeJSON(w, 400, map[string]string{"error": fmt.Sprintf("设备配额已满（%d/%d）", current, u.MaxDevices)})
			return
		}
	}

	token := generateToken()
	tokenHash := hashToken(token)

	userID := ""
	if u != nil {
		userID = u.ID
	}

	// 清理旧设备记录（保留在线设备，避免断连）
	db.Exec("DELETE FROM devices WHERE id=? AND status != 'online'", deviceID)

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
	var devices []Device
	var err error
	if u != nil {
		devices, err = getDevicesByUserID(u.ID)
	} else {
		devices, err = getDevices()
	}
	if err != nil {
		log.Printf("[api] getDevices error: %v", err)
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

	u := getUserFromRequest(r)
	userID := ""
	if u != nil {
		userID = u.ID
	}
	taskID, err := insertTaskRecord(deviceID, userID, req.Prompt)
	if err != nil {
		log.Printf("[api] insertTaskRecord error: %v", err)
		writeJSON(w, 500, map[string]string{"error": "failed to create task record"})
		return
	}

	err = hub.sendToDevice(deviceID, WSMessage{
		Type:   "cmd_task",
		TaskID: taskID,
		Prompt: req.Prompt,
	})
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "device not connected"})
		return
	}

	writeJSON(w, 200, map[string]interface{}{"status": "sent", "task_id": taskID})
}

func handleGetTasks(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 { limit = 20 }
	tasks, err := getTaskHistory(deviceID, limit, offset)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "failed to get tasks"})
		return
	}
	writeJSON(w, 200, tasks)
}

func handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	taskID, err := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid task id"})
		return
	}
	if err := deleteTaskRecord(deviceID, taskID); err != nil {
		writeJSON(w, 500, map[string]string{"error": "failed to delete task"})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tx, err := db.Begin()
	if err != nil {
		log.Printf("[device] tx begin error: %v", err)
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}

	if _, err := tx.Exec("DELETE FROM devices WHERE id=?", id); err != nil {
		tx.Rollback()
		log.Printf("[device] delete device error: %v", err)
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}

	if _, err := tx.Exec("DELETE FROM device_codes WHERE device_id=?", id); err != nil {
		tx.Rollback()
		log.Printf("[device] delete device_codes error: %v", err)
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[device] tx commit error: %v", err)
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
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
	if err := db.Ping(); err != nil {
		writeJSON(w, 503, map[string]string{"status": "unhealthy", "error": err.Error()})
		return
	}
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

// ============================================================
// Middleware
// ============================================================

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

// ============================================================
// Main
// ============================================================

var (
	staticDir  string
	listenAddr string
	tlsCert    string
	tlsKey     string
	dbPath     string
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
		// JWT from cookie (page loads)
		if cookie, err := r.Cookie("nftouch_token"); err == nil {
			if claims, err := validateJWT(cookie.Value); err == nil {
				if u, _ := getUserByID(claims.UserID); u != nil && u.Status == "active" {
					fs.ServeHTTP(w, r)
					return
				}
			}
		}
		http.ServeFile(w, r, staticDir+"/login.html")
	}
}

func main() {
	jwtSecret = []byte(envOrDefault("JWT_SECRET", "change-me"))
	if string(jwtSecret) == "change-me" {
		log.Fatalf("FATAL: JWT_SECRET not configured. Set JWT_SECRET environment variable.")
	}
	listenAddr = envOrDefault("LISTEN_ADDR", ":8443")
	tlsCert = os.Getenv("TLS_CERT")
	tlsKey = os.Getenv("TLS_KEY")
	dbPath = envOrDefault("DB_PATH", "./nftouch.db")
	staticDir = envOrDefault("STATIC_DIR", "./static")

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	version := bumpVersion()
	log.Printf("=== NFTouch Server v%s ===", version)
	log.Printf("Listen: %s", listenAddr)

	if err := initDB(dbPath); err != nil {
		log.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	// Initialize admin user
	adminEmail := envOrDefault("ADMIN_EMAIL", "admin@nftouch.local")
	if !strings.Contains(adminEmail, "@") {
		log.Fatalf("Invalid ADMIN_EMAIL: %s", adminEmail)
	}
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
			cleanupPairingRateLimit()
		}
	}()

	mux := http.NewServeMux()

	// Helper to reduce repeated middleware chains
	jwt := jwtAuth
	admin := func(h http.HandlerFunc) http.HandlerFunc { return jwt(adminOnly(h)) }

	mux.HandleFunc("GET /ws/device", handleDeviceWS)
	mux.HandleFunc("GET /ws/dash", handleDashWS)

	mux.HandleFunc("POST /api/auth/login", handleAuthLogin)
	mux.HandleFunc("GET /api/auth/check", handleAuthCheckNew)
	mux.HandleFunc("GET /api/profile", jwt(handleProfile))
	// Admin routes
	mux.HandleFunc("GET /api/admin/users", admin(handleAdminUsers))
	mux.HandleFunc("POST /api/admin/users", admin(handleAdminUsers))
	mux.HandleFunc("PUT /api/admin/users/{id}", admin(handleAdminUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", admin(handleAdminUser))
	mux.HandleFunc("PUT /api/admin/users/{id}/toggle", admin(handleAdminUserToggle))
	// Device routes (JWT only)
	mux.HandleFunc("POST /api/bind", jwt(handleBind))
	mux.HandleFunc("GET /api/devices", jwt(handleGetDevices))
	mux.HandleFunc("GET /api/devices/{id}", jwt(handleGetDevice))
	mux.HandleFunc("GET /api/devices/{id}/tasks", jwt(handleGetTasks))
	mux.HandleFunc("DELETE /api/devices/{id}", jwt(handleDeleteDevice))
	mux.HandleFunc("POST /api/devices/{id}/task", jwt(handlePostTask))
	mux.HandleFunc("DELETE /api/devices/{id}/tasks/{tid}", jwt(handleDeleteTask))
	mux.HandleFunc("POST /api/pairing-code", handlePairingCode)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":"` + getVersion() + `"}`))
	})

	staticDir := envOrDefault("STATIC_DIR", "./static")
	fs := http.FileServer(http.Dir(staticDir))
	mux.Handle("GET /", authStatic(fs))

	log.Printf("Routes registered")
	log.Printf("Static files: %s", staticDir)

	hub.Start()

	server := &http.Server{
		Addr:         listenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Printf("Shutting down gracefully...")
		server.Shutdown(context.Background())
	}()

	if tlsCert != "" && tlsKey != "" {
		log.Printf("Starting HTTPS server on %s", listenAddr)
		if err := server.ListenAndServeTLS(tlsCert, tlsKey); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	} else {
		log.Printf("Starting HTTP server on %s (no TLS)", listenAddr)
		log.Printf("WARNING: Running without TLS. JWT tokens and all traffic are in plaintext.")
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
