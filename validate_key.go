package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// proxyOriginFromBaseURL strips a trailing "/v1" (and any trailing slash) from
// the configured base_url to produce the proxy origin used to fetch the
// well-known. Per spec/10-validate_key.md: "https://ai.trustable.ai/v1" ->
// "https://ai.trustable.ai".
func proxyOriginFromBaseURL(baseURL string) (string, error) {
	s := strings.TrimSpace(baseURL)
	if s == "" {
		return "", fmt.Errorf("base_url is empty")
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("base_url is not a valid URL: %q", baseURL)
	}
	origin := u.Scheme + "://" + u.Host
	return origin, nil
}

// pubKeyCache holds the cached Ed25519 public key for the lifetime of the process.
// Per spec/10-validate_key.md ("Fetch once and cache") and the configured policy:
// no re-fetch on verification failure.
var pubKeyCache struct {
	sync.Mutex
	byOrigin map[string]ed25519.PublicKey
}

func init() {
	pubKeyCache.byOrigin = make(map[string]ed25519.PublicKey)
}

// fetchProxyPubKey fetches and caches the Ed25519 public key for the given proxy origin.
func fetchProxyPubKey(origin string) (ed25519.PublicKey, error) {
	pubKeyCache.Lock()
	if k, ok := pubKeyCache.byOrigin[origin]; ok {
		pubKeyCache.Unlock()
		return k, nil
	}
	pubKeyCache.Unlock()

	endpoint := strings.TrimRight(origin, "/") + "/.well-known/ai-proxy-pubkey"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("fetch %s: HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Alg       string `json:"alg"`
		PublicKey string `json:"public_key"`
		Format    string `json:"format"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	if payload.Alg != "Ed25519" {
		return nil, fmt.Errorf("unexpected alg %q from %s", payload.Alg, endpoint)
	}
	pub, err := base64.StdEncoding.DecodeString(payload.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode public_key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public_key has wrong size %d (want %d)", len(pub), ed25519.PublicKeySize)
	}

	pubKey := ed25519.PublicKey(pub)
	pubKeyCache.Lock()
	pubKeyCache.byOrigin[origin] = pubKey
	pubKeyCache.Unlock()
	return pubKey, nil
}

// validateAIProxyKey verifies an "aip_<id>.<sig>" key against the given Ed25519 public key.
// See spec/10-validate_key.md.
func validateAIProxyKey(key string, pub ed25519.PublicKey) error {
	rest, ok := strings.CutPrefix(key, "aip_")
	if !ok {
		return fmt.Errorf("key does not start with aip_")
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 2 {
		return fmt.Errorf("malformed key: expected 2 parts separated by '.'")
	}
	id, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("decode id: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature has wrong size %d (want %d)", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, id, sig) {
		return fmt.Errorf("signature does not verify against proxy public key")
	}
	return nil
}

// requirePublishingAuth enforces server-side publish authorization per spec/6-publish.md.
// On failure it writes a 403 JSON error and returns false; the caller must return immediately.
func requirePublishingAuth(w http.ResponseWriter) bool {
	cfg, err := loadTrustableConfig()
	if err != nil {
		writePublishAuthError(w, "failed to load configuration: "+err.Error())
		return false
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		writePublishAuthError(w, "api_key is not set")
		return false
	}
	// The publish key is the proxy-issued aip_ token and must be verified
	// against the ai-proxy's public key. For trustable, cfg.BaseURL *is* the
	// proxy. For bestia, cfg.BaseURL points at the dedicated GPU inference box
	// (http://bestia:11434/v1), which is NOT the proxy and does not serve the
	// well-known — so derive the origin from AIPBaseURL instead (the same
	// cfg.base_url-independent source credits.go uses). See spec/6-publish.md.
	originSource := cfg.BaseURL
	if cfg.Provider == "bestia" {
		if AIPBaseURL == "" {
			writePublishAuthError(w, "AIP_BASE_URL is not set")
			return false
		}
		originSource = AIPBaseURL
	}
	origin, err := proxyOriginFromBaseURL(originSource)
	if err != nil {
		writePublishAuthError(w, err.Error())
		return false
	}
	pub, err := fetchProxyPubKey(origin)
	if err != nil {
		writePublishAuthError(w, "cannot fetch proxy public key: "+err.Error())
		return false
	}
	if err := validateAIProxyKey(apiKey, pub); err != nil {
		writePublishAuthError(w, err.Error())
		return false
	}
	return true
}

func writePublishAuthError(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{
		"error": "Publishing not authorized: " + reason,
	})
}
