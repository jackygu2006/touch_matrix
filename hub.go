package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
	"nhooyr.io/websocket"
)

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
	h.broadcastDeviceList()
	log.Printf("[hub] device %s connected", deviceID)
}

func (h *Hub) unregisterDevice(deviceID string, dc *deviceConn) {
	h.mu.Lock()
	current, ok := h.devices[deviceID]
	if ok && current == dc {
		delete(h.devices, deviceID)
	}
	h.mu.Unlock()

	// 只有确认是自己的连接才执行离线操作
	if ok && current == dc {
		setDeviceOffline(deviceID)
		h.broadcastDeviceList()
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
	return dc.conn.Write(bgCtx, websocket.MessageText, data)
}

func (h *Hub) broadcastFrame(deviceID string, frameData []byte) {
	header, err := json.Marshal(WSMessage{Type: "frame", DeviceID: deviceID})
	if err != nil {
		log.Printf("[hub] marshal frame header error: %v", err)
		return
	}

	// 先收集匹配的 dash 连接，再释放锁后写入
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
			dc.conn.Write(bgCtx, websocket.MessageText, header)
			dc.conn.Write(bgCtx, websocket.MessageBinary, frameData)
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

	dc := &deviceConn{conn: c}
	var deviceID string

	_, msgBytes, err := c.Read(bgCtx)
	if err != nil {
		log.Printf("[ws/device] read auth error: %v", err)
		return
	}

	var authMsg WSMessage
	if err := json.Unmarshal(msgBytes, &authMsg); err != nil || authMsg.Type != "auth" {
		log.Printf("[ws/device] auth parse error: %v, type=%s", err, authMsg.Type)
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
					dev = &Device{ID: authMsg.DeviceID, Name: authMsg.DeviceID, UserID: uid, TokenHash: tokenHash}
					if err := upsertDevice(*dev); err != nil {
						log.Printf("[ws/device] upsertDevice error for pending bind %s: %v", authMsg.DeviceID, err)
						c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "auth_fail", Reason: "server error"}))
						return
					}
					db.Exec("DELETE FROM device_codes WHERE device_id=?", authMsg.DeviceID)
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
		c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "auth_fail", Reason: "server error"}))
		return
	}

	c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "auth_ok"}))

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
				err := c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "ping"}))
				dc.mu.Unlock()
				if err != nil {
					return
				}
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
				case "device_ping":
					// 设备主动探测延迟，原样回传时间戳
					c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{
						Type: "device_pong", Text: msg.Text,
					}))
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

	devices, _ := getDevices()
	c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devices)}))

	// 定期同步设备状态（10s），检测变化时修正掉帧
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
				// P03: 设备列表未变化时跳过 DB 查询
				hub.mu.RLock()
				curVersion := hub.deviceVersion
				hub.mu.RUnlock()
				if curVersion == dc.lastVersion {
					continue
				}
				dc.lastVersion = curVersion
				dc.mu.Lock()
				devs, _ := getDevices()
				err := c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devs)}))
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
		case "cmd_tap", "cmd_swipe", "cmd_input", "cmd_task", "cmd_key":
			if msg.DeviceID == "" {
				msg.DeviceID = dc.deviceID
			}
			if msg.DeviceID != "" {
				if err := hub.sendToDevice(msg.DeviceID, msg); err != nil {
					c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{
						Type: "error", Reason: fmt.Sprintf("发送命令到设备失败: %s", err.Error()),
					}))
					log.Printf("[ws/dash] send command to device %s failed: %v", msg.DeviceID, err)
				} else {
					log.Printf("[ws/dash] command %s sent to device %s", msg.Type, msg.DeviceID)
				}
			}
		case "refresh":
			devices, _ := getDevices()
			c.Write(bgCtx, websocket.MessageText, mustJSON(WSMessage{Type: "device_list", Devices: filterDevicesForUser(dc, devices)}))
		}
	}
}

// ============================================================
