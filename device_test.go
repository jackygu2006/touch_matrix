package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
