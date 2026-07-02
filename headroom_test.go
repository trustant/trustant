package main

import (
	"path/filepath"
	"testing"
)

func withWorkspaceDir(t *testing.T, dir string) {
	t.Helper()
	old := WorkspaceDir
	WorkspaceDir = dir
	t.Cleanup(func() {
		WorkspaceDir = old
	})
}

func TestHeadroomConfigDefaultsDisabled(t *testing.T) {
	withWorkspaceDir(t, t.TempDir())

	cfg, err := headroomConfigFromEnv()
	if err != nil {
		t.Fatalf("headroomConfigFromEnv returned error: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("Headroom should be disabled by default")
	}
	if cfg.Mode != "proxy" {
		t.Fatalf("default mode = %q, want proxy", cfg.Mode)
	}
	if cfg.Port != defaultHeadroomPort {
		t.Fatalf("default port = %d, want %d", cfg.Port, defaultHeadroomPort)
	}
	wantState := filepath.Join(WorkspaceDir, ".trustable", "headroom")
	if cfg.StateDir != wantState {
		t.Fatalf("default state dir = %q, want %q", cfg.StateDir, wantState)
	}
}

func TestHeadroomConfigEnabledCustomValues(t *testing.T) {
	withWorkspaceDir(t, t.TempDir())
	t.Setenv("TRUSTABLE_HEADROOM_ENABLED", "yes")
	t.Setenv("TRUSTABLE_HEADROOM_MODE", "proxy")
	t.Setenv("TRUSTABLE_HEADROOM_PORT", "18787")
	t.Setenv("TRUSTABLE_HEADROOM_STATE_DIR", "/tmp/headroom-state")

	cfg, err := headroomConfigFromEnv()
	if err != nil {
		t.Fatalf("headroomConfigFromEnv returned error: %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("Headroom should be enabled")
	}
	if cfg.Mode != "proxy" {
		t.Fatalf("mode = %q, want proxy", cfg.Mode)
	}
	if cfg.Port != 18787 {
		t.Fatalf("port = %d, want 18787", cfg.Port)
	}
	if cfg.StateDir != "/tmp/headroom-state" {
		t.Fatalf("state dir = %q, want /tmp/headroom-state", cfg.StateDir)
	}
}

func TestHeadroomConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{name: "bool", key: "TRUSTABLE_HEADROOM_ENABLED", val: "maybe"},
		{name: "mode", key: "TRUSTABLE_HEADROOM_MODE", val: "wrap"},
		{name: "port", key: "TRUSTABLE_HEADROOM_PORT", val: "70000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withWorkspaceDir(t, t.TempDir())
			t.Setenv("TRUSTABLE_HEADROOM_ENABLED", "true")
			t.Setenv(tt.key, tt.val)
			if _, err := headroomConfigFromEnv(); err == nil {
				t.Fatalf("expected error for %s=%q", tt.key, tt.val)
			}
		})
	}
}
