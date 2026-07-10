package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
	"golang.org/x/crypto/bcrypt"
	"nhooyr.io/websocket"
	_ "modernc.org/sqlite"
)



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
	conn      *websocket.Conn
	deviceID  string
	lastFrame time.Time
	mu        sync.Mutex
}

type dashConn struct {
	conn     *websocket.Conn
	deviceID string
	watchAll bool
	userID   string
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

func filterDevicesForUser(dc *dashConn, devices []Device) []Device {
	var result []Device
	for _, d := range devices {
		if dc.userID == "" || d.UserID == "" || d.UserID == dc.userID {
			result = append(result, d)
		}
	}
	if result == nil { result = []Device{} }
	return result
}

func (h *Hub) broadcastDeviceList() {
	devices, err := getDevices()
	if err != nil {
		log.Printf("[hub] error getting device list: %v", err)
		return
	}

	// 注入各设备的 lastFrame 时间
	h.mu.RLock()
	for i := range devices {
		if dc, ok := h.devices[devices[i].ID]; ok {
			devices[i].LastFrame = dc.lastFrame
		}
	}
	h.mu.RUnlock()

	h.mu.RLock()
	defer h.mu.RUnlock()
	for dc := range h.dash {
		// Filter by user
		var userDevices []Device
		for _, d := range devices {
			if dc.userID == "" || d.UserID == "" || d.UserID == dc.userID {
				userDevices = append(userDevices, d)
			}
		}
		if userDevices == nil { userDevices = []Device{} }
		msg, _ := json.Marshal(WSMessage{Type: "device_list", Devices: userDevices})
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

	dev, err := verifyDeviceToken(authMsg.DeviceID, authMsg.Token)
	log.Printf("[ws/device] auth attempt: deviceID=%s tokenLen=%d", authMsg.DeviceID, len(authMsg.Token))
	if err != nil || dev == nil {
		// 设备不存在：检查是否有待绑定的token
		// 检查 device_codes 中所有待绑定的 token
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
					_, err3 := db.Exec("INSERT INTO devices (id, name, user_id, token_hash, status, created_at) VALUES (?, ?, ?, ?, 'online', datetime('now')) ON CONFLICT(id) DO UPDATE SET token_hash=excluded.token_hash, user_id=excluded.user_id, status='online'", authMsg.DeviceID, authMsg.DeviceID, uid, tokenHash)
					log.Printf("[ws/device] insert result: %v, tokenHash len=%d", err3, len(tokenHash))
					db.Exec("DELETE FROM device_codes WHERE device_id=?", authMsg.DeviceID)
					dev, _ = getDevice(authMsg.DeviceID)
					break
				}
			}
		}
		if dev == nil {
			c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "auth_fail", Reason: "invalid token"}))
			log.Printf("[ws/device] auth failed: token not found")
			return
		}
	}

	deviceID = dev.ID
	dc.lastFrame = time.Now() // 初始化为当前时间，避免误报"无画面"
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
				case "unbind":
					db.Exec("UPDATE devices SET status='unbound' WHERE id=?", deviceID)
					go hub.broadcastDeviceList()
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
	// Check JWT from cookie/header, or token query param
	if getUserFromRequest(r) == nil {
		token := r.URL.Query().Get("token")
		if token != "" {
			_, err := validateJWT(token)
			if err == nil {
				// Valid JWT - allow connection
			} else {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		} else if !checkSession(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[ws/dash] accept error: %v", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "")

	u := getUserFromRequest(r)
	dc := &dashConn{conn: c}
	if u != nil { dc.userID = u.ID }
	hub.registerDash(dc)
	defer hub.unregisterDash(dc)

	devices, _ := getDevices()
	c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devices)}))

	// 定期同步设备状态（10s），修正可能的掉帧
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			dc.mu.Lock()
			devs, _ := getDevices()
			err := c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devs)}))
			dc.mu.Unlock()
			if err != nil { return }
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
			c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devices)}))
		}
	}
}

// ============================================================
