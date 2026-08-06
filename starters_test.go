package main

import (
	"encoding/json"
	"testing"
)

func TestParseStarterDescription(t *testing.T) {
	tests := []struct {
		name        string
		description string
		wantOK      bool
		wantText    string
		wantParams  map[string]string
	}{
		{
			name:        "plain starter",
			description: "Trustable: A React starter",
			wantOK:      true,
			wantText:    "A React starter",
		},
		{
			name:        "templates parameter is extracted and stripped",
			description: "Trustable: A React starter templates=trustable-ai/react-templates",
			wantOK:      true,
			wantText:    "A React starter",
			wantParams:  map[string]string{"templates": "trustable-ai/react-templates"},
		},
		{
			name:        "multiple keys stripped, unknown keys ignored",
			description: "Trustable: Full stack templates=org/tpl runtime=node app",
			wantOK:      true,
			wantText:    "Full stack app",
			wantParams:  map[string]string{"templates": "org/tpl", "runtime": "node"},
		},
		{
			name:        "prefix match is case insensitive",
			description: "TRUSTABLE: Shouty starter",
			wantOK:      true,
			wantText:    "Shouty starter",
		},
		{
			name:        "leading and inner whitespace collapsed",
			description: "  Trustable:    spaced    out   starter  ",
			wantOK:      true,
			wantText:    "spaced out starter",
		},
		{
			name:        "description only, no text after marker",
			description: "Trustable:",
			wantOK:      true,
			wantText:    "",
		},
		{
			name:        "missing colon is not a starter",
			description: "Trustable react starter",
			wantOK:      false,
		},
		{
			name:        "marker must be at the start",
			description: "A repo for Trustable: things",
			wantOK:      false,
		},
		{
			name:        "empty description is not a starter",
			description: "",
			wantOK:      false,
		},
		{
			name:        "unrelated description is not a starter",
			description: "Internal tooling",
			wantOK:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, params, ok := parseStarterDescription(test.description)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}
			if !ok {
				return
			}
			if text != test.wantText {
				t.Errorf("text = %q, want %q", text, test.wantText)
			}
			for key, want := range test.wantParams {
				if params[key] != want {
					t.Errorf("params[%q] = %q, want %q", key, params[key], want)
				}
			}
			if len(test.wantParams) == 0 && len(params) != 0 {
				t.Errorf("params = %v, want empty", params)
			}
		})
	}
}

func TestStartersFromRepos(t *testing.T) {
	repos := []githubPublicRepo{
		{Name: "trureact", FullName: "trustable-ai/trureact", Description: "Trustable: React starter"},
		{Name: "internal", FullName: "trustable-ai/internal", Description: "Not a starter"},
		{Name: "truvue", FullName: "trustable-ai/truvue", Description: "Trustable: Vue starter templates=trustable-ai/vue-templates"},
		{Name: "secret", FullName: "trustable-ai/secret", Description: "Trustable: Private", Private: true},
		{Name: "old", FullName: "trustable-ai/old", Description: "Trustable: Archived", Archived: true},
	}

	starters := startersFromRepos(repos)
	if len(starters) != 2 {
		t.Fatalf("got %d starters, want 2: %+v", len(starters), starters)
	}
	// Sorted by name: trureact before truvue.
	if starters[0].Name != "trureact" || starters[1].Name != "truvue" {
		t.Fatalf("unexpected order: %+v", starters)
	}
	if starters[0].Repo != "trustable-ai/trureact" {
		t.Errorf("repo = %q, want trustable-ai/trureact", starters[0].Repo)
	}
	// No templates= token: falls back to the global default.
	if starters[0].Templates != defaultStarterTemplates {
		t.Errorf("templates = %q, want %q", starters[0].Templates, defaultStarterTemplates)
	}
	if starters[1].Templates != "trustable-ai/vue-templates" {
		t.Errorf("templates = %q, want trustable-ai/vue-templates", starters[1].Templates)
	}
	if starters[1].Description != "Vue starter" {
		t.Errorf("description = %q, want %q", starters[1].Description, "Vue starter")
	}
}

func TestStartersFromReposInvalidTemplatesFallsBack(t *testing.T) {
	repos := []githubPublicRepo{
		{Name: "trubad", FullName: "trustable-ai/trubad", Description: "Trustable: Bad templates templates=not-a-repo-path/with/too/many/parts"},
		{Name: "truhttp", FullName: "trustable-ai/truhttp", Description: "Trustable: URL form templates=https://github.com/org/tpl"},
	}

	starters := startersFromRepos(repos)
	if len(starters) != 2 {
		t.Fatalf("got %d starters, want 2", len(starters))
	}
	if starters[0].Templates != defaultStarterTemplates {
		t.Errorf("malformed templates = %q, want fallback %q", starters[0].Templates, defaultStarterTemplates)
	}
	// A full GitHub URL is normalized to owner/repo by normalizeNotebookRepository.
	if starters[1].Templates != "org/tpl" {
		t.Errorf("url templates = %q, want org/tpl", starters[1].Templates)
	}
}

func TestStartersFromReposDerivesFullName(t *testing.T) {
	// A payload missing full_name still yields a usable org-qualified repo.
	starters := startersFromRepos([]githubPublicRepo{
		{Name: "trureact", Description: "Trustable: React starter"},
	})
	if len(starters) != 1 {
		t.Fatalf("got %d starters, want 1", len(starters))
	}
	if starters[0].Repo != "trustable-ai/trureact" {
		t.Errorf("repo = %q, want trustable-ai/trureact", starters[0].Repo)
	}
}

func TestStartersDecodeGitHubPayload(t *testing.T) {
	// Guards the field tags against the real payload shape.
	payload := `[{"name":"trureact","full_name":"trustable-ai/trureact",
	  "description":"Trustable: React starter","private":false,"archived":false,"disabled":false}]`
	var repos []githubPublicRepo
	if err := json.Unmarshal([]byte(payload), &repos); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}
	starters := startersFromRepos(repos)
	if len(starters) != 1 || starters[0].Name != "trureact" {
		t.Fatalf("unexpected starters: %+v", starters)
	}
}
