package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestHandleStartersRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	handleStarters(rec, httptest.NewRequest(http.MethodPost, "/api/starters", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
