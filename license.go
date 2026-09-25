// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// masterKeyPub is the trusted Ed25519 public key, produced by `trulicense keygen`
// from the "MasterKey Trustant" 1Password vault item. Verification is fully
// offline: the server never talks to 1Password. See spec/14-license.md.
//
//go:embed master_key_pub
var masterKeyPub string

// licensePrefix is the token prefix, mirroring the aip_ key shape.
const licensePrefix = "lic_"

// licenseDateFormat is the format used for iat/exp in the payload.
const licenseDateFormat = "2006-01-02"

// licensePayload is the signed body of a license token.
type licensePayload struct {
	V     int      `json:"v"`
	Sub   string   `json:"sub"`
	Hosts []string `json:"hosts"`
	Iat   string   `json:"iat,omitempty"`
	Exp   string   `json:"exp,omitempty"`
}

// licenseCache memoizes the parsed license for the process lifetime. It is
// invalidated whenever the license is set or deleted through the API.
var licenseCache struct {
	sync.Mutex
	loaded  bool
	payload *licensePayload
	err     error
}

func invalidateLicenseCache() {
	licenseCache.Lock()
	licenseCache.loaded = false
	licenseCache.payload = nil
	licenseCache.err = nil
	licenseCache.Unlock()
}

// masterPublicKey decodes the embedded public key.
func masterPublicKey() (ed25519.PublicKey, error) {
	raw := strings.TrimSpace(masterKeyPub)
	if raw == "" {
		return nil, fmt.Errorf("embedded master_key_pub is empty")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode master_key_pub: %w", err)
	}
	if len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("master_key_pub has wrong size %d (want %d)", len(key), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(key), nil
}

// parseLicense decodes and verifies a "lic_<payload>.<sig>" token against the
// given public key. The signature is checked over the exact payload bytes as
// decoded from part 1, so re-serialization can never change the result.
// Expiry is NOT checked here; use validateLicense for the full check.
func parseLicense(token string, pub ed25519.PublicKey) (*licensePayload, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(token), licensePrefix)
	if !ok {
		return nil, fmt.Errorf("license does not start with %s", licensePrefix)
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed license: expected 2 parts separated by '.'")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("signature has wrong size %d (want %d)", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, payloadBytes, sig) {
		return nil, fmt.Errorf("signature does not verify against the Trustant master key")
	}
	var payload licensePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("parse payload: %w", err)
	}
	return &payload, nil
}

// licenseExpired reports whether the payload carries a past exp date. An absent
// exp means the license never expires. An unparseable exp is treated as
// invalid rather than as "never expires".
func licenseExpired(p *licensePayload, now time.Time) (bool, error) {
	exp := strings.TrimSpace(p.Exp)
	if exp == "" {
		return false, nil
	}
	t, err := time.Parse(licenseDateFormat, exp)
	if err != nil {
		return false, fmt.Errorf("license has an unreadable expiry date %q", exp)
	}
	// The license stays valid through the whole exp day.
	return now.After(t.AddDate(0, 0, 1).Add(-time.Nanosecond)), nil
}

// validateLicense verifies signature and expiry of a token.
func validateLicense(token string, now time.Time) (*licensePayload, error) {
	pub, err := masterPublicKey()
	if err != nil {
		return nil, err
	}
	payload, err := parseLicense(token, pub)
	if err != nil {
		return nil, err
	}
	expired, err := licenseExpired(payload, now)
	if err != nil {
		return nil, err
	}
	if expired {
		return nil, fmt.Errorf("license expired on %s", payload.Exp)
	}
	return payload, nil
}

// loadLicense reads the license token from the workspace config `license`
// field, verifies it, and caches the result for the process lifetime.
func loadLicense() (*licensePayload, error) {
	licenseCache.Lock()
	defer licenseCache.Unlock()
	if licenseCache.loaded {
		return licenseCache.payload, licenseCache.err
	}

	payload, err := loadLicenseUncached()
	licenseCache.loaded = true
	licenseCache.payload = payload
	licenseCache.err = err
	return payload, err
}

func loadLicenseUncached() (*licensePayload, error) {
	cfg, err := loadWorkspaceConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	token := strings.TrimSpace(cfg.License)
	if token == "" {
		return nil, fmt.Errorf("no license is installed")
	}
	return validateLicense(token, time.Now())
}

// normalizeAPIHost reduces an apihost to a comparable "scheme://host[:port]"
// form: lowercased, with any path, query and trailing slash stripped. It fails
// on entries without an http/https scheme or without a host.
func normalizeAPIHost(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty host")
	}
	if strings.Contains(s, "*") {
		return "", fmt.Errorf("wildcards are not supported in %q", raw)
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("not a valid URL: %q", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("host %q must include an http:// or https:// scheme", raw)
	}
	host := strings.ToLower(u.Host)
	if host == "" {
		return "", fmt.Errorf("host %q has no hostname", raw)
	}
	return scheme + "://" + host, nil
}

// licenseAllowsHost reports whether apihost is covered by the license hosts
// list. Matching is exact on scheme + host + port after normalization; there
// are no wildcards and no subdomain expansion.
func licenseAllowsHost(p *licensePayload, apihost string) bool {
	if p == nil {
		return false
	}
	want, err := normalizeAPIHost(apihost)
	if err != nil {
		return false
	}
	for _, entry := range p.Hosts {
		got, err := normalizeAPIHost(entry)
		if err != nil {
			// Defensive: an entry without a scheme, or carrying a wildcard,
			// never matches.
			continue
		}
		if got == want {
			return true
		}
	}
	return false
}

// localAPIHosts is the fixed set of hostnames that are always considered
// licensed. Publishing to the local mini cluster must not require listing it in
// every license. Compared as whole hostnames, never as substrings or suffixes.
var localAPIHosts = map[string]bool{
	"miniops.me": true,
	"localhost":  true,
	"127.0.0.1":  true,
	"::1":        true,
}

// isLocalApihost reports whether apihost points at the local cluster. Only the
// host part is considered, so any port is accepted and either scheme matches.
func isLocalApihost(apihost string) bool {
	normalized, err := normalizeAPIHost(apihost)
	if err != nil {
		return false
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return false
	}
	return localAPIHosts[strings.ToLower(u.Hostname())]
}

// Error prefixes are a frontend contract: web/applist.html keys its license
// modal off them. Changing the wording here breaks that modal.
const (
	licenseRequiredPrefix = "License required: "
	hostNotLicensedPrefix = "Host not licensed: "
)

func writeLicenseError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// requireValidLicense gates git push and publishing. On failure it writes a 402
// JSON error and returns false; the caller must return immediately. With
// ENABLE_LICENSE unset or empty the whole check is skipped.
func requireValidLicense(w http.ResponseWriter) bool {
	if !EnableLicense {
		return true
	}
	if _, err := loadLicense(); err != nil {
		writeLicenseError(w, licenseRequiredPrefix+err.Error())
		return false
	}
	return true
}

// requireLicensedHost gates production deploys to a specific apihost. It must
// be called only after requireValidLicense has passed. Local apihosts are
// always allowed; the license hosts list is consulted for everything else.
// With ENABLE_LICENSE unset or empty the whole check is skipped.
func requireLicensedHost(w http.ResponseWriter, apihost string) bool {
	if !EnableLicense {
		return true
	}
	if isLocalApihost(apihost) {
		return true
	}
	payload, err := loadLicense()
	if err != nil {
		writeLicenseError(w, licenseRequiredPrefix+err.Error())
		return false
	}
	if !licenseAllowsHost(payload, apihost) {
		writeLicenseError(w, hostNotLicensedPrefix+apihost+" is not covered by your license")
		return false
	}
	return true
}

// licenseStatus is the JSON shape returned by GET/POST /api/license. The raw
// token is never echoed back.
type licenseStatus struct {
	Present bool     `json:"present"`
	Sub     string   `json:"sub,omitempty"`
	Hosts   []string `json:"hosts,omitempty"`
	Iat     string   `json:"iat,omitempty"`
	Exp     string   `json:"exp,omitempty"`
	Valid   bool     `json:"valid"`
	Reason  string   `json:"reason,omitempty"`
}

func currentLicenseStatus() licenseStatus {
	cfg, err := loadWorkspaceConfig()
	if err != nil {
		return licenseStatus{Reason: "failed to load configuration: " + err.Error()}
	}
	token := strings.TrimSpace(cfg.License)
	if token == "" {
		return licenseStatus{Reason: "no license is installed"}
	}
	status := licenseStatus{Present: true}
	payload, err := validateLicense(token, time.Now())
	if err != nil {
		status.Reason = err.Error()
		// Surface the descriptive fields even when the license no longer
		// validates, so the UI can say *which* license expired.
		if pub, keyErr := masterPublicKey(); keyErr == nil {
			if p, parseErr := parseLicense(token, pub); parseErr == nil {
				status.Sub, status.Hosts, status.Iat, status.Exp = p.Sub, p.Hosts, p.Iat, p.Exp
			}
		}
		return status
	}
	status.Valid = true
	status.Sub, status.Hosts, status.Iat, status.Exp = payload.Sub, payload.Hosts, payload.Iat, payload.Exp
	return status
}

func writeLicenseStatus(w http.ResponseWriter, status licenseStatus) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleLicense serves GET/POST/DELETE /api/license. See spec/14-license.md.
func handleLicense(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeLicenseStatus(w, currentLicenseStatus())

	case http.MethodPost:
		var req struct {
			License string `json:"license"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		token := strings.TrimSpace(req.License)
		// Validate before storing: an invalid license never reaches the config.
		if _, err := validateLicense(token, time.Now()); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "Invalid license: " + err.Error(),
			})
			return
		}
		cfg, err := loadWorkspaceConfig()
		if err != nil {
			http.Error(w, "Failed to load config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		cfg.License = token
		if err := saveWorkspaceConfig(cfg); err != nil {
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		invalidateLicenseCache()
		writeLicenseStatus(w, currentLicenseStatus())

	case http.MethodDelete:
		cfg, err := loadWorkspaceConfig()
		if err != nil {
			http.Error(w, "Failed to load config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		cfg.License = ""
		if err := saveWorkspaceConfig(cfg); err != nil {
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		invalidateLicenseCache()
		writeLicenseStatus(w, currentLicenseStatus())

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
