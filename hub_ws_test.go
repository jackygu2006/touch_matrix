package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func newDashWSTestConn(t *testing.T, token string) (*websocket.Conn, func()) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/dash", handleDashWS)
	srv := httptest.NewServer(mux)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dash?token=" + url.QueryEscape(token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	cancel()
	if err != nil {
		srv.Close()
		t.Fatalf("websocket dial failed: %v", err)
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	if _, _, err := conn.Read(readCtx); err != nil {
		conn.Close(websocket.StatusInternalError, "read init")
		srv.Close()
		t.Fatalf("failed to read initial device_list: %v", err)
	}

	cleanup := func() {
		_ = conn.Close(websocket.StatusNormalClosure, "test done")
		srv.Close()
	}
	return conn, cleanup
}

func writeWSMessage(t *testing.T, conn *websocket.Conn, msg WSMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal ws message: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write ws message: %v", err)
	}
}

func readWSMessage(t *testing.T, conn *websocket.Conn) WSMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read ws message: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal ws message: %v", err)
	}
	return msg
}

func TestDashWatchOneForbidden(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "ws-owner@nftouch.local", "test123456", "user")
	viewer := createTestUser(t, "ws-viewer@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "ws-dev-owned", owner.ID)
	token, _ := generateJWT(viewer)

	conn, cleanup := newDashWSTestConn(t, token)
	defer cleanup()

	writeWSMessage(t, conn, WSMessage{Type: "watch_one", DeviceID: "ws-dev-owned"})
	msg := readWSMessage(t, conn)
	if msg.Type != "error" || msg.Reason != "access denied" {
		t.Fatalf("expected access denied error, got type=%s reason=%s", msg.Type, msg.Reason)
	}
}

func TestDashCommandForbidden(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "cmd-owner@nftouch.local", "test123456", "user")
	viewer := createTestUser(t, "cmd-viewer@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "cmd-dev-owned", owner.ID)
	token, _ := generateJWT(viewer)

	conn, cleanup := newDashWSTestConn(t, token)
	defer cleanup()

	writeWSMessage(t, conn, WSMessage{Type: "cmd_task", DeviceID: "cmd-dev-owned", Prompt: "hello"})
	msg := readWSMessage(t, conn)
	if msg.Type != "error" || msg.Reason != "access denied" {
		t.Fatalf("expected access denied error, got type=%s reason=%s", msg.Type, msg.Reason)
	}
}

func TestDashRefreshReturnsMergedRuntimeState(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "refresh@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "refresh-dev", u.ID)
	token, _ := generateJWT(u)

	frameAt := time.Now().Add(-2 * time.Second).UTC().Truncate(time.Second)
	msgAt := time.Now().UTC().Truncate(time.Second)
	hub.mu.Lock()
	hub.devices["refresh-dev"] = &deviceConn{
		deviceID:    "refresh-dev",
		userID:      u.ID,
		lastFrame:   frameAt,
		lastMessage: msgAt,
		screenOn:    true,
		permissions: map[string]bool{"accessibility": true},
	}
	hub.mu.Unlock()

	conn, cleanup := newDashWSTestConn(t, token)
	defer cleanup()

	writeWSMessage(t, conn, WSMessage{Type: "refresh"})
	msg := readWSMessage(t, conn)
	if msg.Type != "device_list" || len(msg.Devices) != 1 {
		t.Fatalf("expected refreshed device_list with 1 device, got type=%s len=%d", msg.Type, len(msg.Devices))
	}
	dev := msg.Devices[0]
	if !dev.ScreenOn || !dev.Permissions["accessibility"] {
		t.Fatalf("expected refresh to merge runtime state, got %+v", dev)
	}
	if !dev.LastFrame.Equal(frameAt) || !dev.LastMessage.Equal(msgAt) {
		t.Fatalf("expected merged timestamps, got lastFrame=%v lastMessage=%v", dev.LastFrame, dev.LastMessage)
	}
}

func TestDashAdminWatchOneForbiddenForOtherUsersDevice(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "admin-watch-owner@nftouch.local", "test123456", "user")
	admin := createTestUser(t, "admin-watch@nftouch.local", "test123456", "admin")
	createTestDeviceInDB(t, "admin-watch-dev", owner.ID)
	token, _ := generateJWT(admin)

	conn, cleanup := newDashWSTestConn(t, token)
	defer cleanup()

	writeWSMessage(t, conn, WSMessage{Type: "watch_one", DeviceID: "admin-watch-dev"})
	msg := readWSMessage(t, conn)
	if msg.Type != "error" || msg.Reason != "access denied" {
		t.Fatalf("expected access denied error, got type=%s reason=%s", msg.Type, msg.Reason)
	}
}

func TestEnqueueDeviceListSnapshotReportsLoadError(t *testing.T) {
	dc := &dashConn{
		userID:  "user-1",
		textCh:  make(chan dashTextMessage, 1),
		frameCh: make(chan dashFrameMessage, 1),
		done:    make(chan struct{}),
	}

	err := enqueueDeviceListSnapshot(dc, func() ([]Device, error) {
		return nil, errors.New("database unavailable")
	})
	if err == nil {
		t.Fatal("expected load error to be returned")
	}

	select {
	case msg := <-dc.textCh:
		if msg.typ != websocket.MessageText {
			t.Fatalf("expected text error message, got type=%v", msg.typ)
		}
		var wsMsg WSMessage
		if err := json.Unmarshal(msg.data, &wsMsg); err != nil {
			t.Fatalf("unmarshal queued error message: %v", err)
		}
		if wsMsg.Type != "error" || wsMsg.Reason != "device list unavailable" {
			t.Fatalf("unexpected queued message: %+v", wsMsg)
		}
	default:
		t.Fatal("expected error message to be queued")
	}
}
