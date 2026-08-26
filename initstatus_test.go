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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeInitLock points WorkspaceDir at a temp dir and optionally writes a lock
// with the given content. Returns the temp workspace root.
func writeInitLock(t *testing.T, content string, write bool) string {
	t.Helper()
	dir := t.TempDir()
	saved := WorkspaceDir
	WorkspaceDir = dir
	t.Cleanup(func() { WorkspaceDir = saved })

	if write {
		lock := filepath.Join(dir, ".trustable", "init.lock")
		if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
			t.Fatalf("mkdir: %s", err)
		}
		if err := os.WriteFile(lock, []byte(content), 0o644); err != nil {
			t.Fatalf("write lock: %s", err)
		}
	}
	return dir
}

func getInitStatus(t *testing.T) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	handleInitStatus(rec, httptest.NewRequest(http.MethodGet, "/api/initstatus", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %s", err)
	}
	return out
}

// No lock is both the steady state and the development case, where
// image/start.sh never runs. The splash must not hang on a dev machine.
func TestInitStatusReportsNotInitializingWhenLockAbsent(t *testing.T) {
	writeInitLock(t, "", false)
	out := getInitStatus(t)
	if out["initializing"] != false {
		t.Fatalf("initializing = %v, want false when the lock is absent", out["initializing"])
	}
}

func TestInitStatusReportsCountWhenLockPresent(t *testing.T) {
	writeInitLock(t, "12480\n", true)
	out := getInitStatus(t)
	if out["initializing"] != true {
		t.Fatalf("initializing = %v, want true while the lock exists", out["initializing"])
	}
	if got := out["count"].(float64); got != 12480 {
		t.Fatalf("count = %v, want 12480", got)
	}
}

// A malformed counter must not abort the wait: the chown is still running, we
// just do not know how far along it is.
func TestInitStatusTreatsGarbageCountAsZeroButStillInitializing(t *testing.T) {
	for _, garbage := range []string{"not-a-number", "", "-5", "12 34"} {
		writeInitLock(t, garbage, true)
		out := getInitStatus(t)
		if out["initializing"] != true {
			t.Fatalf("initializing = %v for lock %q, want true", out["initializing"], garbage)
		}
		if got := out["count"].(float64); got != 0 {
			t.Fatalf("count = %v for lock %q, want 0", got, garbage)
		}
	}
}

func TestInitStatusRejectsNonGet(t *testing.T) {
	writeInitLock(t, "", false)
	rec := httptest.NewRecorder()
	handleInitStatus(rec, httptest.NewRequest(http.MethodPost, "/api/initstatus", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
