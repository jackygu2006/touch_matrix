package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestAdminCreateUser(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	w := httptest.NewRecorder()
	r := newJSONRequest("POST", "/api/admin/users", map[string]interface{}{
		"email":       "newuser@nftouch.local",
		"password":    "newpass123",
		"nickname":    "New User",
		"max_devices": 3,
	})
	handleAdminUsers(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	u, err := getUserByEmail("newuser@nftouch.local")
	if err != nil {
		t.Fatalf("getUserByEmail: %v", err)
	}
	if u == nil {
		t.Fatal("user should be created")
	}
	if u.Role != "user" {
		t.Fatalf("expected role=user, got %s", u.Role)
	}
	if u.MaxDevices != 3 {
		t.Fatalf("expected max_devices=3, got %d", u.MaxDevices)
	}
}

func TestAdminCreateUserMissingFields(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{"no email", map[string]interface{}{"password": "pass123"}},
		{"no password", map[string]interface{}{"email": "test@nftouch.local"}},
		{"empty body", map[string]interface{}{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := newJSONRequest("POST", "/api/admin/users", tt.body)
			handleAdminUsers(w, r)
			if w.Code != 400 {
				t.Fatalf("expected 400, got %d", w.Code)
			}
		})
	}
}

func TestAdminCreateUserPasswordTooShort(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	w := httptest.NewRecorder()
	r := newJSONRequest("POST", "/api/admin/users", map[string]interface{}{
		"email":    "shortpw@nftouch.local",
		"password": "12345",
	})
	handleAdminUsers(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400 for short password, got %d", w.Code)
	}
}

func TestAdminCreateUserDefaultMaxDevices(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	w := httptest.NewRecorder()
	r := newJSONRequest("POST", "/api/admin/users", map[string]interface{}{
		"email":    "default@nftouch.local",
		"password": "pass123456",
	})
	handleAdminUsers(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	u, _ := getUserByEmail("default@nftouch.local")
	if u.MaxDevices != 5 {
		t.Fatalf("expected default max_devices=5, got %d", u.MaxDevices)
	}
}

func TestAdminCreateUserDuplicateEmail(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	createTestUser(t, "dup@nftouch.local", "first123", "user")

	w := httptest.NewRecorder()
	r := newJSONRequest("POST", "/api/admin/users", map[string]interface{}{
		"email":    "dup@nftouch.local",
		"password": "second456",
	})
	handleAdminUsers(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400 for duplicate email, got %d", w.Code)
	}
}

func TestAdminListUsers(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	createTestUser(t, "u1@nftouch.local", "pass123456", "user")
	createTestUser(t, "u2@nftouch.local", "pass123456", "user")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/admin/users", nil)
	handleAdminUsers(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var users []User
	json.NewDecoder(w.Body).Decode(&users)
	if len(users) < 2 {
		t.Fatalf("expected at least 2 users, got %d", len(users))
	}
}

func TestAdminUpdateUser(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "update@nftouch.local", "oldpass123", "user")

	w := httptest.NewRecorder()
	r := newJSONRequest("PUT", "/api/admin/users/"+u.ID, map[string]interface{}{
		"nickname":    "Updated Name",
		"max_devices": 10,
	})
	setPathValue(r, "id", u.ID)
	handleAdminUser(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, _ := getUserByID(u.ID)
	if updated.Nickname != "Updated Name" {
		t.Fatalf("expected nickname=Updated Name, got %s", updated.Nickname)
	}
	if updated.MaxDevices != 10 {
		t.Fatalf("expected max_devices=10, got %d", updated.MaxDevices)
	}
}

func TestAdminDeleteUser(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "todelete@nftouch.local", "pass123456", "user")
	createTestDeviceInDB(t, "user-dev-del", u.ID)
	addDeviceToHub("user-dev-del", u.ID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/admin/users/"+u.ID, nil)
	setPathValue(r, "id", u.ID)
	handleAdminUser(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	got, _ := getUserByID(u.ID)
	if got != nil {
		t.Fatal("user should be deleted")
	}
	if _, ok := hub.devices["user-dev-del"]; ok {
		t.Fatal("user device should be dropped from hub after user deletion")
	}
}

func TestAdminToggleUserStatus(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "toggle@nftouch.local", "pass123456", "user")
	createTestDeviceInDB(t, "user-dev-toggle", u.ID)
	addDeviceToHub("user-dev-toggle", u.ID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/api/admin/users/"+u.ID+"/toggle", nil)
	setPathValue(r, "id", u.ID)
	handleAdminUserToggle(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	got, _ := getUserByID(u.ID)
	if got.Status != "disabled" {
		t.Fatalf("expected disabled, got %s", got.Status)
	}
	if _, ok := hub.devices["user-dev-toggle"]; ok {
		t.Fatal("user device should be dropped from hub when user is disabled")
	}

	// Toggle back
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("PUT", "/api/admin/users/"+u.ID+"/toggle", nil)
	setPathValue(r2, "id", u.ID)
	handleAdminUserToggle(w2, r2)

	got2, _ := getUserByID(u.ID)
	if got2.Status != "active" {
		t.Fatalf("expected active, got %s", got2.Status)
	}
}

func TestAdminCannotToggleSelf(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	admin := createTestUser(t, "selftoggle@nftouch.local", "admin123", "admin")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/api/admin/users/"+admin.ID+"/toggle", nil)
	setPathValue(r, "id", admin.ID)
	handleAdminUserToggle(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400 for toggling admin, got %d", w.Code)
	}
}
