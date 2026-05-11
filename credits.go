package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// aipURLsFromConfig returns the bearer key needed to call the upstream AIP
// JSON endpoints. The upstream base lives in AIPBaseURL (set at preflight
// from the AIP_BASE_URL env var) and is independent of cfg.base_url, which
// is the user-facing OpenAI-compatible base used by OpenCode.
//
// Returns errProviderNotTrustable so the caller can map that to HTTP 404.
var errProviderNotTrustable = fmt.Errorf("provider is not trustable")

type aipURLs struct {
	APIBase string // e.g. "http://localhost:8080/api/v2"
	APIKey  string
}

func aipURLsFromConfig() (aipURLs, error) {
	cfg, err := loadTrustableConfig()
	if err != nil {
		return aipURLs{}, fmt.Errorf("load config: %w", err)
	}
	if cfg.Provider != "trustable" {
		return aipURLs{}, errProviderNotTrustable
	}
	if cfg.APIKey == "" {
		return aipURLs{}, fmt.Errorf("api_key is empty")
	}
	if AIPBaseURL == "" {
		return aipURLs{}, fmt.Errorf("AIP_BASE_URL is not set")
	}
	return aipURLs{APIBase: AIPBaseURL, APIKey: cfg.APIKey}, nil
}

// handleCredits proxies GET /api/credits to GET $AIP_BASE_URL/credits.
// Per spec/3-app.md "Credits API":
//   - 404 when provider != "trustable"
//   - on 2xx from the proxy: return the JSON body verbatim
//   - on non-2xx from the proxy: 502 with {"error": "<status>: <body>"}
func handleCredits(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	aip, err := aipURLsFromConfig()
	if err == errProviderNotTrustable {
		http.Error(w, "not trustable", http.StatusNotFound)
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, aip.APIBase+"/credits", nil)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+aip.APIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("%d: %s", resp.StatusCode, string(body)))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// allowedTopUpAmounts mirrors the default TOPUP_AMOUNTS whitelist documented in
// spec/credit_check.md. The proxy is the source of truth (it returns
// 400 invalid_amount on mismatch); we still pre-check here so a typo doesn't
// hit the network.
var allowedTopUpAmounts = map[int64]bool{
	1000:  true,
	5000:  true,
	10000: true,
}

// handleTopUp proxies POST /api/topup to POST $AIP_BASE_URL/top-up.
// Request body: {"amount": <int>}.
// Per spec/3-app.md "Credits API":
//   - 404 when provider != "trustable"
//   - 400 {"error": "invalid_amount"} when the amount is rejected (locally or by proxy)
//   - 2xx: return the proxy body verbatim
//   - other non-2xx: 502 with {"error": "<status>: <body>"}
func handleTopUp(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var in struct {
		Amount int64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if !allowedTopUpAmounts[in.Amount] {
		writeJSONError(w, http.StatusBadRequest, "invalid_amount")
		return
	}

	aip, err := aipURLsFromConfig()
	if err == errProviderNotTrustable {
		http.Error(w, "not trustable", http.StatusNotFound)
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	payload, _ := json.Marshal(map[string]int64{"amount": in.Amount})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, aip.APIBase+"/top-up", bytes.NewReader(payload))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+aip.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	if resp.StatusCode == http.StatusBadRequest && bytesContainInvalidAmount(body) {
		writeJSONError(w, http.StatusBadRequest, "invalid_amount")
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("%d: %s", resp.StatusCode, string(body)))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func bytesContainInvalidAmount(body []byte) bool {
	var probe struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.Error.Code == "invalid_amount"
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
