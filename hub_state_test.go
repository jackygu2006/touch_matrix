package main

import (
	"fmt"
	"testing"
	"time"
)

func TestBroadcastDeviceListUsesLastFrameOnlyForFrameFreshness(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "state@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-state", u.ID)

	frameAt := time.Now().Add(-10 * time.Second).UTC().Truncate(time.Second)
	msgAt := time.Now().UTC().Truncate(time.Second)

	hub.mu.Lock()
	hub.devices["dev-state"] = &deviceConn{
		deviceID:    "dev-state",
		userID:      u.ID,
		lastFrame:   frameAt,
		lastMessage: msgAt,
		screenOn:    true,
	}
	hub.mu.Unlock()

	devices, err := getDevices()
	if err != nil {
		t.Fatalf("getDevices: %v", err)
	}
	for i := range devices {
		if dc, ok := hub.devices[devices[i].ID]; ok {
			devices[i].LastFrame = dc.lastFrame
			devices[i].LastMessage = dc.lastMessage
		}
	}

	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	if !devices[0].LastFrame.Equal(frameAt) {
		t.Fatalf("expected last_frame=%v, got %v", frameAt, devices[0].LastFrame)
	}
	if !devices[0].LastMessage.Equal(msgAt) {
		t.Fatalf("expected last_message_at=%v, got %v", msgAt, devices[0].LastMessage)
	}
}

func TestBumpDeviceVersion(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	if hub.deviceVersion != 0 {
		t.Fatalf("expected initial version 0, got %d", hub.deviceVersion)
	}
	hub.bumpDeviceVersion()
	if hub.deviceVersion != 1 {
		t.Fatalf("expected version 1, got %d", hub.deviceVersion)
	}
	hub.bumpDeviceVersion()
	if hub.deviceVersion != 2 {
		t.Fatalf("expected version 2, got %d", hub.deviceVersion)
	}
}

func TestRegisterDeviceBumpsVersion(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "version@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-version", u.ID)

	dc := &deviceConn{
		deviceID:    "dev-version",
		userID:      u.ID,
		lastMessage: time.Now(),
	}
	hub.registerDevice("dev-version", dc)

	if hub.deviceVersion != 1 {
		t.Fatalf("expected version to bump to 1, got %d", hub.deviceVersion)
	}
}

func TestDashConnFrameQueueKeepsLatest(t *testing.T) {
	dc := &dashConn{
		frameCh: make(chan dashFrameMessage, 1),
		done:    make(chan struct{}),
	}

	if err := dc.enqueueFrame([]byte("old-header"), []byte("old-frame")); err != nil {
		t.Fatalf("enqueue old frame: %v", err)
	}
	if err := dc.enqueueFrame([]byte("new-header"), []byte("new-frame")); err != nil {
		t.Fatalf("enqueue new frame: %v", err)
	}

	select {
	case frame := <-dc.frameCh:
		if string(frame.header) != "new-header" || string(frame.data) != "new-frame" {
			t.Fatalf("expected latest frame to remain in queue, got header=%q data=%q", string(frame.header), string(frame.data))
		}
	default:
		t.Fatal("expected one frame in queue")
	}
}

func TestDashConnWatchStateAccessors(t *testing.T) {
	dc := &dashConn{}

	dc.setWatchState("dev-one", false)
	deviceID, watchAll := dc.getWatchState()
	if deviceID != "dev-one" || watchAll {
		t.Fatalf("unexpected watch state: deviceID=%q watchAll=%v", deviceID, watchAll)
	}

	dc.setWatchState("", true)
	deviceID, watchAll = dc.getWatchState()
	if deviceID != "" || !watchAll {
		t.Fatalf("unexpected watch-all state: deviceID=%q watchAll=%v", deviceID, watchAll)
	}

	dc.setLastVersion(42)
	if got := dc.getLastVersion(); got != 42 {
		t.Fatalf("expected lastVersion=42, got %d", got)
	}
}

func TestMergeRuntimeDeviceStateIncludesPermissionsAndScreen(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "merge-runtime@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-runtime", u.ID)

	frameAt := time.Now().Add(-5 * time.Second).UTC().Truncate(time.Second)
	msgAt := time.Now().UTC().Truncate(time.Second)
	perms := map[string]bool{"accessibility": true, "overlay": true}

	hub.mu.Lock()
	hub.devices["dev-runtime"] = &deviceConn{
		deviceID:    "dev-runtime",
		userID:      u.ID,
		lastFrame:   frameAt,
		lastMessage: msgAt,
		permissions: perms,
		screenOn:    true,
	}
	hub.mu.Unlock()

	devices, err := getDevices()
	if err != nil {
		t.Fatalf("getDevices: %v", err)
	}
	devices = mergeRuntimeDeviceState(devices)

	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	if !devices[0].Permissions["accessibility"] || !devices[0].Permissions["overlay"] {
		t.Fatalf("expected runtime permissions to be merged, got %#v", devices[0].Permissions)
	}
	if !devices[0].ScreenOn {
		t.Fatalf("expected screenOn=true after merge")
	}
	if !devices[0].LastFrame.Equal(frameAt) {
		t.Fatalf("expected lastFrame=%v, got %v", frameAt, devices[0].LastFrame)
	}
	if !devices[0].LastMessage.Equal(msgAt) {
		t.Fatalf("expected lastMessage=%v, got %v", msgAt, devices[0].LastMessage)
	}
}

func TestBroadcastFrameWatchAllDoesNotLeakAcrossUsers(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "frame-owner@nftouch.local", "test123456", "user")
	admin := createTestUser(t, "frame-admin@nftouch.local", "test123456", "admin")
	createTestDeviceInDB(t, "frame-dev", owner.ID)

	hub.mu.Lock()
	hub.devices["frame-dev"] = &deviceConn{deviceID: "frame-dev", userID: owner.ID}
	dc := &dashConn{
		userID:   admin.ID,
		role:     admin.Role,
		frameCh:  make(chan dashFrameMessage, 1),
		textCh:   make(chan dashTextMessage, 1),
		done:     make(chan struct{}),
		watchAll: true,
	}
	hub.dash[dc] = true
	hub.mu.Unlock()

	hub.broadcastFrame("frame-dev", []byte("frame-data"))

	select {
	case <-dc.frameCh:
		t.Fatal("watch_all should not receive frames from other users' devices")
	default:
	}
}

func TestLoadDevicesSnapshotFallsBackToCachedSnapshotOnLockError(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	frameAt := time.Now().Add(-2 * time.Second).UTC().Truncate(time.Second)
	msgAt := time.Now().UTC().Truncate(time.Second)
	hub.setDeviceCache([]Device{{
		ID:          "cached-dev",
		UserID:      "user-1",
		Name:        "cached-dev",
		Status:      "online",
		LastFrame:   frameAt,
		LastMessage: msgAt,
		Permissions: map[string]bool{"accessibility": true},
		ScreenOn:    true,
	}})

	devices, err := loadDevicesSnapshot(func() ([]Device, error) {
		return nil, fmt.Errorf("database is locked (5) (SQLITE_BUSY)")
	})
	if err != nil {
		t.Fatalf("expected cached fallback, got error: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != "cached-dev" {
		t.Fatalf("expected cached snapshot, got %+v", devices)
	}
	if !devices[0].Permissions["accessibility"] || !devices[0].ScreenOn {
		t.Fatalf("expected cached runtime fields to survive fallback, got %+v", devices[0])
	}
}

func TestShouldCloseStaleDeviceUsesWatchdogTimeout(t *testing.T) {
	now := time.Now()
	if shouldCloseStaleDevice(now.Add(-deviceReadTimeout), now) {
		t.Fatalf("device should not be closed at read timeout boundary")
	}
	if !shouldCloseStaleDevice(now.Add(-deviceWatchdogTimeout-time.Second), now) {
		t.Fatalf("device should be closed after watchdog timeout")
	}
	if deviceWatchdogTimeout <= deviceReadTimeout {
		t.Fatalf("watchdog timeout must exceed read timeout: watchdog=%v read=%v", deviceWatchdogTimeout, deviceReadTimeout)
	}
}
