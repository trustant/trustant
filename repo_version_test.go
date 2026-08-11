package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestVersionMetadata(t *testing.T) {
	oldVersion, oldBuild := appVersion, appBuild
	oldBranch, oldStream, oldExpiry := appBranch, appStream, expiryDate
	oldGitBranch := gitBranch
	t.Cleanup(func() {
		appVersion, appBuild = oldVersion, oldBuild
		appBranch, appStream, expiryDate = oldBranch, oldStream, oldExpiry
		gitBranch = oldGitBranch
	})

	parseVersion("Version: v0.3.11\nBuild: local-42\nBranch: feature/runtime\nStream: trustable-code\nExpiry: 2099/08/31\n")

	recorder := httptest.NewRecorder()
	handleVersion(recorder, httptest.NewRequest("GET", "/api/version", nil))

	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := map[string]any{
		"version": "Trustable v0.3.11",
		"build":   "local-42",
		"branch":  "feature/runtime",
		"stream":  "trustable-code",
		"expire":  "2099/08/31",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want %v", key, got[key], value)
		}
	}
	if expiryDate != time.Date(2099, time.August, 31, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("expiryDate = %v", expiryDate)
	}
}

// The web pages read the two feature flags off /api/version, so they must be
// present as real booleans mirroring EnableLicense / EnableRegolo.
func TestVersionReportsFeatureFlags(t *testing.T) {
	oldExpiry := expiryDate
	oldLicense, oldRegolo := EnableLicense, EnableRegolo
	t.Cleanup(func() {
		expiryDate = oldExpiry
		EnableLicense, EnableRegolo = oldLicense, oldRegolo
	})
	expiryDate = time.Date(2099, time.August, 31, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct{ license, regolo bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		EnableLicense, EnableRegolo = tc.license, tc.regolo
		recorder := httptest.NewRecorder()
		handleVersion(recorder, httptest.NewRequest("GET", "/api/version", nil))

		var got map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got["license"] != tc.license {
			t.Errorf("license = %v, want %v", got["license"], tc.license)
		}
		if got["regolo"] != tc.regolo {
			t.Errorf("regolo = %v, want %v", got["regolo"], tc.regolo)
		}
	}
}

// envFlag treats unset and empty as OFF: that is what keeps a plain .env
// (which has neither variable) running without the license gate.
func TestEnvFlag(t *testing.T) {
	const key = "TRUSTABLE_TEST_FLAG"
	for _, tc := range []struct {
		value string
		set   bool
		want  bool
	}{
		{"", false, false},
		{"", true, false},
		{"  ", true, false},
		{"0", true, false},
		{"false", true, false},
		{"FALSE", true, false},
		{"no", true, false},
		{"off", true, false},
		{"1", true, true},
		{"true", true, true},
		{"yes", true, true},
	} {
		os.Unsetenv(key)
		if tc.set {
			os.Setenv(key, tc.value)
		}
		if got := envFlag(key); got != tc.want {
			t.Errorf("envFlag(%q set=%v) = %v, want %v", tc.value, tc.set, got, tc.want)
		}
	}
	os.Unsetenv(key)
}

func TestVersionMetadataFallsBackToDevelopmentBranch(t *testing.T) {
	oldGitBranch := gitBranch
	gitBranch = func() string { return "trustable-code" }
	t.Cleanup(func() { gitBranch = oldGitBranch })

	parseVersion("Version: v0.3.11\nBuild: local\nExpiry: 2099/08/31\n")

	if appBranch != "trustable-code" {
		t.Fatalf("branch = %q, want trustable-code", appBranch)
	}
	if appStream != "trustable-code" {
		t.Fatalf("stream = %q, want trustable-code", appStream)
	}
}
