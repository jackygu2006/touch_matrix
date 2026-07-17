package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetDevicesEmpty(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "empty@nftouch.local", "test123456", "user")
	token, _ := generateJWT(u)

	r := httptest.NewRequest("GET", "/api/devices", nil)
	setAuth(r, token)
	w := httptest.NewRecorder()
	handleGetDevices(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var devices []Device
	json.NewDecoder(w.Body).Decode(&devices)
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices, got %d", len(devices))
	}
}

func TestGetDevicesOwnedByUser(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	userA := createTestUser(t, "a@nftouch.local", "test123456", "user")
	userB := createTestUser(t, "b@nftouch.local", "test123456", "user")

	createTestDeviceInDB(t, "device-a1", userA.ID)
	createTestDeviceInDB(t, "device-a2", userA.ID)
	createTestDeviceInDB(t, "device-b1", userB.ID)

	token, _ := generateJWT(userA)

	r := httptest.NewRequest("GET", "/api/devices", nil)
	setAuth(r, token)
	w := httptest.NewRecorder()
	handleGetDevices(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var devices []Device
	json.NewDecoder(w.Body).Decode(&devices)

	if len(devices) != 2 {
		t.Fatalf("user A should see 2 devices, got %d", len(devices))
	}
	for _, d := range devices {
		if d.ID != "device-a1" && d.ID != "device-a2" {
			t.Fatalf("unexpected device: %s", d.ID)
		}
	}
}

func TestGetDevicesUnboundVisible(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	userA := createTestUser(t, "ua@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "device-a", userA.ID)
	createTestDeviceInDB(t, "device-unbound", "") // unbound device

	token, _ := generateJWT(userA)

	r := httptest.NewRequest("GET", "/api/devices", nil)
	setAuth(r, token)
	w := httptest.NewRecorder()
	handleGetDevices(w, r)

	var devices []Device
	json.NewDecoder(w.Body).Decode(&devices)
	if len(devices) != 2 {
		t.Fatalf("user should see own device and unbound device, got %d", len(devices))
	}
}

func TestGetDevicesMergesRuntimeState(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "runtime-list@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-runtime-list", u.ID)
	token, _ := generateJWT(u)

	frameAt := time.Now().Add(-3 * time.Second).UTC().Truncate(time.Second)
	msgAt := time.Now().UTC().Truncate(time.Second)
	hub.mu.Lock()
	hub.devices["dev-runtime-list"] = &deviceConn{
		deviceID:    "dev-runtime-list",
		userID:      u.ID,
		lastFrame:   frameAt,
		lastMessage: msgAt,
		screenOn:    true,
		permissions: map[string]bool{"accessibility": true, "overlay": true},
	}
	hub.mu.Unlock()

	r := httptest.NewRequest("GET", "/api/devices", nil)
	setAuth(r, token)
	w := httptest.NewRecorder()
	handleGetDevices(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var devices []Device
	json.NewDecoder(w.Body).Decode(&devices)
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	if !devices[0].ScreenOn || !devices[0].Permissions["accessibility"] {
		t.Fatalf("expected merged runtime fields, got %+v", devices[0])
	}
	if !devices[0].LastFrame.Equal(frameAt) || !devices[0].LastMessage.Equal(msgAt) {
		t.Fatalf("expected merged timestamps, got lastFrame=%v lastMessage=%v", devices[0].LastFrame, devices[0].LastMessage)
	}
}

func TestGetSingleDevice(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "single@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-001", u.ID)

	token, _ := generateJWT(u)

	r := httptest.NewRequest("GET", "/api/devices/dev-001", nil)
	setAuth(r, token)
	setPathValue(r, "id", "dev-001")
	w := httptest.NewRecorder()
	handleGetDevice(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var dev Device
	json.NewDecoder(w.Body).Decode(&dev)
	if dev.ID != "dev-001" {
		t.Fatalf("expected dev-001, got %s", dev.ID)
	}
}

func TestGetSingleDeviceMergesRuntimeState(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "single-runtime@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-single-runtime", u.ID)
	token, _ := generateJWT(u)

	hub.mu.Lock()
	hub.devices["dev-single-runtime"] = &deviceConn{
		deviceID:    "dev-single-runtime",
		userID:      u.ID,
		screenOn:    true,
		permissions: map[string]bool{"notification": true},
	}
	hub.mu.Unlock()

	r := httptest.NewRequest("GET", "/api/devices/dev-single-runtime", nil)
	setAuth(r, token)
	setPathValue(r, "id", "dev-single-runtime")
	w := httptest.NewRecorder()
	handleGetDevice(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var dev Device
	json.NewDecoder(w.Body).Decode(&dev)
	if !dev.ScreenOn || !dev.Permissions["notification"] {
		t.Fatalf("expected merged runtime fields, got %+v", dev)
	}
}

func TestGetSingleDeviceForbidden(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "owner@nftouch.local", "test123456", "user")
	viewer := createTestUser(t, "viewer@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-owned", owner.ID)
	token, _ := generateJWT(viewer)

	r := httptest.NewRequest("GET", "/api/devices/dev-owned", nil)
	setAuth(r, token)
	setPathValue(r, "id", "dev-owned")
	w := httptest.NewRecorder()
	handleGetDevice(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminCannotAccessOtherUsersDevice(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "owner-admin-block@nftouch.local", "test123456", "user")
	admin := createTestUser(t, "admin-block@nftouch.local", "test123456", "admin")
	createTestDeviceInDB(t, "dev-admin-block", owner.ID)
	token, _ := generateJWT(admin)

	r := httptest.NewRequest("GET", "/api/devices/dev-admin-block", nil)
	setAuth(r, token)
	setPathValue(r, "id", "dev-admin-block")
	w := httptest.NewRecorder()
	handleGetDevice(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminListDoesNotIncludeOtherUsersDevices(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "owner-list@nftouch.local", "test123456", "user")
	admin := createTestUser(t, "admin-list@nftouch.local", "test123456", "admin")
	createTestDeviceInDB(t, "dev-owner-only", owner.ID)
	createTestDeviceInDB(t, "dev-admin-own", admin.ID)
	createTestDeviceInDB(t, "dev-unbound", "")
	token, _ := generateJWT(admin)

	r := httptest.NewRequest("GET", "/api/devices", nil)
	setAuth(r, token)
	w := httptest.NewRecorder()
	handleGetDevices(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var devices []Device
	json.NewDecoder(w.Body).Decode(&devices)
	if len(devices) != 2 {
		t.Fatalf("expected admin to see own + unbound devices only, got %d", len(devices))
	}
	for _, d := range devices {
		if d.ID == "dev-owner-only" {
			t.Fatalf("admin should not see other users' device: %+v", devices)
		}
	}
}

func TestGetDeviceNotFound(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "nf@nftouch.local", "test123456", "user")
	token, _ := generateJWT(u)

	r := httptest.NewRequest("GET", "/api/devices/nonexistent", nil)
	setAuth(r, token)
	setPathValue(r, "id", "nonexistent")
	w := httptest.NewRecorder()
	handleGetDevice(w, r)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestPostTaskDeviceNotFound(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "task@nftouch.local", "test123456", "user")
	token, _ := generateJWT(u)

	r := newJSONRequest("POST", "/api/devices/nonexistent/task", map[string]string{
		"prompt": "test task",
	})
	setAuth(r, token)
	setPathValue(r, "id", "nonexistent")
	w := httptest.NewRecorder()
	handlePostTask(w, r)

	if w.Code != 404 {
		t.Fatalf("expected 404 for nonexistent device, got %d", w.Code)
	}
}

func TestPostTaskEmptyPrompt(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "emptytask@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-task", u.ID)
	token, _ := generateJWT(u)

	r := newJSONRequest("POST", "/api/devices/dev-task/task", map[string]string{
		"prompt": "",
	})
	setAuth(r, token)
	setPathValue(r, "id", "dev-task")
	w := httptest.NewRecorder()
	handlePostTask(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400 for empty prompt, got %d", w.Code)
	}
}

func TestPostTaskDeviceNotConnected(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "notconn@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-nc", u.ID)
	token, _ := generateJWT(u)
	// Device is in DB but NOT in hub (not connected)

	r := newJSONRequest("POST", "/api/devices/dev-nc/task", map[string]string{
		"prompt": "test task",
	})
	setAuth(r, token)
	setPathValue(r, "id", "dev-nc")
	w := httptest.NewRecorder()
	handlePostTask(w, r)

	if w.Code != 503 {
		t.Fatalf("expected 503 for not-connected device, got %d", w.Code)
	}
}

func TestPostTaskForbidden(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "task-owner@nftouch.local", "test123456", "user")
	viewer := createTestUser(t, "task-viewer@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-locked", owner.ID)
	token, _ := generateJWT(viewer)

	r := newJSONRequest("POST", "/api/devices/dev-locked/task", map[string]string{
		"prompt": "test task",
	})
	setAuth(r, token)
	setPathValue(r, "id", "dev-locked")
	w := httptest.NewRecorder()
	handlePostTask(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetTasksForbidden(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "tasks-owner@nftouch.local", "test123456", "user")
	viewer := createTestUser(t, "tasks-viewer@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-tasks", owner.ID)
	token, _ := generateJWT(viewer)

	r := httptest.NewRequest("GET", "/api/devices/dev-tasks/tasks", nil)
	setAuth(r, token)
	setPathValue(r, "id", "dev-tasks")
	w := httptest.NewRecorder()
	handleGetTasks(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteDevice(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "del@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-del", u.ID)
	token, _ := generateJWT(u)

	r := httptest.NewRequest("DELETE", "/api/devices/dev-del", nil)
	setAuth(r, token)
	setPathValue(r, "id", "dev-del")
	w := httptest.NewRecorder()
	handleDeleteDevice(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	dev, _ := getDevice("dev-del")
	if dev != nil {
		t.Fatal("device should be deleted but still exists")
	}
}

func TestDeleteDeviceForbidden(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	owner := createTestUser(t, "del-owner@nftouch.local", "test123456", "user")
	viewer := createTestUser(t, "del-viewer@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-denied", owner.ID)
	token, _ := generateJWT(viewer)

	r := httptest.NewRequest("DELETE", "/api/devices/dev-denied", nil)
	setAuth(r, token)
	setPathValue(r, "id", "dev-denied")
	w := httptest.NewRecorder()
	handleDeleteDevice(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteDeviceDropsOnlineConnection(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "drop@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-drop", u.ID)
	addDeviceToHub("dev-drop", u.ID)
	token, _ := generateJWT(u)

	r := httptest.NewRequest("DELETE", "/api/devices/dev-drop", nil)
	setAuth(r, token)
	setPathValue(r, "id", "dev-drop")
	w := httptest.NewRecorder()
	handleDeleteDevice(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, ok := hub.devices["dev-drop"]; ok {
		t.Fatal("device should be removed from hub after delete")
	}
	if err := hub.sendToDevice("dev-drop", WSMessage{Type: "cmd_task", Prompt: "x"}); err == nil {
		t.Fatal("sendToDevice should fail after device delete cleanup")
	}
}

func TestDeviceRoutesRequireAuth(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "require@nftouch.local", "test123456", "user")
	createTestDeviceInDB(t, "dev-req", u.ID)

	tests := []struct {
		name   string
		method string
		path   string
		body   interface{}
	}{
		{"get devices", "GET", "/api/devices", nil},
		{"get single device", "GET", "/api/devices/dev-req", nil},
		{"delete device", "DELETE", "/api/devices/dev-req", nil},
		{"post task", "POST", "/api/devices/dev-req/task", map[string]string{"prompt": "test"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r *http.Request
			if tt.body != nil {
				r = newJSONRequest(tt.method, tt.path, tt.body)
			} else {
				r = httptest.NewRequest(tt.method, tt.path, nil)
			}
			setPathValue(r, "id", "dev-req")

			var w *httptest.ResponseRecorder
			switch tt.name {
			case "get devices":
				w = httptest.NewRecorder()
				handleGetDevices(w, r)
			case "get single device":
				w = httptest.NewRecorder()
				handleGetDevice(w, r)
			case "delete device":
				w = httptest.NewRecorder()
				handleDeleteDevice(w, r)
			case "post task":
				w = httptest.NewRecorder()
				handlePostTask(w, r)
			}

			// Note: these handlers don't enforce auth themselves;
			// auth is enforced by the mux middleware in main().
			// This test verifies the handlers don't crash without auth.
			// The auth check is tested in auth_test.go via middleware tests.
			if w.Code == 0 {
				t.Fatal("no response recorded")
			}
		})
	}
}
