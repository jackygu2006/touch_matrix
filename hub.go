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

const (
	deviceReadTimeout     = 15 * time.Second
	deviceWatchdogTimeout = 20 * time.Second
	deviceListCacheTTL    = 30 * time.Second
)

// wsWrite is a helper that writes to a WebSocket connection with a 10s timeout.
func wsWrite(conn *websocket.Conn, typ websocket.MessageType, data []byte) error {
	ctx, cancel := context.WithTimeout(bgCtx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, typ, data)
}

func cloneDeviceSnapshot(d Device) Device {
	cloned := d
	if d.LastSeen != nil {
		lastSeen := *d.LastSeen
		cloned.LastSeen = &lastSeen
	}
	if d.Permissions != nil {
		cloned.Permissions = make(map[string]bool, len(d.Permissions))
		for k, v := range d.Permissions {
			cloned.Permissions[k] = v
		}
	}
	return cloned
}

func cloneDeviceSnapshots(devices []Device) []Device {
	cloned := make([]Device, len(devices))
	for i, d := range devices {
		cloned[i] = cloneDeviceSnapshot(d)
	}
	return cloned
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
	Mime     string          `json:"mime,omitempty"`
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
	userID      string
	lastMessage time.Time
	lastFrame   time.Time
	permissions map[string]bool
	screenOn    bool
	mu          sync.Mutex
}

type dashTextMessage struct {
	typ  websocket.MessageType
	data []byte
}

type dashFrameMessage struct {
	header []byte
	data   []byte
}

type dashConn struct {
	conn        *websocket.Conn
	deviceID    string
	watchAll    bool
	userID      string
	role        string
	lastVersion int64
	stateMu     sync.RWMutex
	textCh      chan dashTextMessage
	frameCh     chan dashFrameMessage
	done        chan struct{}
	closeOnce   sync.Once
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

func newDashConn(conn *websocket.Conn) *dashConn {
	return &dashConn{
		conn:    conn,
		textCh:  make(chan dashTextMessage, 32),
		frameCh: make(chan dashFrameMessage, 1),
		done:    make(chan struct{}),
	}
}

func (dc *dashConn) runWriter() {
	for {
		select {
		case <-dc.done:
			return
		case msg := <-dc.textCh:
			if err := wsWrite(dc.conn, msg.typ, msg.data); err != nil {
				dc.close()
				return
			}
		default:
			select {
			case <-dc.done:
				return
			case msg := <-dc.textCh:
				if err := wsWrite(dc.conn, msg.typ, msg.data); err != nil {
					dc.close()
					return
				}
			case frame := <-dc.frameCh:
				if err := wsWrite(dc.conn, websocket.MessageText, frame.header); err != nil {
					dc.close()
					return
				}
				if err := wsWrite(dc.conn, websocket.MessageBinary, frame.data); err != nil {
					dc.close()
					return
				}
			}
		}
	}
}

func (dc *dashConn) close() {
	dc.closeOnce.Do(func() {
		close(dc.done)
		_ = dc.conn.Close(websocket.StatusNormalClosure, "dash writer closed")
	})
}

func (dc *dashConn) enqueueText(msgType websocket.MessageType, data []byte) error {
	select {
	case <-dc.done:
		return fmt.Errorf("dash connection closed")
	case dc.textCh <- dashTextMessage{typ: msgType, data: data}:
		return nil
	}
}

func (dc *dashConn) enqueueJSON(msg WSMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return dc.enqueueText(websocket.MessageText, data)
}

func (dc *dashConn) enqueueFrame(header, data []byte) error {
	frame := dashFrameMessage{header: header, data: data}
	select {
	case <-dc.done:
		return fmt.Errorf("dash connection closed")
	default:
	}

	select {
	case dc.frameCh <- frame:
		return nil
	default:
	}

	select {
	case <-dc.frameCh:
	default:
	}

	select {
	case <-dc.done:
		return fmt.Errorf("dash connection closed")
	case dc.frameCh <- frame:
		return nil
	default:
		return nil
	}
}

func (dc *dashConn) setWatchState(deviceID string, watchAll bool) {
	dc.stateMu.Lock()
	dc.deviceID = deviceID
	dc.watchAll = watchAll
	dc.stateMu.Unlock()
}

func (dc *dashConn) getWatchState() (string, bool) {
	dc.stateMu.RLock()
	defer dc.stateMu.RUnlock()
	return dc.deviceID, dc.watchAll
}

func (dc *dashConn) setLastVersion(version int64) {
	dc.stateMu.Lock()
	dc.lastVersion = version
	dc.stateMu.Unlock()
}

func (dc *dashConn) getLastVersion() int64 {
	dc.stateMu.RLock()
	defer dc.stateMu.RUnlock()
	return dc.lastVersion
}

func (h *Hub) bumpDeviceVersion() {
	h.mu.Lock()
	h.deviceVersion++
	h.mu.Unlock()
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
	h.bumpDeviceVersion()
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
		h.bumpDeviceVersion()
		hub.broadcastDeviceList()
		log.Printf("[hub] device %s disconnected", deviceID)
	}
}

func (h *Hub) dropDevice(deviceID, reason string) {
	h.mu.Lock()
	dc, ok := h.devices[deviceID]
	if ok {
		delete(h.devices, deviceID)
	}
	h.mu.Unlock()

	if !ok {
		return
	}

	if dc.conn != nil {
		_ = dc.conn.Close(websocket.StatusNormalClosure, reason)
	}
	if err := setDeviceOffline(deviceID); err != nil {
		log.Printf("[hub] setDeviceOffline error for %s: %v", deviceID, err)
	}
	h.bumpDeviceVersion()
	h.broadcastDeviceList()
	log.Printf("[hub] device %s dropped: %s", deviceID, reason)
}

func (h *Hub) markDeviceUnbound(deviceID, reason string) {
	if _, err := db.Exec("UPDATE devices SET status='unbound' WHERE id=?", deviceID); err != nil {
		log.Printf("[hub] mark device %s unbound error: %v", deviceID, err)
		return
	}

	h.mu.RLock()
	_, connected := h.devices[deviceID]
	h.mu.RUnlock()
	if connected {
		h.dropDevice(deviceID, reason)
		return
	}

	h.bumpDeviceVersion()
	h.broadcastDeviceList()
	log.Printf("[hub] device %s marked unbound: %s", deviceID, reason)
}

func (h *Hub) dropDevicesByUser(userID, reason string) {
	h.mu.RLock()
	var deviceIDs []string
	for deviceID, dc := range h.devices {
		if dc.userID == userID {
			deviceIDs = append(deviceIDs, deviceID)
		}
	}
	h.mu.RUnlock()

	for _, deviceID := range deviceIDs {
		h.dropDevice(deviceID, reason)
	}
}

func (h *Hub) registerDash(dc *dashConn) {
	h.mu.Lock()
	h.dash[dc] = true
	total := len(h.dash)
	h.mu.Unlock()
	go dc.runWriter()
	log.Printf("[hub] dashboard connected (total: %d)", total)
}

func (h *Hub) unregisterDash(dc *dashConn) {
	h.mu.Lock()
	delete(h.dash, dc)
	total := len(h.dash)
	h.mu.Unlock()
	dc.close()
	log.Printf("[hub] dashboard disconnected (total: %d)", total)
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
	header, err := json.Marshal(WSMessage{Type: "frame", DeviceID: deviceID, Mime: "image/webp"})
	if err != nil {
		log.Printf("[hub] marshal frame header error: %v", err)
		return
	}

	// Collect matching dash connections first, then write after releasing the lock
	h.mu.RLock()
	source, sourceOK := h.devices[deviceID]
	var targets []*dashConn
	for dc := range h.dash {
		watchDeviceID, watchAll := dc.getWatchState()
		allowed := false
		if sourceOK {
			allowed = source.userID != "" && dc.userID == source.userID
		}
		if allowed && (watchDeviceID == deviceID || watchAll) {
			targets = append(targets, dc)
		}
	}
	h.mu.RUnlock()

	for _, dc := range targets {
		if err := dc.enqueueFrame(header, frameData); err != nil {
			log.Printf("[hub] enqueue frame failed: %v", err)
		}
	}
}

func (h *Hub) broadcastToDash(msg WSMessage) {
	h.mu.RLock()
	var targets []*dashConn
	for dc := range h.dash {
		targets = append(targets, dc)
	}
	h.mu.RUnlock()
	for _, dc := range targets {
		if err := dc.enqueueJSON(msg); err != nil {
			log.Printf("[hub] enqueue broadcast error: %v", err)
		}
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

func mergeRuntimeStateIntoDevice(dev *Device) {
	if dev == nil {
		return
	}
	hub.mu.RLock()
	dc, ok := hub.devices[dev.ID]
	hub.mu.RUnlock()
	if !ok {
		return
	}
	dc.mu.Lock()
	dev.LastFrame = dc.lastFrame
	dev.LastMessage = dc.lastMessage
	dev.Permissions = dc.permissions
	dev.ScreenOn = dc.screenOn
	dc.mu.Unlock()
}

func mergeRuntimeDeviceState(devices []Device) []Device {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for i := range devices {
		if dc, ok := hub.devices[devices[i].ID]; ok {
			dc.mu.Lock()
			devices[i].LastFrame = dc.lastFrame
			devices[i].LastMessage = dc.lastMessage
			devices[i].Permissions = dc.permissions
			devices[i].ScreenOn = dc.screenOn
			dc.mu.Unlock()
		}
	}
	return devices
}

func (h *Hub) setDeviceCache(devices []Device) {
	h.mu.Lock()
	h.deviceCache = cloneDeviceSnapshots(devices)
	h.cacheTime = time.Now()
	h.mu.Unlock()
}

func (h *Hub) getCachedDeviceSnapshot(maxAge time.Duration) ([]Device, bool) {
	h.mu.RLock()
	if h.cacheTime.IsZero() || time.Since(h.cacheTime) > maxAge {
		h.mu.RUnlock()
		return nil, false
	}
	devices := cloneDeviceSnapshots(h.deviceCache)
	h.mu.RUnlock()
	return mergeRuntimeDeviceState(devices), true
}

func loadDevicesSnapshot(load func() ([]Device, error)) ([]Device, error) {
	devices, err := load()
	if err != nil {
		if isLockError(err) {
			if cached, ok := hub.getCachedDeviceSnapshot(deviceListCacheTTL); ok {
				log.Printf("[hub] loadDevicesSnapshot: fallback to cached snapshot after lock error: %v", err)
				return cached, nil
			}
		}
		return nil, err
	}
	devices = mergeRuntimeDeviceState(devices)
	hub.setDeviceCache(devices)
	return devices, nil
}

func loadAllDevicesSnapshot() ([]Device, error) {
	return loadDevicesSnapshot(getDevices)
}

func enqueueDeviceListSnapshot(dc *dashConn, load func() ([]Device, error)) error {
	devices, err := load()
	if err != nil {
		_ = dc.enqueueJSON(WSMessage{Type: "error", Reason: "device list unavailable"})
		return err
	}
	return dc.enqueueJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devices)})
}

func shouldCloseStaleDevice(lastMessage, now time.Time) bool {
	return now.Sub(lastMessage) > deviceWatchdogTimeout
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

	devices, err := loadAllDevicesSnapshot()
	if err != nil {
		log.Printf("[hub] error getting device list: %v", err)
		return
	}

	h.mu.RLock()
	var targets []*dashConn
	for dc := range h.dash {
		targets = append(targets, dc)
	}
	h.mu.RUnlock()

	for _, dc := range targets {
		userDevices := filterDevicesForUser(dc, devices)
		if err := dc.enqueueJSON(WSMessage{Type: "device_list", Devices: userDevices}); err != nil {
			log.Printf("[hub] enqueue device_list error: %v", err)
		}
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
	if parseErr := json.Unmarshal(msgBytes, &authMsg); parseErr != nil || authMsg.Type != "auth" {
		log.Printf("[ws/device] auth parse error: %v, type=%s", parseErr, authMsg.Type)
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
	now := time.Now()
	dc.lastMessage = now
	dc.deviceID = deviceID
	dc.userID = dev.UserID

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
		readCtx, cancel := context.WithTimeout(bgCtx, deviceReadTimeout)
		msgType, data, err := c.Read(readCtx)
		cancel()
		if err != nil {
			log.Printf("[ws/device] %s read error: %v", deviceID, err)
			return
		}

		dc.mu.Lock()
		dc.lastMessage = time.Now()
		dc.mu.Unlock()

		if msgType == websocket.MessageBinary {
			dc.mu.Lock()
			dc.lastFrame = time.Now()
			dc.mu.Unlock()
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
					dc.mu.Lock()
					err := wsWrite(c, websocket.MessageText, mustJSON(WSMessage{
						Type: "device_pong", Text: msg.Text,
					}))
					dc.mu.Unlock()
					if err != nil {
						return
					}
				case "unbind":
					hub.markDeviceUnbound(deviceID, "device unbound")
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
						hub.bumpDeviceVersion()
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
	dc := newDashConn(c)
	if u != nil {
		dc.userID = u.ID
		dc.role = u.Role
	} else if queryTokenUser != nil {
		dc.userID = queryTokenUser.ID
		dc.role = queryTokenUser.Role
	}
	hub.registerDash(dc)
	defer hub.unregisterDash(dc)

	// Send initial device_list with in-memory permissions merged
	if err := enqueueDeviceListSnapshot(dc, loadAllDevicesSnapshot); err != nil {
		log.Printf("[ws/dash] initial device_list unavailable: %v", err)
		return
	}

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
				if curVersion == dc.getLastVersion() {
					continue
				}
				dc.setLastVersion(curVersion)
				if err := enqueueDeviceListSnapshot(dc, loadAllDevicesSnapshot); err != nil {
					log.Printf("[ws/dash] periodic device_list unavailable: %v", err)
					continue
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
			dev, allowed, err := canAccessDevice(dc.userID, dc.role, msg.DeviceID)
			if err != nil {
				_ = dc.enqueueJSON(WSMessage{Type: "error", Reason: "server error"})
				continue
			}
			if dev == nil {
				_ = dc.enqueueJSON(WSMessage{Type: "error", Reason: "device not found"})
				continue
			}
			if !allowed {
				_ = dc.enqueueJSON(WSMessage{Type: "error", Reason: "access denied"})
				continue
			}
			dc.setWatchState(msg.DeviceID, false)
			log.Printf("[ws/dash] watching device: %s", msg.DeviceID)
		case "watch_all":
			dc.setWatchState("", true)
			log.Printf("[ws/dash] watching all devices")
		case "watch_one":
			dev, allowed, err := canAccessDevice(dc.userID, dc.role, msg.DeviceID)
			if err != nil {
				_ = dc.enqueueJSON(WSMessage{Type: "error", Reason: "server error"})
				continue
			}
			if dev == nil {
				_ = dc.enqueueJSON(WSMessage{Type: "error", Reason: "device not found"})
				continue
			}
			if !allowed {
				_ = dc.enqueueJSON(WSMessage{Type: "error", Reason: "access denied"})
				continue
			}
			dc.setWatchState(msg.DeviceID, false)
			log.Printf("[ws/dash] watching one: %s", msg.DeviceID)
		case "cmd_tap", "cmd_swipe", "cmd_input", "cmd_task", "cmd_key", "cmd_cancel_task":
			if msg.DeviceID == "" {
				msg.DeviceID, _ = dc.getWatchState()
			}
			if msg.DeviceID != "" {
				dev, allowed, err := canAccessDevice(dc.userID, dc.role, msg.DeviceID)
				if err != nil {
					_ = dc.enqueueJSON(WSMessage{
						Type: "error", Reason: "server error",
					})
					continue
				}
				if dev == nil {
					_ = dc.enqueueJSON(WSMessage{
						Type: "error", Reason: "device not found",
					})
					continue
				}
				if !allowed {
					_ = dc.enqueueJSON(WSMessage{
						Type: "error", Reason: "access denied",
					})
					continue
				}
				if err := hub.sendToDevice(msg.DeviceID, msg); err != nil {
					_ = dc.enqueueJSON(WSMessage{
						Type: "error", Reason: fmt.Sprintf("send command to device failed: %s", err.Error()),
					})
					log.Printf("[ws/dash] send command to device %s failed: %v", msg.DeviceID, err)
				} else {
					log.Printf("[ws/dash] command %s sent to device %s", msg.Type, msg.DeviceID)
				}
			}
		case "refresh":
			if err := enqueueDeviceListSnapshot(dc, loadAllDevicesSnapshot); err != nil {
				log.Printf("[ws/dash] refresh device_list unavailable: %v", err)
			}
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
	now := time.Now()
	for deviceID, dc := range h.devices {
		dc.mu.Lock()
		lastMessage := dc.lastMessage
		age := now.Sub(lastMessage)
		dc.mu.Unlock()
		if shouldCloseStaleDevice(lastMessage, now) {
			log.Printf("[hub] watchdog: device %s no message for %.0fs, closing", deviceID, age.Seconds())
			dc.conn.Close(websocket.StatusNormalClosure, "watchdog: no data")
		}
	}
}

// ============================================================
