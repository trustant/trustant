package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authModeEnv             = "TRUSTABLE_AUTH_MODE"
	authUsernameEnv         = "TRUSTABLE_AUTH_USERNAME"
	authPasswordHashEnv     = "TRUSTABLE_AUTH_PASSWORD_HASH"
	authSessionKeyEnv       = "TRUSTABLE_AUTH_SESSION_KEY"
	authSessionCookie       = "trustable_session"
	authSecureSessionCookie = "__Host-trustable_session"
	authCSRFHeader          = "X-Trustable-CSRF"
	defaultAuthSessionTTL   = 8 * time.Hour
	defaultAuthMaxSessions  = 256
	defaultLoginMaxEntries  = 512
	defaultLoginMaxFails    = 5
	defaultLoginWindow      = 5 * time.Minute
)

type authContextKey struct{}

type passwordHash struct {
	iterations int
	salt       []byte
	digest     []byte
}

type authSession struct {
	ID        string
	Username  string
	CSRF      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type loginAttempt struct {
	Failures int
	First    time.Time
	Last     time.Time
}

type authManager struct {
	enabled      bool
	username     string
	passwordHash passwordHash
	sessionKey   []byte
	ttl          time.Duration
	maxSessions  int
	now          func() time.Time
	random       io.Reader

	mu       sync.Mutex
	sessions map[string]*authSession
	attempts map[string]*loginAttempt
}

func newAuthManagerFromEnv() (*authManager, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(authModeEnv)))
	if mode == "" || mode == "disabled" {
		return &authManager{enabled: false}, nil
	}
	if mode != "local" {
		return nil, fmt.Errorf("unsupported %s value %q", authModeEnv, mode)
	}

	username, err := explicitSecret(authUsernameEnv)
	if err != nil {
		return nil, err
	}
	hashValue, err := explicitSecret(authPasswordHashEnv)
	if err != nil {
		return nil, err
	}
	sessionKeyValue, err := explicitSecret(authSessionKeyEnv)
	if err != nil {
		return nil, err
	}
	if username == "" {
		return nil, fmt.Errorf("%s or %s_FILE is required in local auth mode", authUsernameEnv, authUsernameEnv)
	}
	parsedHash, err := parsePasswordHash(hashValue)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", authPasswordHashEnv, err)
	}
	sessionKey, err := parseSessionKey(sessionKeyValue)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", authSessionKeyEnv, err)
	}
	return newLocalAuthManager(username, parsedHash, sessionKey), nil
}

func explicitSecret(name string) (string, error) {
	if path := strings.TrimSpace(os.Getenv(name + "_FILE")); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s_FILE %q: %w", name, path, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return strings.TrimSpace(os.Getenv(name)), nil
}

func parseSessionKey(value string) ([]byte, error) {
	if strings.HasPrefix(value, "base64:") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "base64:"))
		if err != nil {
			return nil, errors.New("base64 session key cannot be decoded")
		}
		value = string(decoded)
	}
	key := []byte(value)
	if len(key) < 32 {
		return nil, errors.New("session key must contain at least 32 bytes")
	}
	return append([]byte(nil), key...), nil
}

func parsePasswordHash(value string) (passwordHash, error) {
	parts := strings.Split(value, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return passwordHash{}, errors.New("expected pbkdf2-sha256$iterations$salt$digest")
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 100000 || iterations > 2000000 {
		return passwordHash{}, errors.New("iterations must be an integer between 100000 and 2000000")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < 16 || len(salt) > 1024 {
		return passwordHash{}, errors.New("salt must be base64 without padding and contain between 16 and 1024 bytes")
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(digest) < 32 || len(digest) > 64 {
		return passwordHash{}, errors.New("digest must be base64 without padding and contain between 32 and 64 bytes")
	}
	return passwordHash{iterations: iterations, salt: salt, digest: digest}, nil
}

func derivePBKDF2(password, salt []byte, iterations, size int, newHash func() hash.Hash) []byte {
	hLen := newHash().Size()
	blocks := (size + hLen - 1) / hLen
	derived := make([]byte, 0, blocks*hLen)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(newHash, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(newHash, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		derived = append(derived, t...)
	}
	return derived[:size]
}

func (p passwordHash) matches(password string) bool {
	actual := derivePBKDF2([]byte(password), p.salt, p.iterations, len(p.digest), sha256.New)
	return subtle.ConstantTimeCompare(actual, p.digest) == 1
}

func newLocalAuthManager(username string, hash passwordHash, sessionKey []byte) *authManager {
	return &authManager{
		enabled:      true,
		username:     username,
		passwordHash: hash,
		sessionKey:   append([]byte(nil), sessionKey...),
		ttl:          defaultAuthSessionTTL,
		maxSessions:  defaultAuthMaxSessions,
		now:          time.Now,
		random:       rand.Reader,
		sessions:     make(map[string]*authSession),
		attempts:     make(map[string]*loginAttempt),
	}
}

func (a *authManager) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/login", a.handleLogin)
	mux.HandleFunc("/api/auth/session", a.handleSession)
	mux.HandleFunc("/api/auth/logout", a.handleLogout)
}

func (a *authManager) middleware(next http.Handler) http.Handler {
	if a == nil || !a.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		session := a.requestSession(r)
		if session == nil {
			a.unauthorized(w, r)
			return
		}
		if effectfulAuthRequest(r) && !a.validCSRFRequest(r, session) {
			writeAuthJSON(w, http.StatusForbidden, map[string]any{"error": "invalid CSRF token or request origin"})
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		ctx := context.WithValue(r.Context(), authContextKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func publicAuthPath(path string) bool {
	switch path {
	case "/login.html", "/trustable-ui.css", "/tailwind.js", "/trustable-logo.svg", "/favicon.ico",
		"/api/auth/login", "/api/version", "/api/status":
		return true
	default:
		return false
	}
}

func effectfulAuthRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		return r.URL.Path != "/api/auth/login"
	}
	if r.Method != http.MethodGet {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/launch/") {
		return true
	}
	switch r.URL.Path {
	case "/api/configure", "/api/ollama-connect", "/api/redeploy":
		return true
	default:
		return false
	}
}

func (a *authManager) validCSRFRequest(r *http.Request, session *authSession) bool {
	provided := r.Header.Get(authCSRFHeader)
	return provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(session.CSRF)) == 1 && sameRequestOrigin(r)
}

func sameRequestOrigin(r *http.Request) bool {
	expectedScheme := "http"
	if r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https") {
		expectedScheme = "https"
	}
	expectedHost := strings.ToLower(r.Host)
	check := func(raw string) bool {
		u, err := url.Parse(raw)
		return err == nil && strings.EqualFold(u.Scheme, expectedScheme) && strings.EqualFold(u.Host, expectedHost)
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return check(origin)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return check(referer)
	}
	return false
}

func (a *authManager) unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeAuthJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	next := r.URL.RequestURI()
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.Redirect(w, r, "/login.html?next="+url.QueryEscape(next), http.StatusSeeOther)
}

func (a *authManager) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.enabled {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameRequestOrigin(r) {
		writeAuthJSON(w, http.StatusForbidden, map[string]any{"error": "invalid request origin"})
		return
	}
	client := authClientKey(r)
	if retryAfter, limited := a.loginLimited(client); limited {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		writeAuthJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many login attempts"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	usernameOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(a.username)) == 1
	passwordOK := a.passwordHash.matches(req.Password)
	req.Password = ""
	if !usernameOK || !passwordOK {
		a.recordLoginFailure(client)
		writeAuthJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid username or password"})
		return
	}

	session, token, err := a.createSession()
	if err != nil {
		writeAuthJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to create session"})
		return
	}
	a.clearLoginFailures(client)
	a.setSessionCookie(w, r, token, session.ExpiresAt)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"username": session.Username,
		"expires":  session.ExpiresAt.UTC().Format(time.RFC3339),
		"csrf":     session.CSRF,
	})
}

func (a *authManager) handleSession(w http.ResponseWriter, r *http.Request) {
	if !a.enabled {
		writeAuthJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, _ := r.Context().Value(authContextKey{}).(*authSession)
	if session == nil {
		writeAuthJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"enabled":  true,
		"username": session.Username,
		"expires":  session.ExpiresAt.UTC().Format(time.RFC3339),
		"csrf":     session.CSRF,
	})
}

func (a *authManager) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !a.enabled {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie := sessionCookie(r); cookie != nil {
		if id, ok := a.verifyToken(cookie.Value); ok {
			a.mu.Lock()
			delete(a.sessions, id)
			a.mu.Unlock()
		}
	}
	a.clearSessionCookies(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (a *authManager) createSession() (*authSession, string, error) {
	id, err := randomToken(a.random, 32)
	if err != nil {
		return nil, "", err
	}
	csrf, err := randomToken(a.random, 32)
	if err != nil {
		return nil, "", err
	}
	now := a.now()
	session := &authSession{ID: id, Username: a.username, CSRF: csrf, CreatedAt: now, ExpiresAt: now.Add(a.ttl)}

	a.mu.Lock()
	a.cleanupSessionsLocked(now)
	for len(a.sessions) >= a.maxSessions {
		var oldestID string
		var oldest time.Time
		for candidateID, candidate := range a.sessions {
			if oldestID == "" || candidate.CreatedAt.Before(oldest) {
				oldestID, oldest = candidateID, candidate.CreatedAt
			}
		}
		delete(a.sessions, oldestID)
	}
	a.sessions[id] = session
	a.mu.Unlock()
	return session, a.signToken(id), nil
}

func randomToken(reader io.Reader, size int) (string, error) {
	buf := make([]byte, size)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (a *authManager) signToken(id string) string {
	mac := hmac.New(sha256.New, a.sessionKey)
	mac.Write([]byte(id))
	return id + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *authManager) verifyToken(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, a.sessionKey)
	mac.Write([]byte(parts[0]))
	return parts[0], hmac.Equal(signature, mac.Sum(nil))
}

func (a *authManager) requestSession(r *http.Request) *authSession {
	cookie := sessionCookie(r)
	if cookie == nil {
		return nil
	}
	id, ok := a.verifyToken(cookie.Value)
	if !ok {
		return nil
	}
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.sessions[id]
	if session == nil {
		return nil
	}
	if !session.ExpiresAt.After(now) {
		delete(a.sessions, id)
		return nil
	}
	copy := *session
	return &copy
}

func sessionCookie(r *http.Request) *http.Cookie {
	if cookie, err := r.Cookie(authSecureSessionCookie); err == nil {
		return cookie
	}
	if cookie, err := r.Cookie(authSessionCookie); err == nil {
		return cookie
	}
	return nil
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func (a *authManager) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, expires time.Time) {
	secure := requestIsHTTPS(r)
	name := authSessionCookie
	if secure {
		name = authSecureSessionCookie
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(expires.Sub(a.now()).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *authManager) clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	for _, cookie := range []http.Cookie{
		{Name: authSessionCookie, Path: "/", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, SameSite: http.SameSiteStrictMode},
		{Name: authSecureSessionCookie, Path: "/", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode},
	} {
		http.SetCookie(w, &cookie)
	}
}

func (a *authManager) cleanupSessionsLocked(now time.Time) {
	for id, session := range a.sessions {
		if !session.ExpiresAt.After(now) {
			delete(a.sessions, id)
		}
	}
}

func authClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func (a *authManager) loginLimited(client string) (time.Duration, bool) {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cleanupAttemptsLocked(now)
	attempt := a.attempts[client]
	if attempt == nil || attempt.Failures < defaultLoginMaxFails {
		return 0, false
	}
	remaining := defaultLoginWindow - now.Sub(attempt.First)
	if remaining <= 0 {
		delete(a.attempts, client)
		return 0, false
	}
	return remaining, true
}

func (a *authManager) recordLoginFailure(client string) {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cleanupAttemptsLocked(now)
	if len(a.attempts) >= defaultLoginMaxEntries {
		var oldestClient string
		var oldest time.Time
		for candidate, attempt := range a.attempts {
			if oldestClient == "" || attempt.Last.Before(oldest) {
				oldestClient, oldest = candidate, attempt.Last
			}
		}
		delete(a.attempts, oldestClient)
	}
	attempt := a.attempts[client]
	if attempt == nil {
		attempt = &loginAttempt{First: now}
		a.attempts[client] = attempt
	}
	attempt.Failures++
	attempt.Last = now
}

func (a *authManager) clearLoginFailures(client string) {
	a.mu.Lock()
	delete(a.attempts, client)
	a.mu.Unlock()
}

func (a *authManager) cleanupAttemptsLocked(now time.Time) {
	for client, attempt := range a.attempts {
		if now.Sub(attempt.First) >= defaultLoginWindow {
			delete(a.attempts, client)
		}
	}
}

func writeAuthJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
