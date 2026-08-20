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

// opsInfoFixture is verbatim real `ops -info` output. It carries the three
// properties that dictate how the parser must work: a first key containing
// spaces and an ampersand, an empty OPS_BRANCH value, and values holding their
// own colons.
const opsInfoFixture = `OPS & OPS_CMD: /home/user/.local/bin/ops
OPS_VERSION: 0.1.0-2409121919.dev
OPS_BRANCH: 
OPS_BIN: /home/user/.ops/linux-arm64/bin
OPS_REPO: http://github.com/apache/openserverless-task
OPS_OLARIS: 80ad2045f0ffbfaa14fe4970d728752044d9e4a4
`

func TestParseOpsInfo(t *testing.T) {
	got := parseOpsInfo(opsInfoFixture)

	// Order must be ops's own order — the Configure table would otherwise
	// shuffle between reloads.
	want := []opsInfoEntry{
		{"OPS & OPS_CMD", "/home/user/.local/bin/ops"},
		{"OPS_VERSION", "0.1.0-2409121919.dev"},
		{"OPS_BRANCH", ""},
		{"OPS_BIN", "/home/user/.ops/linux-arm64/bin"},
		{"OPS_REPO", "http://github.com/apache/openserverless-task"},
		{"OPS_OLARIS", "80ad2045f0ffbfaa14fe4970d728752044d9e4a4"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseOpsInfoSkipsSeparatorlessLines(t *testing.T) {
	got := parseOpsInfo("no separator here\n\nOPS_VERSION: 1.2.3\n")
	if len(got) != 1 || got[0] != (opsInfoEntry{"OPS_VERSION", "1.2.3"}) {
		t.Fatalf("got %+v, want only the OPS_VERSION entry", got)
	}
}

// The footer shows only the shortened tasks hash, so the truncation is a
// contract, not a formatting detail.
func TestVersionReportsOpsInfoAndShortTasks(t *testing.T) {
	oldExpiry, oldInfo, oldTasks := expiryDate, opsInfo, opsTasks
	t.Cleanup(func() { expiryDate, opsInfo, opsTasks = oldExpiry, oldInfo, oldTasks })
	expiryDate = time.Date(2099, time.August, 31, 0, 0, 0, 0, time.UTC)

	opsInfo = parseOpsInfo(opsInfoFixture)
	full := opsInfoValue(opsInfo, "OPS_OLARIS")
	opsTasks = full[:opsTasksShortLen]

	recorder := httptest.NewRecorder()
	handleVersion(recorder, httptest.NewRequest("GET", "/api/version", nil))

	var got struct {
		Tasks   string         `json:"tasks"`
		OpsInfo []opsInfoEntry `json:"opsinfo"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Tasks != "80ad20" {
		t.Errorf("tasks = %q, want %q", got.Tasks, "80ad20")
	}
	if len(got.OpsInfo) != len(opsInfo) {
		t.Fatalf("opsinfo has %d entries, want %d", len(got.OpsInfo), len(opsInfo))
	}
	if got.OpsInfo[0].Key != "OPS & OPS_CMD" {
		t.Errorf("first opsinfo key = %q, want the spaced key intact", got.OpsInfo[0].Key)
	}
	if opsInfoValue(got.OpsInfo, "OPS_VERSION") != "0.1.0-2409121919.dev" {
		t.Errorf("ops version not reachable from opsinfo: %+v", got.OpsInfo)
	}
}

// ops may be absent entirely; /api/version must still answer, just without the
// two fields carrying content.
func TestVersionOmitsOpsInfoWhenProbeFailed(t *testing.T) {
	oldExpiry, oldInfo, oldTasks := expiryDate, opsInfo, opsTasks
	t.Cleanup(func() { expiryDate, opsInfo, opsTasks = oldExpiry, oldInfo, oldTasks })
	expiryDate = time.Date(2099, time.August, 31, 0, 0, 0, 0, time.UTC)
	opsInfo, opsTasks = nil, ""

	recorder := httptest.NewRecorder()
	handleVersion(recorder, httptest.NewRequest("GET", "/api/version", nil))

	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["tasks"] != "" {
		t.Errorf("tasks = %v, want empty", got["tasks"])
	}
	if got["opsinfo"] != nil {
		t.Errorf("opsinfo = %v, want null", got["opsinfo"])
	}
	if got["build"] == nil {
		t.Error("build missing: the rest of the payload must survive a failed probe")
	}
}
