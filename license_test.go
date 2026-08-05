package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTestMasterKey swaps the embedded public key for a freshly generated one
// and returns the matching private key. Restores the original on cleanup.
func withTestMasterKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	original := masterKeyPub
	masterKeyPub = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() {
		masterKeyPub = original
		invalidateLicenseCache()
	})
	invalidateLicenseCache()
	return priv
}

// makeLicense signs a payload into a lic_ token.
func makeLicense(t *testing.T, priv ed25519.PrivateKey, p licensePayload) string {
	t.Helper()
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	sig := ed25519.Sign(priv, body)
	return licensePrefix +
		base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

// useTestWorkspace points WorkspaceDir at a temp dir holding the given license.
func useTestWorkspace(t *testing.T, license string) string {
	t.Helper()
	dir := t.TempDir()
	original := WorkspaceDir
	WorkspaceDir = dir
	t.Cleanup(func() {
		WorkspaceDir = original
		invalidateLicenseCache()
	})

	cfg := trustableConfig{License: license}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trustable.json"), data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	invalidateLicenseCache()
	return dir
}

func TestLicenseSignVerifyRoundTrip(t *testing.T) {
	priv := withTestMasterKey(t)
	token := makeLicense(t, priv, licensePayload{
		V:     1,
		Sub:   "acme@example.com",
		Hosts: []string{"https://api.nuvolaris.io"},
		Iat:   "2026-08-04",
	})

	payload, err := validateLicense(token, time.Now())
	if err != nil {
		t.Fatalf("validateLicense: %v", err)
	}
	if payload.Sub != "acme@example.com" {
		t.Errorf("sub = %q, want acme@example.com", payload.Sub)
	}
	if len(payload.Hosts) != 1 || payload.Hosts[0] != "https://api.nuvolaris.io" {
		t.Errorf("hosts = %v", payload.Hosts)
	}
}

func TestLicenseTamperedPayloadFails(t *testing.T) {
	priv := withTestMasterKey(t)
	token := makeLicense(t, priv, licensePayload{
		V: 1, Sub: "a@b.c", Hosts: []string{"https://one.example.com"},
	})

	// Re-encode a payload that grants an extra host, keeping the original sig.
	parts := strings.SplitN(strings.TrimPrefix(token, licensePrefix), ".", 2)
	forged, _ := json.Marshal(licensePayload{
		V: 1, Sub: "a@b.c", Hosts: []string{"https://one.example.com", "https://evil.example.com"},
	})
	tampered := licensePrefix + base64.RawURLEncoding.EncodeToString(forged) + "." + parts[1]

	if _, err := validateLicense(tampered, time.Now()); err == nil {
		t.Fatal("tampered payload validated; want failure")
	}
}

func TestLicenseTamperedSignatureFails(t *testing.T) {
	priv := withTestMasterKey(t)
	token := makeLicense(t, priv, licensePayload{V: 1, Sub: "a@b.c"})

	parts := strings.SplitN(strings.TrimPrefix(token, licensePrefix), ".", 2)
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	sig[0] ^= 0xFF
	tampered := licensePrefix + parts[0] + "." + base64.RawURLEncoding.EncodeToString(sig)

	if _, err := validateLicense(tampered, time.Now()); err == nil {
		t.Fatal("tampered signature validated; want failure")
	}
}

func TestLicenseSignedByAnotherKeyFails(t *testing.T) {
	withTestMasterKey(t)
	_, other, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	token := makeLicense(t, other, licensePayload{V: 1, Sub: "a@b.c"})

	if _, err := validateLicense(token, time.Now()); err == nil {
		t.Fatal("license from a foreign key validated; want failure")
	}
}

func TestLicenseExpiry(t *testing.T) {
	priv := withTestMasterKey(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		exp   string
		valid bool
	}{
		{"absent exp never expires", "", true},
		{"future exp is valid", "2027-01-01", true},
		{"today is still valid", "2026-08-04", true},
		{"yesterday is expired", "2026-08-03", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := makeLicense(t, priv, licensePayload{V: 1, Sub: "a@b.c", Exp: tc.exp})
			_, err := validateLicense(token, now)
			if tc.valid && err != nil {
				t.Fatalf("want valid, got error: %v", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("want expired, got valid")
			}
		})
	}
}

func TestLicenseAllowsHost(t *testing.T) {
	payload := &licensePayload{
		Hosts: []string{
			"https://api.nuvolaris.io",
			"http://plain.example.com",
			"https://ported.example.com:8443",
		},
	}

	cases := []struct {
		name    string
		apihost string
		want    bool
	}{
		{"exact match", "https://api.nuvolaris.io", true},
		{"trailing slash ignored", "https://api.nuvolaris.io/", true},
		{"path ignored", "https://api.nuvolaris.io/api/v1", true},
		{"case insensitive", "HTTPS://API.NUVOLARIS.IO", true},
		{"http entry matches http", "http://plain.example.com", true},
		{"scheme mismatch rejected", "https://plain.example.com", false},
		{"scheme mismatch rejected the other way", "http://api.nuvolaris.io", false},
		{"port must match", "https://ported.example.com", false},
		{"port match ok", "https://ported.example.com:8443", true},
		{"wrong port rejected", "https://ported.example.com:9999", false},
		{"unrelated host rejected", "https://somewhere.else.com", false},
		{"no scheme rejected", "api.nuvolaris.io", false},
		{"empty rejected", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := licenseAllowsHost(payload, tc.apihost); got != tc.want {
				t.Errorf("licenseAllowsHost(%q) = %v, want %v", tc.apihost, got, tc.want)
			}
		})
	}
}

func TestLicenseNoWildcardBehaviour(t *testing.T) {
	// A bare domain never covers subdomains.
	bare := &licensePayload{Hosts: []string{"https://example.com"}}
	if licenseAllowsHost(bare, "https://a.example.com") {
		t.Error("https://example.com must not cover https://a.example.com")
	}
	if !licenseAllowsHost(bare, "https://example.com") {
		t.Error("https://example.com must cover itself")
	}

	// An entry containing a wildcard matches nothing at all.
	wild := &licensePayload{Hosts: []string{"https://*.example.com"}}
	for _, h := range []string{
		"https://a.example.com",
		"https://a.b.example.com",
		"https://example.com",
		"https://*.example.com",
	} {
		if licenseAllowsHost(wild, h) {
			t.Errorf("wildcard entry must never match, but matched %q", h)
		}
	}
}

func TestIsLocalApihost(t *testing.T) {
	cases := []struct {
		apihost string
		want    bool
	}{
		{"http://miniops.me", true},
		{"https://miniops.me", true},
		{"http://miniops.me/", true},
		{"http://miniops.me:8910", true},
		{"HTTP://MINIOPS.ME", true},
		{"http://localhost", true},
		{"http://localhost:8910", true},
		{"http://127.0.0.1", true},
		{"http://[::1]:8910", true},
		// Neither a suffix nor a subdomain of a local host is local.
		{"https://miniops.me.evil.com", false},
		{"https://x.miniops.me", false},
		{"https://notminiops.me", false},
		{"https://api.nuvolaris.io", false},
		{"miniops.me", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.apihost, func(t *testing.T) {
			if got := isLocalApihost(tc.apihost); got != tc.want {
				t.Errorf("isLocalApihost(%q) = %v, want %v", tc.apihost, got, tc.want)
			}
		})
	}
}

func TestPostLicenseRejectsMalformedTokenAndLeavesConfigUntouched(t *testing.T) {
	withTestMasterKey(t)
	dir := useTestWorkspace(t, "")
	configPath := filepath.Join(dir, "trustable.json")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	body := strings.NewReader(`{"license":"lic_not-a-real-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/license", body)
	rec := httptest.NewRecorder()
	handleLicense(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid license") {
		t.Errorf("body = %q, want an 'Invalid license' error", rec.Body.String())
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("config changed after a rejected license:\nbefore %s\nafter  %s", before, after)
	}
}

func TestLicenseAPIRoundTrip(t *testing.T) {
	priv := withTestMasterKey(t)
	useTestWorkspace(t, "")
	token := makeLicense(t, priv, licensePayload{
		V: 1, Sub: "acme@example.com",
		Hosts: []string{"https://api.nuvolaris.io"},
		Iat:   "2026-08-04",
	})

	// GET with nothing installed.
	rec := httptest.NewRecorder()
	handleLicense(rec, httptest.NewRequest(http.MethodGet, "/api/license", nil))
	var status licenseStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if status.Present || status.Valid {
		t.Errorf("empty workspace reported present=%v valid=%v", status.Present, status.Valid)
	}

	// POST a good license.
	rec = httptest.NewRecorder()
	handleLicense(rec, httptest.NewRequest(http.MethodPost, "/api/license",
		strings.NewReader(`{"license":"`+token+`"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode POST: %v", err)
	}
	if !status.Present || !status.Valid || status.Sub != "acme@example.com" {
		t.Errorf("after POST: %+v", status)
	}
	// The raw token must never be echoed back.
	if strings.Contains(rec.Body.String(), token) {
		t.Error("POST response echoed the raw license token")
	}

	// DELETE removes it.
	rec = httptest.NewRecorder()
	handleLicense(rec, httptest.NewRequest(http.MethodDelete, "/api/license", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode DELETE: %v", err)
	}
	if status.Present || status.Valid {
		t.Errorf("after DELETE: %+v", status)
	}
}

func TestRequireValidLicense(t *testing.T) {
	priv := withTestMasterKey(t)

	t.Run("no license returns 402", func(t *testing.T) {
		useTestWorkspace(t, "")
		rec := httptest.NewRecorder()
		if requireValidLicense(rec) {
			t.Fatal("gate passed with no license")
		}
		if rec.Code != http.StatusPaymentRequired {
			t.Errorf("status = %d, want 402", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "License required") {
			t.Errorf("body = %q, want the 'License required' prefix", rec.Body.String())
		}
	})

	t.Run("valid license passes even when hosts is irrelevant", func(t *testing.T) {
		token := makeLicense(t, priv, licensePayload{
			V: 1, Sub: "a@b.c", Hosts: []string{"https://unrelated.example.com"},
		})
		useTestWorkspace(t, token)
		rec := httptest.NewRecorder()
		if !requireValidLicense(rec) {
			t.Fatalf("gate rejected a valid license: %s", rec.Body.String())
		}
	})

	t.Run("expired license returns 402", func(t *testing.T) {
		token := makeLicense(t, priv, licensePayload{
			V: 1, Sub: "a@b.c", Hosts: []string{"https://api.nuvolaris.io"}, Exp: "2020-01-01",
		})
		useTestWorkspace(t, token)
		rec := httptest.NewRecorder()
		if requireValidLicense(rec) {
			t.Fatal("gate passed with an expired license")
		}
		if rec.Code != http.StatusPaymentRequired {
			t.Errorf("status = %d, want 402", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "License required") {
			t.Errorf("body = %q, want the 'License required' prefix", rec.Body.String())
		}
	})
}

func TestRequireLicensedHost(t *testing.T) {
	priv := withTestMasterKey(t)
	token := makeLicense(t, priv, licensePayload{
		V: 1, Sub: "a@b.c", Hosts: []string{"https://api.nuvolaris.io"},
	})

	t.Run("licensed host passes", func(t *testing.T) {
		useTestWorkspace(t, token)
		rec := httptest.NewRecorder()
		if !requireLicensedHost(rec, "https://api.nuvolaris.io") {
			t.Fatalf("licensed host rejected: %s", rec.Body.String())
		}
	})

	t.Run("unlicensed host returns 402", func(t *testing.T) {
		useTestWorkspace(t, token)
		rec := httptest.NewRecorder()
		if requireLicensedHost(rec, "https://other.example.com") {
			t.Fatal("unlicensed host passed")
		}
		if rec.Code != http.StatusPaymentRequired {
			t.Errorf("status = %d, want 402", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Host not licensed") {
			t.Errorf("body = %q, want the 'Host not licensed' prefix", rec.Body.String())
		}
	})

	t.Run("local hosts are exempt from the host gate", func(t *testing.T) {
		useTestWorkspace(t, token)
		for _, h := range []string{
			"http://miniops.me",
			"https://miniops.me",
			"http://localhost:8910",
			"http://127.0.0.1",
		} {
			rec := httptest.NewRecorder()
			if !requireLicensedHost(rec, h) {
				t.Errorf("local host %q rejected: %s", h, rec.Body.String())
			}
		}
	})

	t.Run("lookalike hosts are not exempt", func(t *testing.T) {
		useTestWorkspace(t, token)
		for _, h := range []string{"https://miniops.me.evil.com", "https://x.miniops.me"} {
			rec := httptest.NewRecorder()
			if requireLicensedHost(rec, h) {
				t.Errorf("lookalike host %q was treated as local", h)
			}
			if !strings.Contains(rec.Body.String(), "Host not licensed") {
				t.Errorf("host %q: body = %q, want 'Host not licensed'", h, rec.Body.String())
			}
		}
	})

	t.Run("local exemption does not bypass the license gate", func(t *testing.T) {
		// No license at all.
		useTestWorkspace(t, "")
		rec := httptest.NewRecorder()
		if requireValidLicense(rec) {
			t.Fatal("license gate passed with no license")
		}
		if !strings.Contains(rec.Body.String(), "License required") {
			t.Errorf("body = %q, want 'License required'", rec.Body.String())
		}

		// Expired license.
		expired := makeLicense(t, priv, licensePayload{
			V: 1, Sub: "a@b.c", Hosts: []string{"http://miniops.me"}, Exp: "2020-01-01",
		})
		useTestWorkspace(t, expired)
		rec = httptest.NewRecorder()
		if requireValidLicense(rec) {
			t.Fatal("license gate passed with an expired license")
		}
		if !strings.Contains(rec.Body.String(), "License required") {
			t.Errorf("body = %q, want 'License required'", rec.Body.String())
		}
	})
}

// TestPublishHandlersGated checks the wiring end-to-end: the publish handlers
// refuse to act without a license and never shell out to ops/git.
func TestPublishHandlersGated(t *testing.T) {
	priv := withTestMasterKey(t)

	t.Run("push returns 402 without a license", func(t *testing.T) {
		useTestWorkspace(t, "")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/publish/push",
			strings.NewReader(`{"name":"demoapp","repo":"git@github.com:x/y.git"}`))
		handlePublishPush(rec, req)

		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402 (%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "License required") {
			t.Errorf("body = %q, want 'License required'", rec.Body.String())
		}
	})

	t.Run("force-push returns 402 without a license", func(t *testing.T) {
		useTestWorkspace(t, "")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/publish/force-push",
			strings.NewReader(`{"name":"demoapp"}`))
		handlePublishForcePush(rec, req)

		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "License required") {
			t.Errorf("body = %q, want 'License required'", rec.Body.String())
		}
	})

	t.Run("remote returns 402 and does not invoke ops for an unlicensed host", func(t *testing.T) {
		token := makeLicense(t, priv, licensePayload{
			V: 1, Sub: "a@b.c", Hosts: []string{"https://api.nuvolaris.io"},
		})
		dir := useTestWorkspace(t, token)

		// Point WorkbenchDir at a path that does not exist: if the handler got
		// past the host gate it would try to clone and fail differently.
		originalWorkbench := WorkbenchDir
		WorkbenchDir = filepath.Join(dir, "workbench")
		t.Cleanup(func() { WorkbenchDir = originalWorkbench })

		// Make `ops` and `git` resolve to a recorder that fails the test if run.
		guardPath(t, dir)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/publish/remote",
			strings.NewReader(`{"name":"demoapp","apihost":"https://unlicensed.example.com","user":"u","password":"p"}`))
		handlePublishRemote(rec, req)

		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402 (%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Host not licensed") {
			t.Errorf("body = %q, want 'Host not licensed'", rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "ops-was-run")); err == nil {
			t.Error("ops was invoked for an unlicensed host")
		}
	})
}

// guardPath puts stub `ops` and `git` binaries at the front of PATH; running
// either leaves a marker file the caller can assert on.
func guardPath(t *testing.T, dir string) {
	t.Helper()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range []string{"ops", "git"} {
		script := "#!/bin/sh\ntouch " + filepath.Join(dir, name+"-was-run") + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
