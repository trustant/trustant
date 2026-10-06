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

// Shared variables on publish (issue #9): the host → app → secrets store, the
// production resolution of imports, and the publish gate built on it.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- the store ------------------------------------------------------------

func TestLegacyProductionPoolIsMigratedAndDropped(t *testing.T) {
	isolateSharedWorkspace(t)
	legacy := `{
  "apps": {"appsuite": {"password": "p"}, "billing": {"password": "p"}},
  "predefined_env_production": {
    "https://api.nuvolaris.io/": {"APPSUITE__DB": "a", "BILLING__DB": "b", "GONE__DB": "orphan", "TYPED": "hand"}
  }
}`
	if err := os.WriteFile(filepath.Join(WorkspaceDir, "trustant.json"), []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy config: %s", err)
	}

	cfg, err := loadWorkspaceConfig()
	if err != nil {
		t.Fatalf("load: %s", err)
	}
	host := cfg.SharedProduction["api.nuvolaris.io"]
	if host["appsuite"]["APPSUITE__DB"] != "a" || host["billing"]["BILLING__DB"] != "b" {
		t.Fatalf("not split by producer: %#v", cfg.SharedProduction)
	}
	// Orphans (no such app) and hand-typed names have no owner: dropped.
	if len(host) != 2 {
		t.Fatalf("orphans survived the migration: %#v", host)
	}

	if err := saveWorkspaceConfig(cfg); err != nil {
		t.Fatalf("save: %s", err)
	}
	data, _ := os.ReadFile(filepath.Join(WorkspaceDir, "trustant.json"))
	if strings.Contains(string(data), "predefined_env_production") {
		t.Fatalf("the legacy key survived a save:\n%s", data)
	}
	if !strings.Contains(string(data), "shared_production") {
		t.Fatalf("the new key was not written:\n%s", data)
	}
}

func TestResolveProductionSharedReplacesTheAppsSecrets(t *testing.T) {
	isolateSharedWorkspace(t)
	makeWorkbench(t, "appsuite")
	saveWorkspaceConfig(&trustantConfig{
		Apps: map[string]*AppConfig{"appsuite": {}},
		SharedProduction: map[string]map[string]map[string]string{
			"openserverless.dev": {"appsuite": {"APPSUITE__OLD": "stale"}},
		},
	})
	if err := saveSharedEnv("appsuite", map[string]string{"APPSUITE__DB": "<.postgres.url>"}); err != nil {
		t.Fatalf("save .env.shared: %s", err)
	}
	writeOpsConfigTree(t, `{"postgres": {"url": "postgres://prod"}}`)

	if _, err := resolveProductionShared("appsuite", "https://openserverless.dev"); err != nil {
		t.Fatalf("resolve: %s", err)
	}
	cfg, _ := loadWorkspaceConfig()
	got := cfg.SharedProduction["openserverless.dev"]["appsuite"]
	if got["APPSUITE__DB"] != "postgres://prod" {
		t.Fatalf("new secret not stored: %#v", got)
	}
	if _, ok := got["APPSUITE__OLD"]; ok {
		t.Fatalf("a secret the producer no longer exports survived: %#v", got)
	}
}

// --- production resolution ------------------------------------------------

func productionTestConfig(dev, prod map[string]string) *trustantConfig {
	return &trustantConfig{
		Apps: map[string]*AppConfig{
			"appsuite": {},
			"billing":  {},
			"demoapp":  {Password: "p", Development: dev, Production: prod},
		},
		SharedProduction: map[string]map[string]map[string]string{
			"openserverless.dev": {
				"appsuite": {"APPSUITE__POSTGRESDB": "postgres://appsuite-prod"},
				"billing":  {"BILLING__POSTGRESDB": "postgres://billing-prod"},
			},
			"api.nuvolaris.io": {
				"appsuite": {"APPSUITE__REDIS": "redis://other-cluster"},
			},
		},
	}
}

func setupProductionApp(t *testing.T, cfg *trustantConfig, dist string) string {
	t.Helper()
	isolateSharedWorkspace(t)
	wb := makeWorkbench(t, "demoapp")
	if dist != "" {
		if err := os.WriteFile(filepath.Join(wb, ".env.dist"), []byte(dist), 0644); err != nil {
			t.Fatalf("write .env.dist: %s", err)
		}
	}
	if err := saveWorkspaceConfig(cfg); err != nil {
		t.Fatalf("save config: %s", err)
	}
	return wb
}

func productionByName(t *testing.T, host string) map[string]ImportResolution {
	t.Helper()
	got, err := resolveProductionImports("demoapp", host)
	if err != nil {
		t.Fatalf("resolveProductionImports: %s", err)
	}
	out := map[string]ImportResolution{}
	for _, r := range got {
		out[r.Name] = r
	}
	return out
}

func TestProductionImportPrecedence(t *testing.T) {
	cfg := productionTestConfig(
		map[string]string{
			"CHOSEN":          importRef("BILLING__POSTGRESDB"),
			"MISSING_ON_HOST": importRef("APPSUITE__REDIS"),
			"REF_ONLY":        importRef("APPSUITE__POSTGRESDB"),
		},
		map[string]string{
			"OPS_APIHOST": "https://openserverless.dev",
			"LITERAL":     "typed for production",
		},
	)
	setupProductionApp(t, cfg, "LITERAL=*__POSTGRESDB\nCHOSEN=*__POSTGRESDB\nMISSING_ON_HOST=*__REDIS\nFRESH=*__POSTGRESDB\n")

	byName := productionByName(t, "openserverless.dev")

	if r := byName["LITERAL"]; r.Pending || r.Value != "typed for production" {
		t.Errorf("a production literal must win: %+v", r)
	}
	// The dev choice is followed on the host pool.
	if r := byName["CHOSEN"]; r.Pending || r.Source != "BILLING__POSTGRESDB" || r.Value != "postgres://billing-prod" {
		t.Errorf("dev choice not followed: %+v", r)
	}
	// Another host's value is never used; the producer is named.
	if r := byName["MISSING_ON_HOST"]; !r.Pending || r.Producer != "appsuite" || r.Value != "" {
		t.Errorf("missing on host = %+v, want pending naming appsuite", r)
	}
	// Nothing recorded, two matches: pending, no suggestion.
	if r := byName["FRESH"]; !r.Pending || len(r.Matches) != 2 {
		t.Errorf("fresh wildcard = %+v", r)
	}
	// A dev ${{ref}} not declared in .env.dist is an import too.
	if r := byName["REF_ONLY"]; r.Pending || r.Value != "postgres://appsuite-prod" {
		t.Errorf("dev-only reference = %+v", r)
	}
}

func TestProductionFirstResolutionIsPendingButSuggested(t *testing.T) {
	cfg := productionTestConfig(nil, map[string]string{"OPS_APIHOST": "openserverless.dev"})
	setupProductionApp(t, cfg, "ONE=*__REDIS\nEXACT=APPSUITE__POSTGRESDB\n")

	byName := productionByName(t, "api.nuvolaris.io")
	if r := byName["ONE"]; !r.Pending || r.Suggested != "APPSUITE__REDIS" {
		t.Errorf("single wildcard match = %+v, want pending + suggested", r)
	}
	if r := byName["EXACT"]; !r.Pending || r.Suggested != "" || r.Producer != "appsuite" {
		t.Errorf("exact miss = %+v, want pending naming appsuite", r)
	}

	if err := applyImportChoices("demoapp", map[string]string{"ONE": "APPSUITE__REDIS"}, map[string]string{"EXACT": "typed"}, true, "api.nuvolaris.io"); err != nil {
		t.Fatalf("apply: %s", err)
	}
	byName = productionByName(t, "api.nuvolaris.io")
	if r := byName["ONE"]; r.Pending || r.Value != "redis://other-cluster" {
		t.Errorf("confirmed choice must resolve silently: %+v", r)
	}
	if r := byName["EXACT"]; r.Pending || r.Value != "typed" {
		t.Errorf("typed value must resolve: %+v", r)
	}

	// Choices are validated against THAT host's pool.
	if err := applyImportChoices("demoapp", map[string]string{"ONE": "BILLING__POSTGRESDB"}, nil, true, "api.nuvolaris.io"); err == nil {
		t.Errorf("a choice from another host's pool was accepted")
	}
}

func TestDevelopmentFirstResolutionRePendsWhenProducerGoes(t *testing.T) {
	bindTestApp(t, map[string]string{}, map[string]string{"APPSUITE__REDIS": "redis://dev"}, "ONE=*__REDIS\n")
	if err := applyImportChoices("demo", map[string]string{"ONE": "APPSUITE__REDIS"}, nil, false, ""); err != nil {
		t.Fatalf("apply: %s", err)
	}
	if pending, _ := pendingImports("demo"); len(pending) != 0 {
		t.Fatalf("confirmed binding still pending: %+v", pending)
	}
	wsCfg, _ := loadWorkspaceConfig()
	pruneSharedPool(wsCfg, "appsuite")
	saveWorkspaceConfig(wsCfg)
	if pending, _ := pendingImports("demo"); len(pending) != 1 {
		t.Fatalf("a removed producer must re-pend: %+v", pending)
	}
}

func TestProductionEnvGetsResolvedImportsNeverReferences(t *testing.T) {
	cfg := productionTestConfig(
		map[string]string{"CHOSEN": importRef("BILLING__POSTGRESDB")},
		map[string]string{
			"OPS_APIHOST": "openserverless.dev",
			"PICKED":      importRef("APPSUITE__POSTGRESDB"),
		},
	)
	wb := setupProductionApp(t, cfg, "CHOSEN=*__POSTGRESDB\nPICKED=*__POSTGRESDB\n")

	if err := generateAppEnvFiles("demoapp"); err != nil {
		t.Fatalf("generate: %s", err)
	}
	body, err := os.ReadFile(filepath.Join(wb, ".env.production"))
	if err != nil {
		t.Fatalf("read .env.production: %s", err)
	}
	env := string(body)
	if !strings.Contains(env, "CHOSEN=postgres://billing-prod") {
		t.Errorf("dev choice not resolved into production:\n%s", env)
	}
	if !strings.Contains(env, "PICKED=postgres://appsuite-prod") {
		t.Errorf("production choice not resolved:\n%s", env)
	}
	if strings.Contains(env, importRefOpen) {
		t.Errorf("a reference leaked into .env.production:\n%s", env)
	}
}

// --- the publish gate -----------------------------------------------------

func TestPublishGateReturnsPendingImportsBeforeAnyOps(t *testing.T) {
	cfg := productionTestConfig(nil, map[string]string{
		"OPS_APIHOST":  "https://openserverless.dev",
		"OPS_USER":     "u",
		"OPS_PASSWORD": "p",
	})
	setupProductionApp(t, cfg, "EXT_URL=*__POSTGRESDB\n")
	origLicense := EnableLicense
	EnableLicense = false
	t.Cleanup(func() { EnableLicense = origLicense })
	dir := t.TempDir()
	guardPath(t, dir)

	publish := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handlePublishRemote(rec, httptest.NewRequest(http.MethodPost, "/api/publish/remote",
			strings.NewReader(`{"name":"demoapp"}`)))
		return rec
	}

	rec := publish()
	var resp struct {
		NeedsConfig    bool               `json:"needs_config"`
		PendingImports []ImportResolution `json:"pending_imports"`
		Host           string             `json:"host"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %s", rec.Body.String(), err)
	}
	if !resp.NeedsConfig || len(resp.PendingImports) != 1 || resp.PendingImports[0].Name != "EXT_URL" {
		t.Fatalf("gate response = %s", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "ops-was-run")); err == nil {
		t.Fatalf("ops ran before the gate")
	}

	// Answer the popup for this host, then the gate passes (and ops is reached).
	rec = httptest.NewRecorder()
	handleImports(rec, httptest.NewRequest(http.MethodPost,
		"/api/imports/demoapp?mode=production&host=https://openserverless.dev",
		strings.NewReader(`{"choices":{"EXT_URL":"BILLING__POSTGRESDB"}}`)))
	if rec.Code != 200 {
		t.Fatalf("production choice: %d %s", rec.Code, rec.Body.String())
	}
	saved, _ := loadWorkspaceConfig()
	if got := saved.Apps["demoapp"].Production["EXT_URL"]; got != importRef("BILLING__POSTGRESDB") {
		t.Fatalf("choice not recorded in production: %q", got)
	}

	rec = publish()
	if strings.Contains(rec.Body.String(), "pending_imports") {
		t.Fatalf("gate still blocks: %s", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "ops-was-run")); err != nil {
		t.Fatalf("publish did not get past the gate: %s", rec.Body.String())
	}
}
