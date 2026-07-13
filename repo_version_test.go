package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVersionMetadata(t *testing.T) {
	oldVersion, oldBuild := appVersion, appBuild
	oldBranch, oldStream, oldExpiry := appBranch, appStream, expiryDate
	t.Cleanup(func() {
		appVersion, appBuild = oldVersion, oldBuild
		appBranch, appStream, expiryDate = oldBranch, oldStream, oldExpiry
	})

	parseVersion("Version: v0.3.11\nBuild: local-42\nBranch: feature/runtime\nStream: trustable-code\nExpiry: 2099/08/31\n")

	recorder := httptest.NewRecorder()
	handleVersion(recorder, httptest.NewRequest("GET", "/api/version", nil))

	var got map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := map[string]string{
		"version": "Trustable v0.3.11",
		"build":   "local-42",
		"branch":  "feature/runtime",
		"stream":  "trustable-code",
		"expire":  "2099/08/31",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
	if expiryDate != time.Date(2099, time.August, 31, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("expiryDate = %v", expiryDate)
	}
}
