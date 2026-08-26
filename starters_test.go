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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeStarters(t *testing.T) {
	entries := []starter{
		// Out of order on purpose: the result must be sorted by name.
		{Name: "truvue", Repo: "trustable-ai/truvue", Templates: "trustable-ai/vue-templates", Description: "Vue starter"},
		{Name: "trureact", Repo: "trustable-ai/trureact", Description: "React   starter"},
		{Name: "", Repo: "trustable-ai/noname", Description: "missing name"},
		{Name: "badrepo", Repo: "not-a-repo", Description: "unusable repo"},
	}

	starters := sanitizeStarters(entries)
	if len(starters) != 2 {
		t.Fatalf("got %d starters, want 2: %+v", len(starters), starters)
	}
	if starters[0].Name != "trureact" || starters[1].Name != "truvue" {
		t.Fatalf("not sorted by name: %+v", starters)
	}
	// No templates in the index entry: falls back to the global default.
	if starters[0].Templates != defaultNotebookRepository {
		t.Errorf("templates = %q, want %q", starters[0].Templates, defaultNotebookRepository)
	}
	if starters[1].Templates != "trustable-ai/vue-templates" {
		t.Errorf("templates = %q, want trustable-ai/vue-templates", starters[1].Templates)
	}
	// Whitespace in the published description is collapsed.
	if starters[0].Description != "React starter" {
		t.Errorf("description = %q, want %q", starters[0].Description, "React starter")
	}
}

func TestSanitizeStartersInvalidTemplatesFallsBack(t *testing.T) {
	starters := sanitizeStarters([]starter{
		{Name: "trubad", Repo: "trustable-ai/trubad", Templates: "too/many/parts/here"},
		{Name: "truurl", Repo: "trustable-ai/truurl", Templates: "https://github.com/org/tpl"},
	})
	if len(starters) != 2 {
		t.Fatalf("got %d starters, want 2", len(starters))
	}
	if starters[0].Templates != defaultNotebookRepository {
		t.Errorf("malformed templates = %q, want fallback %q", starters[0].Templates, defaultNotebookRepository)
	}
	// A full GitHub URL is normalized to owner/repo.
	if starters[1].Templates != "org/tpl" {
		t.Errorf("url templates = %q, want org/tpl", starters[1].Templates)
	}
}

func TestStarterIndexDecodesPublishedShape(t *testing.T) {
	// Guards the field tags against the document support/index.py publishes.
	payload := `{
	  "generated": "2026-08-06T07:33:04Z",
	  "starters": [
	    {"name":"trureact","repo":"trustable-ai/trureact",
	     "templates":"trustable-ai/trureact-templates","description":"React Generic Starter"}
	  ]
	}`
	var index starterIndex
	if err := json.Unmarshal([]byte(payload), &index); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}
	starters := sanitizeStarters(index.Starters)
	if len(starters) != 1 {
		t.Fatalf("got %d starters, want 1", len(starters))
	}
	if starters[0].Repo != "trustable-ai/trureact" ||
		starters[0].Templates != "trustable-ai/trureact-templates" {
		t.Fatalf("unexpected starter: %+v", starters[0])
	}
}

func TestSanitizeApplications(t *testing.T) {
	groups := map[string][]application{
		"Apps": {
			// The published repo is a full URL; it must come out as owner/repo.
			{Name: "tetris", Title: "Tetris", Repo: "https://github.com/trustable-ai/tetris",
				Icon:        "https://raw.githubusercontent.com/trustable-ai/t/main/tetris.png",
				Description: "Falling   blocks"},
			// No icon yet: kept, the tile falls back to a placeholder.
			{Name: "noicon", Title: "No Icon", Repo: "https://github.com/trustable-ai/noicon"},
			// No title: defaults to the name so the tile always has a label.
			{Name: "notitle", Repo: "trustable-ai/notitle"},
			{Name: "", Title: "Nameless", Repo: "https://github.com/trustable-ai/nameless"},
			{Name: "badrepo", Title: "Bad Repo", Repo: "https://example.com/not/a/repo"},
			{Name: "badicon", Title: "Bad Icon", Repo: "https://github.com/trustable-ai/badicon",
				Icon: "javascript:alert(1)"},
		},
		// Every entry is dropped, so the group disappears entirely.
		"Empty": {{Name: "gone", Repo: "nope"}},
		"  ":    {{Name: "blankgroup", Repo: "trustable-ai/blankgroup"}},
	}

	sanitized := sanitizeApplications(groups)
	if len(sanitized) != 1 {
		t.Fatalf("got %d groups, want 1: %+v", len(sanitized), sanitized)
	}
	apps, ok := sanitized["Apps"]
	if !ok {
		t.Fatalf("group Apps missing: %+v", sanitized)
	}
	if len(apps) != 3 {
		t.Fatalf("got %d applications, want 3: %+v", len(apps), apps)
	}
	// Published order within a group is preserved.
	if apps[0].Name != "tetris" || apps[1].Name != "noicon" || apps[2].Name != "notitle" {
		t.Fatalf("order not preserved: %+v", apps)
	}
	if apps[0].Repo != "trustable-ai/tetris" {
		t.Errorf("repo = %q, want trustable-ai/tetris", apps[0].Repo)
	}
	if apps[0].Description != "Falling blocks" {
		t.Errorf("description = %q, want %q", apps[0].Description, "Falling blocks")
	}
	if apps[1].Icon != "" {
		t.Errorf("empty icon = %q, want kept empty", apps[1].Icon)
	}
	if apps[2].Title != "notitle" {
		t.Errorf("title = %q, want it to default to the name", apps[2].Title)
	}
}

func TestNormalizeApplicationRepo(t *testing.T) {
	cases := map[string]string{
		"https://github.com/trustable-ai/tetris":     "trustable-ai/tetris",
		"https://github.com/trustable-ai/tetris.git": "trustable-ai/tetris",
		"https://github.com/trustable-ai/tetris/":    "trustable-ai/tetris",
		"  trustable-ai/tetris  ":                    "trustable-ai/tetris",
		"https://gitlab.com/trustable-ai/tetris":     "",
		"trustable-ai":                               "",
		"":                                           "",
	}
	for input, want := range cases {
		if got := normalizeApplicationRepo(input); got != want {
			t.Errorf("normalizeApplicationRepo(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStarterIndexDecodesApplications(t *testing.T) {
	// Guards the field tags against the document support/index.py publishes.
	payload := `{
	  "generated": "2026-08-12T13:08:41Z",
	  "starters": [],
	  "applications": {
	    "Apps": [
	      {"name":"apistatusmonitor","title":"API Status Monitor",
	       "repo":"https://github.com/trustable-ai/apistatusmonitor",
	       "icon":"https://raw.githubusercontent.com/trustable-ai/trureact-templates/refs/heads/main/apistatusmonitor.png",
	       "description":"API Status Monitor"}
	    ]
	  }
	}`
	var index starterIndex
	if err := json.Unmarshal([]byte(payload), &index); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}
	apps := sanitizeApplications(index.Applications)["Apps"]
	if len(apps) != 1 {
		t.Fatalf("got %d applications, want 1", len(apps))
	}
	if apps[0].Name != "apistatusmonitor" || apps[0].Title != "API Status Monitor" ||
		apps[0].Repo != "trustable-ai/apistatusmonitor" {
		t.Fatalf("unexpected application: %+v", apps[0])
	}
}

func TestSanitizeApplicationsWithoutKeyIsEmptyNotNil(t *testing.T) {
	// An index with no "applications" key must still yield an object the
	// frontend can iterate.
	var index starterIndex
	if err := json.Unmarshal([]byte(`{"starters":[]}`), &index); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}
	applications := sanitizeApplications(index.Applications)
	if applications == nil {
		t.Fatal("applications = nil, want an empty map")
	}
	if len(applications) != 0 {
		t.Fatalf("got %d groups, want 0", len(applications))
	}
}

func TestHandleStartersEmitsApplicationsObject(t *testing.T) {
	// A valid index carrying no applications must still serialize
	// "applications" as {}, never null: the frontend iterates it
	// unconditionally. The cache is primed so the test never hits the network.
	startersCache.Lock()
	list, applications, fetchedAt := startersCache.list, startersCache.applications, startersCache.fetchedAt
	startersCache.list = []starter{}
	startersCache.applications = sanitizeApplications(nil)
	startersCache.fetchedAt = time.Now()
	startersCache.Unlock()
	defer func() {
		startersCache.Lock()
		startersCache.list, startersCache.applications, startersCache.fetchedAt = list, applications, fetchedAt
		startersCache.Unlock()
	}()

	rec := httptest.NewRecorder()
	handleStarters(rec, httptest.NewRequest(http.MethodGet, "/api/starters", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"applications":`)) {
		t.Fatalf("response has no applications key: %s", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`"applications":null`)) {
		t.Fatalf("applications serialized as null: %s", rec.Body.String())
	}
	var response struct {
		Applications map[string][]application `json:"applications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %s", err)
	}
	if response.Applications == nil {
		t.Fatal("applications decoded as nil, want an object")
	}
}

func TestHandleStartersRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	handleStarters(rec, httptest.NewRequest(http.MethodPost, "/api/starters", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// The index host is load-bearing, not cosmetic. raw.githubusercontent.com
// rate-limits and was observed returning 429, which makes fetchStarterIndex
// fail and leaves the Add Application modal empty; the icon URLs carried inside
// the index point at the same host, so the browser's per-tile requests are
// throttled with it. This pins the endpoint so it cannot regress by a careless
// edit or a revert.
func TestStartersIndexURLIsNotRateLimitedHost(t *testing.T) {
	if !strings.HasPrefix(startersIndexURL, "https://trustable.it/") {
		t.Errorf("startersIndexURL = %q, want it served from trustable.it", startersIndexURL)
	}
	if strings.Contains(startersIndexURL, "raw.githubusercontent.com") {
		t.Errorf("startersIndexURL points back at the rate-limited host: %q", startersIndexURL)
	}
}
