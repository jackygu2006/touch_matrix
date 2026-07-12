package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)



// Database Layer
// ============================================================


type Device struct {
	ID         string  `json:"id"`
	LastFrame  time.Time `json:"last_frame"`
	UserID     string  `json:"user_id,omitempty"`
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

type User struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Password   string `json:"-"`
	Nickname   string `json:"nickname"`
	Role       string `json:"role"`
	Status     string `json:"status"`
	MaxDevices int    `json:"max_devices"`
	CreatedAt  string `json:"created_at"`
}

type DeviceCode struct {
	Code      string `json:"code"`
	DeviceID  string `json:"device_id"`
	ExpiresAt string `json:"expires_at"`
}

func initDB(dbPath string) error {
	var err error
	db, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY
		);

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
			token_hash  TEXT,
			expires_at  TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}

	// Users table
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, password TEXT NOT NULL, nickname TEXT DEFAULT '', role TEXT DEFAULT 'user', status TEXT DEFAULT 'active', max_devices INTEGER DEFAULT 5, created_by TEXT, created_at TEXT DEFAULT (datetime('now')), updated_at TEXT DEFAULT (datetime('now')))")
	if err != nil {
		return err
	}

	// Schema migrations based on version
	var currentVersion int
	db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&currentVersion)

	if currentVersion < 1 {
		if _, err := db.Exec("ALTER TABLE devices ADD COLUMN user_id TEXT REFERENCES users(id)"); err != nil {
			log.Printf("[db] ALTER TABLE devices ADD user_id: %v", err)
		}
		db.Exec("INSERT INTO schema_version (version) VALUES (1)")
	}
	if currentVersion < 2 {
		if _, err := db.Exec("ALTER TABLE device_codes ADD COLUMN user_id TEXT"); err != nil {
			log.Printf("[db] ALTER TABLE device_codes ADD user_id: %v", err)
		}
		if _, err := db.Exec("ALTER TABLE device_codes ADD COLUMN token_hash TEXT"); err != nil {
			log.Printf("[db] ALTER TABLE device_codes ADD token_hash: %v", err)
		}
		db.Exec("INSERT INTO schema_version (version) VALUES (2)")
	}
return nil
}

func isLockError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "database is locked") || strings.Contains(s, "SQLITE_BUSY")
}

func upsertDevice(d Device) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var err error
	for i := 0; i < 3; i++ {
		_, err = db.Exec(`
			INSERT INTO devices (id, name, user_id, token_hash, status, brand, model, resolution, battery, last_seen)
			VALUES (?, ?, ?, ?, 'online', ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				name=excluded.name,
				user_id=excluded.user_id,
				token_hash=excluded.token_hash,
				status='online',
				brand=excluded.brand,
				model=excluded.model,
				resolution=excluded.resolution,
				battery=excluded.battery,
				last_seen=excluded.last_seen
		`, d.ID, d.Name, d.UserID, d.TokenHash, d.Brand, d.Model, d.Resolution, d.Battery, now)
		if err == nil {
			return nil
		}
		if !isLockError(err) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

func setDeviceOnline(deviceID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`UPDATE devices SET status='online', last_seen=? WHERE id=?`, now, deviceID)
	return err
}

func setDeviceOffline(deviceID string) error {
	_, err := db.Exec(`UPDATE devices SET status='offline' WHERE id=? AND status != 'unbound'`, deviceID)
	return err
}

func getDevices() ([]Device, error) {
	rows, err := db.Query(`SELECT id, COALESCE(user_id,'') as user_id, name, COALESCE(status,'offline') as status, brand, model, resolution, battery, last_seen, created_at FROM devices ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var d Device
		var lastSeen sql.NullString
		if err := rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Status, &d.Brand, &d.Model, &d.Resolution, &d.Battery, &lastSeen, &d.CreatedAt); err != nil {
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
	err := db.QueryRow(`SELECT id, COALESCE(user_id,'') as user_id, token_hash, name, status, brand, model, resolution, battery, last_seen, created_at FROM devices WHERE id=?`, id).
		Scan(&d.ID, &d.UserID, &d.TokenHash, &d.Name, &d.Status, &d.Brand, &d.Model, &d.Resolution, &d.Battery, &lastSeen, &d.CreatedAt)
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

func verifyDeviceToken(deviceID, token string) (*Device, error) {
	var d Device
	var tokenHash string
	var lastSeen sql.NullString
	err := db.QueryRow("SELECT id, COALESCE(user_id,'') as user_id, name, token_hash, status, brand, model, resolution, battery, last_seen, created_at FROM devices WHERE id=?", deviceID).
		Scan(&d.ID, &d.UserID, &d.Name, &tokenHash, &d.Status, &d.Brand, &d.Model, &d.Resolution, &d.Battery, &lastSeen, &d.CreatedAt)
	if err == sql.ErrNoRows {
		log.Printf("[auth] device %s not found in DB", deviceID)
		return nil, nil
	}
	if err != nil {
		log.Printf("[auth] DB error: %v", err)
		return nil, err
	}
	if tokenHash == "" {
		log.Printf("[auth] device %s has empty token_hash", deviceID)
		return nil, nil
	}
	if err := bcrypt.CompareHashAndPassword([]byte(tokenHash), []byte(token)); err != nil {
		log.Printf("[auth] bcrypt mismatch for device %s: %v", deviceID, err)
		return nil, nil
	}
	d.TokenHash = tokenHash
	if lastSeen.Valid {
		d.LastSeen = &lastSeen.String
	}
	return &d, nil
}

func getUserByID(id string) (*User, error) {
	var u User
	err := db.QueryRow("SELECT id, email, password, nickname, role, status, max_devices, created_at FROM users WHERE id=?", id).
		Scan(&u.ID, &u.Email, &u.Password, &u.Nickname, &u.Role, &u.Status, &u.MaxDevices, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func getUserByEmail(email string) (*User, error) {
	var u User
	err := db.QueryRow("SELECT id, email, password, nickname, role, status, max_devices, created_at FROM users WHERE email=?", email).
		Scan(&u.ID, &u.Email, &u.Password, &u.Nickname, &u.Role, &u.Status, &u.MaxDevices, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func createUserDB(email, password, nickname, role, createdBy string, maxDevices int) (string, error) {
	b := make([]byte, 8)
	rand.Read(b)
	id := hex.EncodeToString(b)
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	_, err := db.Exec("INSERT INTO users (id,email,password,nickname,role,max_devices,created_by) VALUES (?,?,?,?,?,?,?)",
		id, email, string(hash), nickname, role, maxDevices, createdBy)
	return id, err
}

func getUsersAll() ([]User, error) {
	rows, err := db.Query("SELECT id, email, nickname, role, status, max_devices, created_at FROM users ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		rows.Scan(&u.ID, &u.Email, &u.Nickname, &u.Role, &u.Status, &u.MaxDevices, &u.CreatedAt)
		users = append(users, u)
	}
	if users == nil {
		users = []User{}
	}
	return users, nil
}

func countPendingBindsForUser(userID string) int {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM device_codes WHERE user_id=? AND token_hash IS NOT NULL AND expires_at >= datetime('now')", userID).Scan(&count)
	return count
}

func countDevicesForUser(userID string) int {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM devices WHERE user_id=?", userID).Scan(&count)
	return count
}

func createPairingCode(deviceID string) (string, error) {
	db.Exec(`DELETE FROM device_codes WHERE device_id=? AND expires_at < datetime('now')`, deviceID)

	code := generateCode()
	expiresAt := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO device_codes (code, device_id, expires_at) VALUES (?, ?, ?)`, code, deviceID, expiresAt)
	return code, err
}

var pairingRateLimit = sync.Map{}

func checkPairingRateLimit(code string) bool {
	now := time.Now().Unix()
	val, _ := pairingRateLimit.LoadOrStore(code, now)
	lastTime := val.(int64)
	if now-lastTime > 60 {
		pairingRateLimit.Store(code, now)
		return true
	}
	return false
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
	b := make([]byte, 4)
	rand.Read(b)
	code := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	if code < 0 { code = -code }
	code = code%900000 + 100000
	return fmt.Sprintf("%d", code)
}

func hashToken(token string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("bcrypt error: %v", err)
		return ""
	}
	return string(hash)
}

// ============================================================
