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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultStatusURL is the static status file published with the website. It
// carries the operator `messages` map and one model catalog per provider.
// STATUS_URL in the environment overrides it (testing only).
const defaultStatusURL = "https://trustant.ai/status.json"

// handleStatus fetches the status file and returns it with the top-level
// `messages` map replaced by a single `message` string for the running
// version (see resolveStatus). Used by the splash and applist pages to receive
// per-provider model catalogs (`ollama`, `trustant`) and the operator banner.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(StatusURL)
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
	out, err := resolveStatus(body, appVersion)
	if err != nil {
		writeStatusError(w, fmt.Sprintf("status is not a JSON object: %s", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}

// resolveStatus drops the `messages` map and sets `message` to
// messages[version], else messages["default"], else "". Every other key
// (the provider catalogs) passes through unchanged.
func resolveStatus(body []byte, version string) ([]byte, error) {
	var status map[string]json.RawMessage
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, err
	}
	if status == nil {
		return nil, fmt.Errorf("null")
	}
	var messages map[string]string
	_ = json.Unmarshal(status["messages"], &messages)
	message, ok := messages[version]
	if !ok {
		message = messages["default"]
	}
	delete(status, "messages")
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	status["message"] = encoded
	return json.Marshal(status)
}

func writeStatusError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
