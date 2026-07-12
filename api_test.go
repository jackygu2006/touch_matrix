package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestHandleProfile(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "profile@nftouch.local", "test123456", "user")
	token, _ := generateJWT(u)

	r := httptest.NewRequest("GET", "/api/profile", nil)
	setAuth(r, token)
	w := httptest.NewRecorder()
	handleProfile(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp User
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.ID != u.ID {
		t.Fatalf("expected id=%s, got %s", u.ID, resp.ID)
	}
	if resp.Email != u.Email {
		t.Fatalf("expected email=%s, got %s", u.Email, resp.Email)
	}
}

func TestHandleProfileUnauthorized(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	r := httptest.NewRequest("GET", "/api/profile", nil)
	w := httptest.NewRecorder()
	handleProfile(w, r)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	r := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handleHealth(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Fatalf("expected status=ok, got %s", resp["status"])
	}
}
