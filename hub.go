package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
	"nhooyr.io/websocket"
)

// bgCtx is the background context for non-timed operations.
var bgCtx = context.Background()

// writeTimeout is the maximum time to wait for a WebSocket write to complete.
const writeTimeout = 10 * time.Second

// wsWrite is a helper that writes to a WebSocket connection with a 10s timeout.
func wsWrite(conn *websocket.Conn, typ websocket.MessageType, data []byte) error {
	ctx, cancel := context.WithTimeout(bgCtx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, typ, data)
}

// WebSocket Hub
// ============================================================

// WSMessage is the universal message type for device↔server↔dashboard communication.
// Fields are grouped by message type; only relevant fields are populated per type.
//
// Device auth:         Type="auth",       DeviceID, Token, Info(device info)
// Device frame:        Type="frame",      DeviceID (binary frame follows separately)
// Device status:       Type="status",     DeviceID, Info(battery)
// Device unbind:       Type="unbind",     DeviceID
// Dash commands:       Type="cmd_tap"|"cmd_swipe"|"cmd_input"|"cmd_task"|"cmd_key",
//
//	DeviceID, X,Y / X1,Y1,X2,Y2 / Text / Prompt / Key
//
// Server→dash list:    Type="device_list",Devices
// Server→dash error:   Type="error",      Reason
// Server→device ping:  Type="ping"
// Connection control:  Type="auth_ok"|"auth_fail"|"pong"
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
	TaskID   int64           `json:"task_id,omitempty"`
	Key      string          `json:"key,omitempty"`
	Token    string          `json:"token,omitempty"`
	Info     json.RawMessage `json:"info,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Time     string          `json:"time,omitempty"`
	Devices  []Device        `json:"devices,omitempty"`
	Device   *Device         `json:"device,omitempty"`
}

type deviceConn struct {
	conn        *websocket.Conn
	deviceID    string
	lastFrame   time.Time
	permissions map[string]bool
	screenOn    bool
	mu          sync.Mutex
}

type dashConn struct {
	conn        *websocket.Conn
	deviceID    string
	watchAll    bool
	userID      string
	lastVersion int64
	mu          sync.Mutex
}

type Hub struct {
	mu            sync.RWMutex
	devices       map[string]*deviceConn
	dash          map[*dashConn]bool
	deviceVersion int64
	lastBroadcast time.Time
	deviceCache   []Device
	cacheTime     time.Time
}

var hub = &Hub{
	devices: make(map[string]*deviceConn),
	dash:    make(map[*dashConn]bool),
}

func (h *Hub) registerDevice(deviceID string, dc *deviceConn) {
	h.mu.Lock()
	var oldConn *websocket.Conn
	if old, ok := h.devices[deviceID]; ok {
		oldConn = old.conn
	}
	h.devices[deviceID] = dc
	if oldConn != nil {
		oldConn.Close(websocket.StatusNormalClosure, "replaced")
	}
	h.mu.Unlock()

	if err := setDeviceOnline(deviceID); err != nil {
		log.Printf("[hub] setDeviceOnline error for %s (retrying): %v", deviceID, err)
		for retry := 0; retry < 2; retry++ {
			time.Sleep(10 * time.Millisecond)
			if err2 := setDeviceOnline(deviceID); err2 == nil {
				break
			}
		}
	}
	hub.broadcastDeviceList()
	log.Printf("[hub] device %s connected", deviceID)
}

func (h *Hub) unregisterDevice(deviceID string, dc *deviceConn) {
	h.mu.Lock()
	current, ok := h.devices[deviceID]
	if ok && current == dc {
		delete(h.devices, deviceID)
	}
	h.mu.Unlock()

	// Only perform offline operations when confirmed as our own connection
	if ok && current == dc {
		setDeviceOffline(deviceID)
		hub.broadcastDeviceList()
		log.Printf("[hub] device %s disconnected", deviceID)
	}
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
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}
	return wsWrite(dc.conn, websocket.MessageText, data)
}

func (h *Hub) broadcastFrame(deviceID string, frameData []byte) {
	header, err := json.Marshal(WSMessage{Type: "frame", DeviceID: deviceID})
	if err != nil {
		log.Printf("[hub] marshal frame header error: %v", err)
		return
	}

	// Collect matching dash connections first, then write after releasing the lock
	h.mu.RLock()
	var targets []*dashConn
	for dc := range h.dash {
		if dc.deviceID == deviceID || dc.watchAll {
			targets = append(targets, dc)
		}
	}
	h.mu.RUnlock()

	for _, dc := range targets {
		go func(dc *dashConn) {
			dc.mu.Lock()
			defer dc.mu.Unlock()
			wsWrite(dc.conn, websocket.MessageText, header)
			wsWrite(dc.conn, websocket.MessageBinary, frameData)
		}(dc)
	}
}

func (h *Hub) broadcastToDash(msg WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[hub] marshal broadcast error: %v", err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for dc := range h.dash {
		dc.mu.Lock()
		wsWrite(dc.conn, websocket.MessageText, data)
		dc.mu.Unlock()
	}
}

func filterDevicesForUser(dc *dashConn, devices []Device) []Device {
	var result []Device
	for _, d := range devices {
		if dc.userID == "" || d.UserID == "" || d.UserID == dc.userID {
			result = append(result, d)
		}
	}
	if result == nil {
		result = []Device{}
	}
	return result
}

func (h *Hub) broadcastDeviceList() {
	h.mu.Lock()
	now := time.Now()
	if now.Sub(h.lastBroadcast) < 500*time.Millisecond {
		h.mu.Unlock()
		return
	}
	h.lastBroadcast = now
	h.mu.Unlock()

	devices, err := getDevices()
	if err != nil {
		log.Printf("[hub] error getting device list: %v", err)
		return
	}

	h.mu.RLock()
	for i := range devices {
		if dc, ok := h.devices[devices[i].ID]; ok {
			devices[i].LastFrame = dc.lastFrame
			devices[i].Permissions = dc.permissions
			devices[i].ScreenOn = dc.screenOn
		}
	}
	h.mu.RUnlock()

	h.mu.RLock()
	defer h.mu.RUnlock()
	for dc := range h.dash {
		userDevices := filterDevicesForUser(dc, devices)
		msg, err := json.Marshal(WSMessage{Type: "device_list", Devices: userDevices})
		if err != nil {
			log.Printf("[hub] marshal device_list error: %v", err)
			continue
		}
		dc.mu.Lock()
		wsWrite(dc.conn, websocket.MessageText, msg)
		dc.mu.Unlock()
	}
}

// ============================================================
// WebSocket Handlers
// ============================================================

func handleDeviceWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		log.Printf("[ws/device] accept error: %v", err)
		return
	}
	var statusCode websocket.StatusCode = websocket.StatusNormalClosure
	defer func() {
		c.Close(statusCode, "")
	}()
	c.SetReadLimit(2 << 20) // 2MB for screen frames

	dc := &deviceConn{conn: c, screenOn: true}
	var deviceID string

	// 30s timeout for initial auth read
	readCtx, readCancel := context.WithTimeout(bgCtx, 30*time.Second)
	_, msgBytes, err := c.Read(readCtx)
	readCancel()
	if err != nil {
		log.Printf("[ws/device] read auth error: %v", err)
		return
	}

	var authMsg WSMessage
	if err := json.Unmarshal(msgBytes, &authMsg); err != nil || authMsg.Type != "auth" {
		log.Printf("[ws/device] auth parse error: %v, type=%s", err, authMsg.Type)
		wsWrite(c, websocket.MessageText, mustJSON(WSMessage{Type: "auth_fail", Reason: "invalid auth message"}))
		return
	}

	dev, err := verifyDeviceToken(authMsg.DeviceID, authMsg.Token)
	log.Printf("[ws/device] auth attempt: deviceID=%s tokenLen=%d", authMsg.DeviceID, len(authMsg.Token))
	if err != nil || dev == nil {
		// Device not found: check for pending bind tokens
		// Check all pending bind tokens in device_codes
		rows, err2 := db.Query("SELECT token_hash FROM device_codes WHERE device_id=? AND token_hash IS NOT NULL", authMsg.DeviceID)
		if err2 == nil {
			defer rows.Close()
			for rows.Next() {
				var tokenHash string
				rows.Scan(&tokenHash)
				if tokenHash != "" && bcrypt.CompareHashAndPassword([]byte(tokenHash), []byte(authMsg.Token)) == nil {
					log.Printf("[ws/device] creating device from pending bind: %s (tokenHash len=%d)", authMsg.DeviceID, len(tokenHash))
					rows.Close()
					var uid string
					db.QueryRow("SELECT user_id FROM device_codes WHERE device_id=? AND token_hash IS NOT NULL", authMsg.DeviceID).Scan(&uid)
					dev = &Device{ID: authMsg.DeviceID, Name: authMsg.DeviceID, UserID: uid, TokenHash: tokenHash}
					if err := upsertDevice(*dev); err != nil {
						log.Printf("[ws/device] upsertDevice error for pending bind %s: %v", authMsg.DeviceID, err)
						wsWrite(c, websocket.MessageText, mustJSON(WSMessage{Type: "auth_fail", Reason: "server error"}))
						return
					}
					db.Exec("DELETE FROM device_codes WHERE device_id=?", authMsg.DeviceID)
					break
				}
			}
		}
		if dev == nil {
			wsWrite(c, websocket.MessageText, mustJSON(WSMessage{Type: "auth_fail", Reason: "invalid token"}))
			log.Printf("[ws/device] auth failed: token not found")
			return
		}
	}

	deviceID = dev.ID
	dc.lastFrame = time.Now()
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
	if err := upsertDevice(*dev); err != nil {
		log.Printf("[ws/device] upsertDevice error for %s: %v", deviceID, err)
		wsWrite(c, websocket.MessageText, mustJSON(WSMessage{Type: "auth_fail", Reason: "server error"}))
		return
	}

	wsWrite(c, websocket.MessageText, mustJSON(WSMessage{Type: "auth_ok"}))

	hub.registerDevice(deviceID, dc)
	defer hub.unregisterDevice(deviceID, dc)

	// ping goroutine with exit signal
	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pingDone:
				return
			case <-ticker.C:
				dc.mu.Lock()
				err := wsWrite(c, websocket.MessageText, mustJSON(WSMessage{Type: "ping"}))
				dc.mu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

	for {
		// 15s read timeout: devices send device_ping every 5s, 3x tolerance for fast disconnection detection
		readCtx, cancel := context.WithTimeout(bgCtx, 15*time.Second)
		msgType, data, err := c.Read(readCtx)
		cancel()
		if err != nil {
			log.Printf("[ws/device] %s read error: %v", deviceID, err)
			return
		}

		dc.lastFrame = time.Now()

		if msgType == websocket.MessageBinary {
			hub.broadcastFrame(deviceID, data)
		} else {
			var msg WSMessage
			if json.Unmarshal(data, &msg) == nil {
				if msg.Type == "task_status" {
					log.Printf("[debug] received task_status from device=%s text=%.80s task_id=%d", deviceID, msg.Text, msg.TaskID)
				}
				switch msg.Type {
				case "pong":
				case "device_ping":
					// Device actively probes latency, echo the timestamp back
					wsWrite(c, websocket.MessageText, mustJSON(WSMessage{
						Type: "device_pong", Text: msg.Text,
					}))
				case "unbind":
					db.Exec("UPDATE devices SET status='unbound' WHERE id=?", deviceID)
					go hub.broadcastDeviceList()
				case "task_status":
					status := "running"
					if strings.Contains(msg.Text, "完成任务") || strings.Contains(msg.Text, "任务完成") || strings.Contains(msg.Text, "✅") {
						status = "completed"
					} else if strings.Contains(msg.Text, "失败") || strings.Contains(msg.Text, "错误") {
						status = "failed"
					} else if strings.Contains(msg.Text, "取消") {
						status = "cancelled"
					}
					if msg.TaskID > 0 {
						log.Printf("[task] device=%s task_id=%d status=%s text=%.80s", deviceID, msg.TaskID, status, msg.Text)
						updateTaskRecord(msg.TaskID, status, msg.Text)
					} else {
						var id int64
						db.QueryRow("SELECT id FROM task_history WHERE device_id=? ORDER BY created_at DESC LIMIT 1", deviceID).Scan(&id)
						log.Printf("[task] device=%s task_id=0(fallback→%d) status=%s text=%.80s", deviceID, id, status, msg.Text)
						if id > 0 {
							updateTaskRecord(id, status, msg.Text)
						}
					}
					hub.broadcastToDash(WSMessage{Type: "task_status", DeviceID: deviceID, TaskID: msg.TaskID, Text: msg.Text, Time: time.Now().Format("15:04:05")})
				case "status":
					if msg.Info != nil {
						var info struct {
							Battery     int             `json:"battery"`
							ScreenOn    bool            `json:"screen_on"`
							Permissions map[string]bool `json:"permissions"`
						}
						json.Unmarshal(msg.Info, &info)
						db.Exec(`UPDATE devices SET battery=?, last_seen=? WHERE id=?`,
							info.Battery, time.Now().UTC().Format(time.RFC3339), deviceID)
						dc.mu.Lock()
						dc.permissions = info.Permissions
						dc.screenOn = info.ScreenOn
						dc.mu.Unlock()
						hub.broadcastDeviceList()
					}
				default:
					log.Printf("[ws/device] %s unknown message type: %s", deviceID, msg.Type)
				}
			}
		}
	}
}

func handleDashWS(w http.ResponseWriter, r *http.Request) {
	// Check JWT from cookie/header, or token query param
	var queryTokenUser *User
	if getUserFromRequest(r) == nil {
		token := r.URL.Query().Get("token")
		if token != "" {
			claims, err := validateJWT(token)
			if err == nil {
				// Valid JWT - allow connection, and bind user from token
				u, _ := getUserByID(claims.UserID)
				if u != nil && u.Status == "active" {
					queryTokenUser = u
				}
			} else {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		} else {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		log.Printf("[ws/dash] accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "")

	u := getUserFromRequest(r)
	dc := &dashConn{conn: c}
	if u != nil {
		dc.userID = u.ID
	} else if queryTokenUser != nil {
		dc.userID = queryTokenUser.ID
	}
	hub.registerDash(dc)
	defer hub.unregisterDash(dc)

	// Send initial device_list with in-memory permissions merged
	devices, _ := getDevices()
	hub.mu.RLock()
	for i := range devices {
		if devConn, ok := hub.devices[devices[i].ID]; ok {
			devices[i].Permissions = devConn.permissions
			devices[i].ScreenOn = devConn.screenOn
			devices[i].LastFrame = devConn.lastFrame
		}
	}
	hub.mu.RUnlock()
	wsWrite(c, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devices)}))

	// Periodically sync device status (10s), correct missed updates when changes detected
	syncDone := make(chan struct{})
	defer close(syncDone)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-syncDone:
				return
			case <-ticker.C:
				// P03: skip DB query when device list hasn't changed
				hub.mu.RLock()
				curVersion := hub.deviceVersion
				hub.mu.RUnlock()
				if curVersion == dc.lastVersion {
					continue
				}
				dc.lastVersion = curVersion
				dc.mu.Lock()
				devs, _ := getDevices()
				err := wsWrite(c, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devs)}))
				dc.mu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

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
		case "cmd_tap", "cmd_swipe", "cmd_input", "cmd_task", "cmd_key", "cmd_cancel_task":
			if msg.DeviceID == "" {
				msg.DeviceID = dc.deviceID
			}
			if msg.DeviceID != "" {
				if err := hub.sendToDevice(msg.DeviceID, msg); err != nil {
					wsWrite(c, websocket.MessageText, mustJSON(WSMessage{
						Type: "error", Reason: fmt.Sprintf("send command to device failed: %s", err.Error()),
					}))
					log.Printf("[ws/dash] send command to device %s failed: %v", msg.DeviceID, err)
				} else {
					log.Printf("[ws/dash] command %s sent to device %s", msg.Type, msg.DeviceID)
				}
			}
		case "refresh":
			devices, _ := getDevices()
			wsWrite(c, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devices)}))
		}
	}
}

func (h *Hub) Start() {
	// On boot: mark all online devices as offline, since in-memory state is fresh
	if _, err := db.Exec("UPDATE devices SET status='offline' WHERE status='online'"); err != nil {
		log.Printf("[hub] failed to reset device status on startup: %v", err)
	}
	go h.watchdog()
}

func (h *Hub) watchdog() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		h.checkDevices()
		go hub.broadcastDeviceList()
	}
}

func (h *Hub) checkDevices() {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for deviceID, dc := range h.devices {
		dc.mu.Lock()
		age := time.Since(dc.lastFrame)
		dc.mu.Unlock()
		if age > 10*time.Second {
			log.Printf("[hub] watchdog: device %s no message for %.0fs, closing", deviceID, age.Seconds())
			dc.conn.Close(websocket.StatusNormalClosure, "watchdog: no data")
		}
	}
}

// ============================================================
