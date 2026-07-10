package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalWorkbenchPathResolvesSymlinkParent(t *testing.T) {
	root := t.TempDir()
	workspaceWorkbench := filepath.Join(root, "workspace", "workbench")
	if err := os.MkdirAll(filepath.Join(workspaceWorkbench, "existing"), 0755); err != nil {
		t.Fatalf("create persistent workbench: %s", err)
	}

	link := filepath.Join(root, "workbench")
	if err := os.Symlink(workspaceWorkbench, link); err != nil {
		t.Fatalf("create workbench symlink: %s", err)
	}

	origWorkbench := WorkbenchDir
	t.Cleanup(func() { WorkbenchDir = origWorkbench })
	WorkbenchDir = link

	existing, err := canonicalWorkbenchPath("existing")
	if err != nil {
		t.Fatalf("canonical existing path: %s", err)
	}
	if want := filepath.Join(workspaceWorkbench, "existing"); existing != want {
		t.Fatalf("canonical existing path = %q, want %q", existing, want)
	}

	missing, err := canonicalWorkbenchPath("missing")
	if err != nil {
		t.Fatalf("canonical missing path: %s", err)
	}
	if want := filepath.Join(workspaceWorkbench, "missing"); missing != want {
		t.Fatalf("canonical missing path = %q, want %q", missing, want)
	}
}

func TestChooseOpenCodeSessionIDPrefersNewestActiveSession(t *testing.T) {
	sessions := []opencodeSession{
		{
			ID:    "empty-new",
			Title: "New session - 2026-07-07T06:52:03.334Z",
			Time: struct {
				Created int64 `json:"created"`
				Updated int64 `json:"updated"`
			}{Updated: 200},
		},
		{
			ID:    "active-old",
			Title: "\"Homepage E2E visible on this page\" [Headline]",
			Time: struct {
				Created int64 `json:"created"`
				Updated int64 `json:"updated"`
			}{Updated: 100},
		},
	}

	if got := chooseOpenCodeSessionID(sessions); got != "active-old" {
		t.Fatalf("chosen session = %q, want active-old", got)
	}
}

func TestHandleOpenCodeSessionsReturnsPersistentHistory(t *testing.T) {
	origWorkbench := WorkbenchDir
	origLister := listOpenCodeSessionsForUI
	t.Cleanup(func() {
		WorkbenchDir = origWorkbench
		listOpenCodeSessionsForUI = origLister
	})

	WorkbenchDir = t.TempDir()
	app := "truapp"
	appDir := filepath.Join(WorkbenchDir, app)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("create app workbench: %s", err)
	}

	listOpenCodeSessionsForUI = func(host string, port int, directory string) []opencodeSession {
		if host != localLoopbackHost || port != opencodePort {
			t.Fatalf("unexpected OpenCode target %s:%d", host, port)
		}
		if directory != appDir {
			t.Fatalf("session directory = %q, want %q", directory, appDir)
		}
		oldSession := opencodeSession{ID: "ses_old", Title: "Older session"}
		oldSession.Time.Updated = 100
		newSession := opencodeSession{ID: "ses_new", Title: "Newest session"}
		newSession.Time.Updated = 200
		return []opencodeSession{oldSession, newSession}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/opencode/sessions/"+app, nil)
	rec := httptest.NewRecorder()
	handleOpenCodeSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var sessions []opencodeSession
	if err := json.NewDecoder(rec.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode sessions: %s", err)
	}
	if len(sessions) != 2 || sessions[0].ID != "ses_new" || sessions[1].ID != "ses_old" {
		t.Fatalf("unexpected sessions: %#v", sessions)
	}
}

func TestHandleOpenCodeSessionsReportsUnavailableHistory(t *testing.T) {
	origWorkbench := WorkbenchDir
	origLister := listOpenCodeSessionsForUI
	t.Cleanup(func() {
		WorkbenchDir = origWorkbench
		listOpenCodeSessionsForUI = origLister
	})

	WorkbenchDir = t.TempDir()
	app := "truapp"
	if err := os.MkdirAll(filepath.Join(WorkbenchDir, app), 0755); err != nil {
		t.Fatalf("create app workbench: %s", err)
	}
	listOpenCodeSessionsForUI = func(string, int, string) []opencodeSession { return nil }

	req := httptest.NewRequest(http.MethodGet, "/api/opencode/sessions/"+app, nil)
	rec := httptest.NewRecorder()
	handleOpenCodeSessions(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestIsPortListeningDetectsIPv4LoopbackListener(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on IPv4 loopback: %s", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	if !isPortListening(port) {
		t.Fatalf("expected IPv4 loopback listener on port %d to be detected", port)
	}
}
