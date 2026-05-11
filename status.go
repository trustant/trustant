package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// handleStatus proxies GET $AIP_BASE_URL/status?version=<appVersion> and
// returns the response body unchanged. Used by the splash and applist pages
// to receive per-provider model catalogs and operator banner messages.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if AIPBaseURL == "" {
		writeStatusError(w, "AIP_BASE_URL is not set")
		return
	}

	endpoint := AIPBaseURL + "/status?version=" + url.QueryEscape(appVersion)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		writeStatusError(w, fmt.Sprintf("status fetch failed: %s", err))
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeStatusError(w, fmt.Sprintf("status read failed: %s", err))
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeStatusError(w, fmt.Sprintf("status returned HTTP %d", resp.StatusCode))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func writeStatusError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
