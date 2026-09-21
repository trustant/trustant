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
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- pruneSharedPool ------------------------------------------------------

func TestPruneSharedPoolRemovesBothPoolsForOneApp(t *testing.T) {
	cfg := &trustableConfig{
		PredefinedEnv: map[string]string{
			"APPSUITE__DB": "postgres://appsuite",
			"APPSUITE__S3": "s3://appsuite",
			"BILLING__DB":  "postgres://billing",
			"MY_API_KEY":   "typed by the user",
			"NO_SEPARATOR": "also the user's",
		},
		PredefinedEnvProduction: map[string]map[string]string{
			"api.nuvolaris.io":   {"APPSUITE__DB": "prod", "BILLING__DB": "prod"},
			"openserverless.dev": {"APPSUITE__DB": "prod"},
		},
	}

	removed := pruneSharedPool(cfg, "appsuite")

	// Two development entries and two production entries.
	if removed != 4 {
		t.Fatalf("removed = %d, want 4", removed)
	}
	for _, gone := range []string{"APPSUITE__DB", "APPSUITE__S3"} {
		if _, ok := cfg.PredefinedEnv[gone]; ok {
			t.Fatalf("%s survived the prune", gone)
		}
	}
	// Another app's exports are none of this app's business.
	if cfg.PredefinedEnv["BILLING__DB"] != "postgres://billing" {
		t.Fatalf("another app's export was pruned")
	}
	// Hand-typed entries are the user's, separator or not.
	if cfg.PredefinedEnv["MY_API_KEY"] == "" || cfg.PredefinedEnv["NO_SEPARATOR"] == "" {
		t.Fatalf("a hand-typed entry was pruned: %v", cfg.PredefinedEnv)
	}
	if _, ok := cfg.PredefinedEnvProduction["api.nuvolaris.io"]["APPSUITE__DB"]; ok {
		t.Fatalf("production entry survived the prune")
	}
	if cfg.PredefinedEnvProduction["api.nuvolaris.io"]["BILLING__DB"] != "prod" {
		t.Fatalf("another app's production export was pruned")
	}
	// A host whose last entry went with the app leaves no empty map behind:
	// the workspace file uses omitempty to stay small.
	if _, ok := cfg.PredefinedEnvProduction["openserverless.dev"]; ok {
		t.Fatalf("emptied host map was kept: %v", cfg.PredefinedEnvProduction)
	}
}

func TestPruneSharedPoolIsCaseInsensitiveOnTheAppName(t *testing.T) {
	// The pool name is uppercased by sharedVarPrefix; the app is stored as the
	// user typed it. Matching must fold case or nothing would ever be pruned.
	cfg := &trustableConfig{PredefinedEnv: map[string]string{"MyApp__DB": "v", "MYAPP__S3": "v"}}
	if removed := pruneSharedPool(cfg, "myapp"); removed != 2 {
		t.Fatalf("removed = %d, want 2 (%v)", removed, cfg.PredefinedEnv)
	}
}

func TestPruneSharedPoolDoesNotMatchAPrefixOfAnotherApp(t *testing.T) {
	// splitSharedVarName splits on the FIRST separator, so the prefix here is
	// BILLING. Deleting an app called `bill` must not take it with it.
	cfg := &trustableConfig{PredefinedEnv: map[string]string{
		"BILLING__POSTGRESDB": "postgres://billing",
		"BILL__POSTGRESDB":    "postgres://bill",
	}}

	if removed := pruneSharedPool(cfg, "bill"); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if cfg.PredefinedEnv["BILLING__POSTGRESDB"] != "postgres://billing" {
		t.Fatalf("a different app's export was pruned by prefix collision: %v", cfg.PredefinedEnv)
	}
}

func TestPruneSharedPoolNilsAnEmptiedPool(t *testing.T) {
	cfg := &trustableConfig{
		PredefinedEnv:           map[string]string{"APPSUITE__DB": "v"},
		PredefinedEnvProduction: map[string]map[string]string{"h": {"APPSUITE__DB": "v"}},
	}
	pruneSharedPool(cfg, "appsuite")
	if cfg.PredefinedEnv != nil || cfg.PredefinedEnvProduction != nil {
		t.Fatalf("emptied pools were not nilled: %v %v", cfg.PredefinedEnv, cfg.PredefinedEnvProduction)
	}
}

func TestPruneSharedPoolIsANoOpForAnAppThatSharedNothing(t *testing.T) {
	cfg := &trustableConfig{PredefinedEnv: map[string]string{"APPSUITE__DB": "v"}}
	if removed := pruneSharedPool(cfg, "somethingelse"); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if len(cfg.PredefinedEnv) != 1 {
		t.Fatalf("the pool was touched: %v", cfg.PredefinedEnv)
	}
	// An empty name must never be read as "prune everything".
	if removed := pruneSharedPool(cfg, "   "); removed != 0 || len(cfg.PredefinedEnv) != 1 {
		t.Fatalf("a blank app name pruned something: %d %v", removed, cfg.PredefinedEnv)
	}
}

// --- removeSharedPoolVar --------------------------------------------------

func TestRemoveSharedPoolVarSpansEveryPool(t *testing.T) {
	cfg := &trustableConfig{
		PredefinedEnv: map[string]string{"APPSUITE__DB": "v", "OTHER": "v"},
		PredefinedEnvProduction: map[string]map[string]string{
			"api.nuvolaris.io":   {"APPSUITE__DB": "v", "OTHER": "v"},
			"openserverless.dev": {"APPSUITE__DB": "v"},
		},
	}

	// One development entry plus two production entries.
	if removed := removeSharedPoolVar(cfg, "APPSUITE__DB"); removed != 3 {
		t.Fatalf("removed = %d, want 3", removed)
	}
	if _, ok := cfg.PredefinedEnv["APPSUITE__DB"]; ok {
		t.Fatalf("development entry survived")
	}
	if _, ok := cfg.PredefinedEnvProduction["openserverless.dev"]; ok {
		t.Fatalf("emptied host map was kept")
	}
	if cfg.PredefinedEnvProduction["api.nuvolaris.io"]["OTHER"] != "v" {
		t.Fatalf("an unrelated entry was removed")
	}
}

func TestRemoveSharedPoolVarRemovesAHandTypedName(t *testing.T) {
	// The endpoint does not care who produced it; that distinction only governs
	// whether the removal is durable.
	cfg := &trustableConfig{PredefinedEnv: map[string]string{"MY_API_KEY": "v"}}
	if removed := removeSharedPoolVar(cfg, "MY_API_KEY"); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
}

// --- DELETE /api/predefined-env -------------------------------------------

func TestDeletePredefinedEnvEndpoint(t *testing.T) {
	isolateSharedWorkspace(t)
	saveWorkspaceConfig(&trustableConfig{
		Apps:          map[string]*AppConfig{"appsuite": {Password: "p"}},
		PredefinedEnv: map[string]string{"APPSUITE__DB": "secret", "MY_API_KEY": "v"},
		PredefinedEnvProduction: map[string]map[string]string{
			"api.nuvolaris.io": {"APPSUITE__DB": "prodsecret"},
		},
	})

	rec := httptest.NewRecorder()
	handleDeletePredefinedEnv(rec, httptest.NewRequest("DELETE", "/api/predefined-env?name=APPSUITE__DB", nil))
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Removed int `json:"removed"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Removed != 2 {
		t.Fatalf("removed = %d, want 2", got.Removed)
	}

	// It is the saved config that matters, not the response.
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		t.Fatalf("reload: %s", err)
	}
	if _, ok := wsCfg.PredefinedEnv["APPSUITE__DB"]; ok {
		t.Fatalf("app-produced entry survived the delete")
	}
	if wsCfg.PredefinedEnv["MY_API_KEY"] != "v" {
		t.Fatalf("an unrelated entry was lost")
	}
	if _, ok := wsCfg.PredefinedEnvProduction["api.nuvolaris.io"]; ok {
		t.Fatalf("production entry survived the delete")
	}
}

func TestDeletePredefinedEnvIsIdempotentAndNeedsAName(t *testing.T) {
	isolateSharedWorkspace(t)
	saveWorkspaceConfig(&trustableConfig{PredefinedEnv: map[string]string{"KEEP": "v"}})

	// Removing a name that is not there is success: a double-click is not an
	// error.
	rec := httptest.NewRecorder()
	handleDeletePredefinedEnv(rec, httptest.NewRequest("DELETE", "/api/predefined-env?name=NOT_THERE", nil))
	if rec.Code != 200 {
		t.Fatalf("absent name: %d %s", rec.Code, rec.Body.String())
	}

	// A missing name must not be read as "remove everything".
	rec = httptest.NewRecorder()
	handleDeletePredefinedEnv(rec, httptest.NewRequest("DELETE", "/api/predefined-env", nil))
	if rec.Code != 400 {
		t.Fatalf("missing name: %d, want 400", rec.Code)
	}
	wsCfg, _ := loadWorkspaceConfig()
	if wsCfg.PredefinedEnv["KEEP"] != "v" {
		t.Fatalf("the pool was emptied by a nameless delete: %v", wsCfg.PredefinedEnv)
	}
}

// TestBulkSaveStillCarriesAppProducedEntries guards the asymmetry this feature
// rests on: the DELETE is the only way to drop an app-produced entry, and the
// bulk POST must not quietly become a second one. It receives the whole set, so
// honouring an absent key there would let a stale tab drop a live export.
func TestBulkSaveStillCarriesAppProducedEntries(t *testing.T) {
	isolateSharedWorkspace(t)
	saveWorkspaceConfig(&trustableConfig{
		Apps:          map[string]*AppConfig{"appsuite": {Password: "p"}},
		PredefinedEnv: map[string]string{"APPSUITE__DB": "secret", "MY_API_KEY": "v"},
	})

	// A save that omits the app-produced row entirely.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/predefined-env",
		strings.NewReader(`{"vars":[{"name":"MY_API_KEY","value":"v"}]}`))
	handlePostPredefinedEnv(rec, req)
	if rec.Code != 200 {
		t.Fatalf("post: %d %s", rec.Code, rec.Body.String())
	}

	wsCfg, _ := loadWorkspaceConfig()
	if wsCfg.PredefinedEnv["APPSUITE__DB"] != "secret" {
		t.Fatalf("the bulk save dropped an app-produced entry: %v", wsCfg.PredefinedEnv)
	}
}

// --- deleting the app -----------------------------------------------------

// TestDeleteRepoPrunesWhatTheAppShared is the whole point of the issue: the
// pool is append-only, so without the prune a deleted app's resolved secrets
// outlive it.
func TestDeleteRepoPrunesWhatTheAppShared(t *testing.T) {
	isolateSharedWorkspace(t)
	// handleDeleteRepo requires the bare repo to exist before it will act.
	if err := os.MkdirAll(filepath.Join(WorkspaceDir, "workspace", "appsuite"), 0755); err != nil {
		t.Fatalf("mkdir bare repo: %s", err)
	}
	saveWorkspaceConfig(&trustableConfig{
		Apps: map[string]*AppConfig{
			"appsuite": {Password: "p"},
			"billing":  {Password: "p"},
		},
		PredefinedEnv: map[string]string{
			"APPSUITE__DB": "postgres://user:pass@appsuite",
			"BILLING__DB":  "postgres://billing",
			"MY_API_KEY":   "typed by the user",
		},
		PredefinedEnvProduction: map[string]map[string]string{
			"api.nuvolaris.io": {"APPSUITE__DB": "prodsecret", "BILLING__DB": "prod"},
		},
	})

	rec := httptest.NewRecorder()
	handleDeleteRepo(rec, httptest.NewRequest("DELETE", "/api/repo",
		strings.NewReader(`{"name":"appsuite"}`)))
	if rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		t.Fatalf("reload: %s", err)
	}
	if _, ok := wsCfg.Apps["appsuite"]; ok {
		t.Fatalf("the app entry survived")
	}
	if _, ok := wsCfg.PredefinedEnv["APPSUITE__DB"]; ok {
		t.Fatalf("a deleted app's secret is still in the development pool: %v", wsCfg.PredefinedEnv)
	}
	if _, ok := wsCfg.PredefinedEnvProduction["api.nuvolaris.io"]["APPSUITE__DB"]; ok {
		t.Fatalf("a deleted app's secret is still in the production pool")
	}
	// Everything else is untouched, in the same single save.
	if wsCfg.PredefinedEnv["BILLING__DB"] == "" || wsCfg.PredefinedEnv["MY_API_KEY"] == "" {
		t.Fatalf("the prune took more than the app's own: %v", wsCfg.PredefinedEnv)
	}
	if _, ok := wsCfg.Apps["billing"]; !ok {
		t.Fatalf("another app was removed")
	}
}

// TestRecreatingADeletedAppDoesNotInheritItsSecrets is the sharpest
// consequence of the old behaviour. Ownership is derived from the name prefix
// matching an app that exists, so orphaned entries were reclaimed as
// app-produced the moment a same-named app reappeared — handing a brand-new
// app the deleted app's credentials until the first refresh.
func TestRecreatingADeletedAppDoesNotInheritItsSecrets(t *testing.T) {
	isolateSharedWorkspace(t)
	if err := os.MkdirAll(filepath.Join(WorkspaceDir, "workspace", "appsuite"), 0755); err != nil {
		t.Fatalf("mkdir bare repo: %s", err)
	}
	saveWorkspaceConfig(&trustableConfig{
		Apps:          map[string]*AppConfig{"appsuite": {Password: "p"}},
		PredefinedEnv: map[string]string{"APPSUITE__DB": "the old app's database"},
	})

	rec := httptest.NewRecorder()
	handleDeleteRepo(rec, httptest.NewRequest("DELETE", "/api/repo", strings.NewReader(`{"name":"appsuite"}`)))
	if rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	// A new, unrelated app of the same name.
	wsCfg, _ := loadWorkspaceConfig()
	if wsCfg.Apps == nil {
		wsCfg.Apps = map[string]*AppConfig{}
	}
	wsCfg.Apps["appsuite"] = &AppConfig{Password: "p2"}
	saveWorkspaceConfig(wsCfg)

	wsCfg, _ = loadWorkspaceConfig()
	if v, ok := wsCfg.PredefinedEnv["APPSUITE__DB"]; ok {
		t.Fatalf("the new app inherited the deleted app's credential: %q", v)
	}
}

// TestConsumerOfAPrunedProducerRePends closes the loop. resolveOneImport
// already re-pends a reference whose target left the pool rather than falling
// back to a different producer; until the prune existed that branch could
// never fire, because nothing ever left.
func TestConsumerOfAPrunedProducerRePends(t *testing.T) {
	cfg := &trustableConfig{PredefinedEnv: map[string]string{
		"APPSUITE__DB": "postgres://appsuite",
		"BILLING__DB":  "postgres://billing",
	}}
	// The consumer is bound to appsuite by reference.
	dev := map[string]string{"EXT_URL": "${{APPSUITE__DB}}"}
	binding := EnvBinding{Name: "EXT_URL", Pattern: "*__DB"}

	if res := resolveOneImport(binding, dev, cfg.PredefinedEnv); res.Pending {
		t.Fatalf("resolved import was pending before the prune")
	}

	pruneSharedPool(cfg, "appsuite")

	res := resolveOneImport(binding, dev, cfg.PredefinedEnv)
	if !res.Pending {
		t.Fatalf("a consumer of a pruned producer did not re-pend: %+v", res)
	}
	// Silently falling back to BILLING__DB would point the app at a different
	// database without telling anyone.
	if res.Value == "postgres://billing" {
		t.Fatalf("the import silently fell back to another producer")
	}
}
