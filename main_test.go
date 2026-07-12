package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setupTest(t *testing.T) {
	t.Helper()
	if err := initDB(":memory:"); err != nil {
		t.Fatalf("initDB: %v", err)
	}
	jwtSecret = []byte("test-secret-key-32bytes-long!")
	hub = &Hub{devices: make(map[string]*deviceConn), dash: make(map[*dashConn]bool)}
}

func teardownTest() {
	if db != nil {
		db.Close()
		db = nil
	}
}

func createTestUser(t *testing.T, email, password, role string) *User {
	t.Helper()
	name := email
	if idx := strings.Index(email, "@"); idx > 0 {
		name = email[:idx]
	}
	id, err := createUserDB(email, password, name, role, "", 5)
	if err != nil {
		t.Fatalf("createUserDB(%s): %v", email, err)
	}
	u, err := getUserByID(id)
	if err != nil {
		t.Fatalf("getUserByID: %v", err)
	}
	return u
}

func loginAndGetToken(t *testing.T, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAuthLogin(w, req)
	if w.Code != 200 {
		t.Fatalf("login failed: status=%d", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	return resp["token"].(string)
}

func newJSONRequest(method, path string, body interface{}) *http.Request {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func newGetRequest(path string) *http.Request {
	return httptest.NewRequest("GET", path, nil)
}

func setAuth(r *http.Request, token string) {
	r.Header.Set("Authorization", "Bearer "+token)
}

func createTestDeviceInDB(t *testing.T, deviceID, userID string) {
	t.Helper()
	d := Device{ID: deviceID, Name: deviceID, UserID: userID, Status: "online"}
	if err := upsertDevice(d); err != nil {
		t.Fatalf("upsertDevice: %v", err)
	}
}

func addDeviceToHub(deviceID string) {
	hub.mu.Lock()
	hub.devices[deviceID] = &deviceConn{deviceID: deviceID, lastFrame: time.Now()}
	hub.mu.Unlock()
}

func setPathValue(r *http.Request, key, value string) {
	r.SetPathValue(key, value)
}
