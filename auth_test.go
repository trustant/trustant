package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPasswordHash(password string) (passwordHash, string) {
	salt := []byte("0123456789abcdef")
	digest := derivePBKDF2([]byte(password), salt, 100000, 32, sha256.New)
	encoded := fmt.Sprintf("pbkdf2-sha256$100000$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest))
	hash, err := parsePasswordHash(encoded)
	if err != nil {
		panic(err)
	}
	return hash, encoded
}

func TestPBKDF2SHA256KnownVector(t *testing.T) {
	got := derivePBKDF2([]byte("password"), []byte("salt"), 1, 32, sha256.New)
	want, err := base64.RawStdEncoding.DecodeString("Eg+2z/z4syxD5yJSVsT4N6hlSMkszDVICAWYfLcL4Xs")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("PBKDF2 vector = %x, want %x", got, want)
	}
}

func testAuthManager() *authManager {
	hash, _ := testPasswordHash("correct horse")
	return newLocalAuthManager("admin", hash, []byte("0123456789abcdef0123456789abcdef"))
}

func authTestHandler(a *authManager) http.Handler {
	mux := http.NewServeMux()
	a.registerRoutes(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("page")) })
	mux.HandleFunc("/api/read", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("read")) })
	mux.HandleFunc("/api/write", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("write")) })
	mux.HandleFunc("/api/launch/demo", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("launch")) })
	return a.middleware(mux)
}

func authRequest(method, target string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Host = "trustable.example.test"
	req.Header.Set("Origin", "http://trustable.example.test")
	req.RemoteAddr = "192.0.2.10:1234"
	return req
}

func loginForTest(t *testing.T, handler http.Handler, username, password string) (*http.Cookie, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := authRequest(http.MethodPost, "http://trustable.example.test/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want 1", len(cookies))
	}
	var response struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode login: %s", err)
	}
	return cookies[0], response.CSRF
}

func TestAuthDisabledPreservesExistingHandler(t *testing.T) {
	a := &authManager{enabled: false}
	handler := a.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authRequest(http.MethodPost, "http://trustable.example.test/api/write", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("disabled status = %d, want %d", rec.Code, http.StatusTeapot)
	}
}

func TestAuthDefaultsToDisabled(t *testing.T) {
	t.Setenv(authModeEnv, "")
	a, err := newAuthManagerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if a.enabled {
		t.Fatal("authentication should be disabled by default")
	}
}

func TestLocalAuthConfigurationFromFiles(t *testing.T) {
	_, encodedHash := testPasswordHash("secret")
	dir := t.TempDir()
	files := map[string]string{
		"username": "file-admin\n",
		"hash":     encodedHash + "\n",
		"key":      "0123456789abcdef0123456789abcdef\n",
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(authModeEnv, "local")
	t.Setenv(authUsernameEnv, "ignored")
	t.Setenv(authUsernameEnv+"_FILE", filepath.Join(dir, "username"))
	t.Setenv(authPasswordHashEnv+"_FILE", filepath.Join(dir, "hash"))
	t.Setenv(authSessionKeyEnv+"_FILE", filepath.Join(dir, "key"))
	a, err := newAuthManagerFromEnv()
	if err != nil {
		t.Fatalf("new auth: %s", err)
	}
	if !a.enabled || a.username != "file-admin" || !a.passwordHash.matches("secret") {
		t.Fatalf("unexpected file configuration: enabled=%v username=%q", a.enabled, a.username)
	}
}

func TestLocalAuthConfigurationRejectsInvalidSecrets(t *testing.T) {
	t.Setenv(authModeEnv, "local")
	t.Setenv(authUsernameEnv, "admin")
	t.Setenv(authPasswordHashEnv, "plaintext")
	t.Setenv(authSessionKeyEnv, strings.Repeat("k", 32))
	if _, err := newAuthManagerFromEnv(); err == nil || !strings.Contains(err.Error(), "pbkdf2-sha256") {
		t.Fatalf("invalid hash error = %v", err)
	}
}

func TestLocalAuthLoginSessionCSRFAndLogout(t *testing.T) {
	a := testAuthManager()
	handler := authTestHandler(a)

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, authRequest(http.MethodGet, "http://trustable.example.test/applist.html?view=list", nil))
	if page.Code != http.StatusSeeOther || !strings.HasPrefix(page.Header().Get("Location"), "/login.html?next=") {
		t.Fatalf("page response = %d location=%q", page.Code, page.Header().Get("Location"))
	}
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, authRequest(http.MethodGet, "http://trustable.example.test/api/read", nil))
	if api.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API status = %d", api.Code)
	}

	cookie, csrf := loginForTest(t, handler, "admin", "correct horse")
	if cookie.Name != authSessionCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" || cookie.Secure {
		t.Fatalf("unexpected HTTP cookie: %#v", cookie)
	}

	readReq := authRequest(http.MethodGet, "http://trustable.example.test/api/read", nil)
	readReq.AddCookie(cookie)
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, readReq)
	if read.Code != http.StatusOK || read.Body.String() != "read" {
		t.Fatalf("authenticated read = %d %q", read.Code, read.Body.String())
	}

	writeReq := authRequest(http.MethodPost, "http://trustable.example.test/api/write", nil)
	writeReq.AddCookie(cookie)
	write := httptest.NewRecorder()
	handler.ServeHTTP(write, writeReq)
	if write.Code != http.StatusForbidden {
		t.Fatalf("write without CSRF = %d", write.Code)
	}
	writeReq = authRequest(http.MethodPost, "http://trustable.example.test/api/write", nil)
	writeReq.AddCookie(cookie)
	writeReq.Header.Set(authCSRFHeader, csrf)
	write = httptest.NewRecorder()
	handler.ServeHTTP(write, writeReq)
	if write.Code != http.StatusOK {
		t.Fatalf("write with CSRF = %d, body=%s", write.Code, write.Body.String())
	}

	launchReq := authRequest(http.MethodGet, "http://trustable.example.test/api/launch/demo", nil)
	launchReq.AddCookie(cookie)
	launch := httptest.NewRecorder()
	handler.ServeHTTP(launch, launchReq)
	if launch.Code != http.StatusForbidden {
		t.Fatalf("effectful GET without CSRF = %d", launch.Code)
	}
	launchReq = authRequest(http.MethodGet, "http://trustable.example.test/api/launch/demo", nil)
	launchReq.AddCookie(cookie)
	launchReq.Header.Set(authCSRFHeader, csrf)
	launch = httptest.NewRecorder()
	handler.ServeHTTP(launch, launchReq)
	if launch.Code != http.StatusOK {
		t.Fatalf("effectful GET with CSRF = %d", launch.Code)
	}

	logoutReq := authRequest(http.MethodPost, "http://trustable.example.test/api/auth/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutReq.Header.Set(authCSRFHeader, csrf)
	logout := httptest.NewRecorder()
	handler.ServeHTTP(logout, logoutReq)
	if logout.Code != http.StatusNoContent || len(logout.Result().Cookies()) != 2 {
		t.Fatalf("logout = %d cookies=%d", logout.Code, len(logout.Result().Cookies()))
	}
	readReq = authRequest(http.MethodGet, "http://trustable.example.test/api/read", nil)
	readReq.AddCookie(cookie)
	read = httptest.NewRecorder()
	handler.ServeHTTP(read, readReq)
	if read.Code != http.StatusUnauthorized {
		t.Fatalf("reused logged-out cookie = %d", read.Code)
	}
}

func TestLocalAuthHTTPSCookie(t *testing.T) {
	a := testAuthManager()
	handler := authTestHandler(a)
	body := []byte(`{"username":"admin","password":"correct horse"}`)
	req := authRequest(http.MethodPost, "http://trustable.example.test/api/auth/login", body)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://trustable.example.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTPS login = %d, body=%s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != authSecureSessionCookie || !cookie.Secure || cookie.Domain != "" {
		t.Fatalf("unexpected HTTPS cookie: %#v", cookie)
	}
}

func TestLocalAuthSessionsExpireAndAreBounded(t *testing.T) {
	a := testAuthManager()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	a.ttl = time.Minute
	a.maxSessions = 2

	first, firstToken, err := a.createSession()
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	_, _, _ = a.createSession()
	now = now.Add(time.Second)
	_, _, _ = a.createSession()
	if len(a.sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(a.sessions))
	}
	if _, exists := a.sessions[first.ID]; exists {
		t.Fatal("oldest session was not evicted")
	}

	req := authRequest(http.MethodGet, "http://trustable.example.test/api/read", nil)
	req.AddCookie(&http.Cookie{Name: authSessionCookie, Value: firstToken})
	if session := a.requestSession(req); session != nil {
		t.Fatal("evicted session is still accepted")
	}

	latest, latestToken, _ := a.createSession()
	now = latest.ExpiresAt.Add(time.Second)
	req = authRequest(http.MethodGet, "http://trustable.example.test/api/read", nil)
	req.AddCookie(&http.Cookie{Name: authSessionCookie, Value: latestToken})
	if session := a.requestSession(req); session != nil {
		t.Fatal("expired session is still accepted")
	}
}

func TestLocalAuthLoginRateLimit(t *testing.T) {
	a := testAuthManager()
	handler := authTestHandler(a)
	for attempt := 0; attempt < defaultLoginMaxFails; attempt++ {
		body := []byte(`{"username":"admin","password":"wrong"}`)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authRequest(http.MethodPost, "http://trustable.example.test/api/auth/login", body))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed login %d status = %d", attempt+1, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authRequest(http.MethodPost, "http://trustable.example.test/api/auth/login", []byte(`{"username":"admin","password":"correct horse"}`)))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("rate-limited login = %d retry=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestEffectfulAuthRoutes(t *testing.T) {
	for _, path := range []string{"/api/launch/demo", "/api/configure", "/api/ollama-connect", "/api/redeploy"} {
		req := authRequest(http.MethodGet, "http://trustable.example.test"+path, nil)
		if !effectfulAuthRequest(req) {
			t.Errorf("GET %s should require CSRF", path)
		}
	}
	if effectfulAuthRequest(authRequest(http.MethodGet, "http://trustable.example.test/api/configuration", nil)) {
		t.Fatal("read-only configuration GET unexpectedly requires CSRF")
	}
}
