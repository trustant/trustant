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
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLaunchProgressSSEContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/launch/demo", nil)
	request.Header.Set("Accept", "text/event-stream")
	writer, ok := prepareLaunchResponseWriter(recorder, request)
	if !ok { t.Fatal("expected streaming response writer") }
	reportLaunchProgress(writer, 3, "Connecting to OpenServerless...")
	if err := json.NewEncoder(writer).Encode(map[string]interface{}{"browser_url": "http://localhost:5173"}); err != nil { t.Fatalf("encode launch result: %v", err) }
	body := recorder.Body.String()
	for _, expected := range []string{"event: progress", "\"stage\":3", "\"total\":8", "Connecting to OpenServerless...", "event: done", "\"browser_url\":\"http://localhost:5173\""} {
		if !strings.Contains(body, expected) { t.Errorf("stream does not contain %q: %s", expected, body) }
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" { t.Fatalf("Content-Type = %q, want text/event-stream", got) }
}

func TestLaunchProgressSSEPreservesErrorPayload(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/launch/demo", nil)
	request.Header.Set("Accept", "text/event-stream")
	writer, ok := prepareLaunchResponseWriter(recorder, request)
	if !ok { t.Fatal("expected streaming response writer") }
	if err := json.NewEncoder(writer).Encode(map[string]interface{}{"error": "setup required", "setup_required": true}); err != nil { t.Fatalf("encode launch error: %v", err) }
	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "\"setup_required\":true") { t.Fatalf("stream did not preserve launch error payload: %s", body) }
}

func TestApplicationListLaunchProgressWiring(t *testing.T) {
	content, err := os.ReadFile("web/applist.html")
	if err != nil { t.Fatalf("read application list: %v", err) }
	html := string(content)
	for _, expected := range []string{`id="launchProgressBar"`, `role="progressbar"`, `'Accept': 'text/event-stream'`, `response.body.getReader()`} {
		if !strings.Contains(html, expected) { t.Errorf("application list does not contain %q", expected) }
	}
}
