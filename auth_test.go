package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestLoginSuccess(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	createTestUser(t, "test@nftouch.local", "test123456", "user")

	w := httptest.NewRecorder()
	r := newJSONRequest("POST", "/api/auth/login", map[string]string{
		"email": "test@nftouch.local", "password": "test123456",
	})
	handleAuthLogin(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["token"] == nil || resp["token"] == "" {
		t.Fatal("token should not be empty")
	}
	if resp["role"] != "user" {
		t.Fatalf("expected role=user, got %v", resp["role"])
	}
}

func TestLoginWrongPassword(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	createTestUser(t, "test@nftouch.local", "test123456", "user")

	w := httptest.NewRecorder()
	r := newJSONRequest("POST", "/api/auth/login", map[string]string{
		"email": "test@nftouch.local", "password": "wrongpass",
	})
	handleAuthLogin(w, r)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLoginDisabledUser(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "disabled@nftouch.local", "test123456", "user")
	db.Exec("UPDATE users SET status='disabled' WHERE id=?", u.ID)

	w := httptest.NewRecorder()
	r := newJSONRequest("POST", "/api/auth/login", map[string]string{
		"email": "disabled@nftouch.local", "password": "test123456",
	})
	handleAuthLogin(w, r)

	if w.Code != 401 {
		t.Fatalf("expected 401 for disabled user, got %d", w.Code)
	}
}

func TestLoginNonexistentUser(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	w := httptest.NewRecorder()
	r := newJSONRequest("POST", "/api/auth/login", map[string]string{
		"email": "nobody@nftouch.local", "password": "whatever",
	})
	handleAuthLogin(w, r)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJWTGenerationAndValidation(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "jwt@nftouch.local", "test123456", "user")

	token, err := generateJWT(u)
	if err != nil {
		t.Fatalf("generateJWT: %v", err)
	}

	claims, err := validateJWT(token)
	if err != nil {
		t.Fatalf("validateJWT: %v", err)
	}
	if claims.UserID != u.ID {
		t.Fatalf("expected userID=%s, got %s", u.ID, claims.UserID)
	}
	if claims.Email != u.Email {
		t.Fatalf("expected email=%s, got %s", u.Email, claims.Email)
	}
}

func TestJWTExpiredToken(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "expired@nftouch.local", "test123456", "user")

	claims := JWTClaims{
		UserID: u.ID, Email: u.Email, Role: u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	expiredToken, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)

	_, err := validateJWT(expiredToken)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestJWTInvalidSignature(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "sig@nftouch.local", "test123456", "user")
	claims := JWTClaims{
		UserID: u.ID, Email: u.Email, Role: u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}
	wrongKey := []byte("wrong-secret-key-1234567890!")
	wrongToken, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(wrongKey)

	_, err := validateJWT(wrongToken)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestGetUserFromRequest(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "req@nftouch.local", "test123456", "user")
	token, _ := generateJWT(u)

	r := httptest.NewRequest("GET", "/", nil)
	setAuth(r, token)
	got := getUserFromRequest(r)
	if got == nil {
		t.Fatal("getUserFromRequest returned nil")
	}
	if got.ID != u.ID {
		t.Fatalf("expected id=%s, got %s", u.ID, got.ID)
	}
}

func TestGetUserFromRequestNoAuth(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	r := httptest.NewRequest("GET", "/", nil)
	got := getUserFromRequest(r)
	if got != nil {
		t.Fatal("expected nil for request without auth header")
	}
}

func TestJWTAuthMiddleware(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "middleware@nftouch.local", "test123456", "user")
	token, _ := generateJWT(u)

	handler := jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	t.Run("valid token", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		setAuth(r, token)
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 200 {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("no token", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 401 {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("disabled user", func(t *testing.T) {
		db.Exec("UPDATE users SET status='disabled' WHERE id=?", u.ID)
		r := httptest.NewRequest("GET", "/", nil)
		setAuth(r, token)
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 401 {
			t.Fatalf("expected 401 for disabled user, got %d", w.Code)
		}
	})
}

func TestAdminOnlyMiddleware(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	admin := createTestUser(t, "admin@nftouch.local", "admin123", "admin")
	user := createTestUser(t, "normal@nftouch.local", "user123", "user")

	handler := jwtAuth(adminOnly(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))

	t.Run("admin access", func(t *testing.T) {
		token, _ := generateJWT(admin)
		r := httptest.NewRequest("GET", "/", nil)
		setAuth(r, token)
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 200 {
			t.Fatalf("admin should get 200, got %d", w.Code)
		}
	})

	t.Run("normal user denied", func(t *testing.T) {
		token, _ := generateJWT(user)
		r := httptest.NewRequest("GET", "/", nil)
		setAuth(r, token)
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 403 {
			t.Fatalf("normal user should get 403, got %d", w.Code)
		}
	})
}

func TestAuthCheckNew(t *testing.T) {
	setupTest(t)
	defer teardownTest()

	u := createTestUser(t, "check@nftouch.local", "test123456", "user")
	token, _ := generateJWT(u)

	t.Run("authenticated", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/auth/check", nil)
		setAuth(r, token)
		w := httptest.NewRecorder()
		handleAuthCheckNew(w, r)
		if w.Code != 200 {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/auth/check", nil)
		w := httptest.NewRecorder()
		handleAuthCheckNew(w, r)
		if w.Code != 401 {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})
}
