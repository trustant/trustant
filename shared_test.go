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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateSharedWorkspace points WorkspaceDir/WorkbenchDir and HOME at temp
// directories so a test can write .env.shared, trustant.json and
// ~/.ops/config.json without touching the developer's own.
func isolateSharedWorkspace(t *testing.T) (root string) {
	t.Helper()
	origWorkspace := WorkspaceDir
	origWorkbench := WorkbenchDir
	t.Cleanup(func() {
		WorkspaceDir = origWorkspace
		WorkbenchDir = origWorkbench
	})

	root = t.TempDir()
	WorkspaceDir = filepath.Join(root, "workspace")
	WorkbenchDir = filepath.Join(root, "workbench")
	t.Setenv("HOME", filepath.Join(root, "home"))
	if err := os.MkdirAll(WorkspaceDir, 0755); err != nil {
		t.Fatalf("mkdir workspace: %s", err)
	}
	if err := os.MkdirAll(WorkbenchDir, 0755); err != nil {
		t.Fatalf("mkdir workbench: %s", err)
	}
	return root
}

func writeOpsConfigTree(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".ops")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir ops dir: %s", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0644); err != nil {
		t.Fatalf("write ops config: %s", err)
	}
}

func makeWorkbench(t *testing.T, app string) string {
	t.Helper()
	path := filepath.Join(WorkbenchDir, app)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir workbench %s: %s", app, err)
	}
	return path
}

// --- the file format ------------------------------------------------------

func TestSharedEnvRoundTripKeepsTheTemplateForm(t *testing.T) {
	isolateSharedWorkspace(t)
	makeWorkbench(t, "appsuite")

	declared := map[string]string{
		"<app>__POSTGRES_URL": "<.postgres.url>",
		"<app>__S3_KEY":       "<.s3.access.key>",
	}
	if err := saveSharedEnv("appsuite", declared); err != nil {
		t.Fatalf("save: %s", err)
	}
	got := loadSharedEnv("appsuite")
	if len(got) != 2 || got["<app>__POSTGRES_URL"] != "<.postgres.url>" {
		t.Fatalf("round trip lost the template form: %#v", got)
	}
}

func TestSharedEnvIsWrittenInSortedOrder(t *testing.T) {
	isolateSharedWorkspace(t)
	makeWorkbench(t, "appsuite")

	if err := saveSharedEnv("appsuite", map[string]string{
		"<app>__ZED": "<.z.value>",
		"<app>__ABC": "<.a.value>",
		"<app>__MID": "<.m.value>",
	}); err != nil {
		t.Fatalf("save: %s", err)
	}
	body, err := os.ReadFile(sharedEnvPath("appsuite"))
	if err != nil {
		t.Fatalf("read: %s", err)
	}
	want := "<app>__ABC=<.a.value>\n<app>__MID=<.m.value>\n<app>__ZED=<.z.value>\n"
	if string(body) != want {
		// An unstable order would make the committed diff churn on every save.
		t.Fatalf("unsorted output:\n%s", body)
	}
}

func TestSharedEnvNeverContainsAResolvedValue(t *testing.T) {
	isolateSharedWorkspace(t)
	makeWorkbench(t, "appsuite")
	writeOpsConfigTree(t, `{"postgres":{"url":"postgres://user:s3cr3t@db/app"}}`)

	if err := saveSharedEnv("appsuite", map[string]string{
		"<app>__POSTGRES_URL": wrapSharedPath("postgres.url"),
	}); err != nil {
		t.Fatalf("save: %s", err)
	}
	body, err := os.ReadFile(sharedEnvPath("appsuite"))
	if err != nil {
		t.Fatalf("read: %s", err)
	}
	// The file is committed, so a value here would be published.
	if strings.Contains(string(body), "s3cr3t") {
		t.Fatalf(".env.shared leaked a secret:\n%s", body)
	}
}

func TestSaveSharedEnvRemovesTheFileWhenNothingIsShared(t *testing.T) {
	isolateSharedWorkspace(t)
	makeWorkbench(t, "appsuite")

	if err := saveSharedEnv("appsuite", map[string]string{"<app>__X": "<.a.b>"}); err != nil {
		t.Fatalf("save: %s", err)
	}
	if err := saveSharedEnv("appsuite", nil); err != nil {
		t.Fatalf("clear: %s", err)
	}
	if _, err := os.Stat(sharedEnvPath("appsuite")); !os.IsNotExist(err) {
		t.Fatalf("expected the file to be removed, got err=%v", err)
	}
}

// --- placeholders ---------------------------------------------------------

func TestExpandSharedNameUppercasesTheApp(t *testing.T) {
	if got := expandSharedName("<app>__POSTGRES_URL", "appsuite"); got != "APPSUITE__POSTGRES_URL" {
		t.Fatalf("expand = %q", got)
	}
	// A hand-written file that already spells the app out still works.
	if got := expandSharedName("APPSUITE__X", "appsuite"); got != "APPSUITE__X" {
		t.Fatalf("expand of an explicit name = %q", got)
	}
}

func TestTemplateSharedNameFoldsTheAppBackIntoThePlaceholder(t *testing.T) {
	if got := templateSharedName("APPSUITE__POSTGRES_URL", "appsuite"); got != "<app>__POSTGRES_URL" {
		t.Fatalf("template = %q", got)
	}
}

func TestSharedPathOfRejectsABareValue(t *testing.T) {
	if got, ok := sharedPathOf("<.postgres.url>"); !ok || got != "postgres.url" {
		t.Fatalf("unwrap = %q ok=%v", got, ok)
	}
	if got, ok := sharedPathOf("<postgres.url>"); !ok || got != "postgres.url" {
		t.Fatalf("unwrap without the leading dot = %q ok=%v", got, ok)
	}
	// A bare value in this file is a leaked secret, not a pointer. Using it
	// would silently treat the secret as a path and resolve to nothing.
	for _, bad := range []string{"postgres://user:pw@db/app", "", "<>", "<.>"} {
		if _, ok := sharedPathOf(bad); ok {
			t.Fatalf("accepted a non-pointer value %q", bad)
		}
	}
}

func TestSplitSharedVarNameHandlesAnAppNameWithAnUnderscore(t *testing.T) {
	// Splitting on the FIRST separator is what makes this unambiguous.
	got, ok := splitSharedVarName("MY_APP__POSTGRES_URL")
	if !ok || got != "MY_APP" {
		t.Fatalf("split = %q ok=%v", got, ok)
	}
	if _, ok := splitSharedVarName("PLAIN_NAME"); ok {
		t.Fatalf("a name with no separator must not look app-produced")
	}
	if _, ok := splitSharedVarName("APPSUITE__"); ok {
		t.Fatalf("the bare prefix names nothing")
	}
}

func TestSharedProducerOfOnlyMatchesAKnownApp(t *testing.T) {
	apps := map[string]*AppConfig{"appsuite": {}}
	if got, ok := sharedProducerOf("APPSUITE__DB", apps); !ok || got != "appsuite" {
		t.Fatalf("producer = %q ok=%v", got, ok)
	}
	// A hand-typed palette entry that happens to contain "__" is not
	// app-produced, or the folding rules would treat it as derived.
	if _, ok := sharedProducerOf("SOME__THING", apps); ok {
		t.Fatalf("an unknown prefix must not count as app-produced")
	}
}

func TestSharedHostKeyFoldsSchemeAndSlash(t *testing.T) {
	for _, raw := range []string{"https://api.nuvolaris.io/", "http://API.nuvolaris.io", "api.nuvolaris.io"} {
		if got := sharedHostKey(raw); got != "api.nuvolaris.io" {
			t.Fatalf("sharedHostKey(%q) = %q", raw, got)
		}
	}
}

// --- path resolution ------------------------------------------------------

func TestLookupOpsPathResolvesLeavesOnly(t *testing.T) {
	var tree map[string]interface{}
	if err := json.Unmarshal([]byte(`{
		"postgres": {"url": "postgres://db", "port": 5432, "tls": true},
		"s3": {"access": {"key": "AKIA"}}
	}`), &tree); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}

	cases := map[string]string{
		"postgres.url":  "postgres://db",
		"postgres.tls":  "true",
		"s3.access.key": "AKIA",
	}
	for path, want := range cases {
		if got, ok := lookupOpsPath(tree, path); !ok || got != want {
			t.Fatalf("lookup(%q) = %q ok=%v, want %q", path, got, ok, want)
		}
	}

	// A port must not come back as 5432.000000.
	if got, ok := lookupOpsPath(tree, "postgres.port"); !ok || got != "5432" {
		t.Fatalf("numeric leaf = %q ok=%v", got, ok)
	}
	// An object node is a group of secrets, not a value.
	if _, ok := lookupOpsPath(tree, "postgres"); ok {
		t.Fatalf("an object node must not resolve")
	}
	if _, ok := lookupOpsPath(tree, "postgres.missing"); ok {
		t.Fatalf("a missing path must not resolve")
	}
}

func TestOpsConfigTreeExcludesAuth(t *testing.T) {
	isolateSharedWorkspace(t)
	writeOpsConfigTree(t, `{"auth":{"token":"secret"},"postgres":{"url":"postgres://db"}}`)

	tree, err := opsConfigTree()
	if err != nil {
		t.Fatalf("tree: %s", err)
	}
	if _, present := tree["auth"]; present {
		t.Fatalf("auth must be excluded: it is the CLI's own credentials, never something an app shares")
	}
	if _, present := tree["postgres"]; !present {
		t.Fatalf("service blocks must be present")
	}
}

func TestOpsConfigTreeIsNotRedacted(t *testing.T) {
	isolateSharedWorkspace(t)
	writeOpsConfigTree(t, `{"postgres":{"url":"postgres://user:s3cr3t@db/app"}}`)

	tree, err := opsConfigTree()
	if err != nil {
		t.Fatalf("tree: %s", err)
	}
	// The picker exists so the user can see and choose secrets in their own
	// local file; the UI masks them on screen instead.
	got, _ := lookupOpsPath(tree, "postgres.url")
	if !strings.Contains(got, "s3cr3t") {
		t.Fatalf("the tree must not be redacted, got %q", got)
	}
}

func TestRemoveOpsConfigTreatsAMissingFileAsSuccess(t *testing.T) {
	isolateSharedWorkspace(t)
	removeOpsConfig() // must not panic or log-fail with no file at all
	writeOpsConfigTree(t, `{"postgres":{"url":"x"}}`)
	removeOpsConfig()
	if _, err := os.Stat(opsConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("config still present after removal: %v", err)
	}
}

// TestEveryOpsLoginClearsTheGlobalConfigFirst guards the invariant by reading
// the sources: ops ide login MERGES into ~/.ops/config.json, so a call site that
// loses its removal leaves another app's service blocks in place — a wrong
// service binding, not noise.
func TestEveryOpsLoginClearsTheGlobalConfigFirst(t *testing.T) {
	for _, file := range []string{"launch.go", "publish.go", "shared.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %s", file, err)
		}
		src := string(body)
		idx := 0
		for {
			at := strings.Index(src[idx:], `"ide", "login"`)
			if at < 0 {
				break
			}
			at += idx
			// The removal must appear in the lines immediately preceding the
			// command construction, not merely somewhere in the file.
			start := at - 500
			if start < 0 {
				start = 0
			}
			if !strings.Contains(src[start:at], "removeOpsConfig()") {
				t.Fatalf("%s: an ops ide login at offset %d is not preceded by removeOpsConfig()", file, at)
			}
			idx = at + 1
		}
	}
}

// --- the pool rules -------------------------------------------------------

func TestFoldIntoSharedPoolOverwritesAppProducedAndKeepsHandTyped(t *testing.T) {
	apps := map[string]*AppConfig{"appsuite": {}}
	cfg := &trustableConfig{PredefinedEnv: map[string]string{
		"APPSUITE__DB": "stale",
		"MY_API_KEY":   "typed by the user",
	}}

	foldIntoSharedPool(cfg, map[string]string{
		"APPSUITE__DB": "fresh",
		"MY_API_KEY":   "resolved",
	}, apps)

	// An app-produced name belongs to its producer: a refresh that could not
	// update it would serve a stale credential forever.
	if cfg.PredefinedEnv["APPSUITE__DB"] != "fresh" {
		t.Fatalf("app-produced value not refreshed: %q", cfg.PredefinedEnv["APPSUITE__DB"])
	}
	// A hand-typed name is the user's.
	if cfg.PredefinedEnv["MY_API_KEY"] != "typed by the user" {
		t.Fatalf("hand-typed value was clobbered: %q", cfg.PredefinedEnv["MY_API_KEY"])
	}
}

func TestFoldIntoSharedPoolRespectsTheLimit(t *testing.T) {
	apps := map[string]*AppConfig{}
	cfg := &trustableConfig{PredefinedEnv: map[string]string{}}
	for i := 0; i < maxPredefinedEnvVars; i++ {
		cfg.PredefinedEnv[string(rune('A'+i%26))+strings.Repeat("x", i/26+1)] = "v"
	}
	before := len(cfg.PredefinedEnv)
	foldIntoSharedPool(cfg, map[string]string{"BRAND_NEW_NAME": "v"}, apps)
	if len(cfg.PredefinedEnv) > before {
		t.Fatalf("the pool grew past its limit: %d", len(cfg.PredefinedEnv))
	}
}

func TestFoldIntoProductionPoolKeepsValuesPerHost(t *testing.T) {
	apps := map[string]*AppConfig{"appsuite": {}}
	cfg := &trustableConfig{}

	foldIntoProductionPool(cfg, "https://api.nuvolaris.io/", map[string]string{"APPSUITE__DB": "nuvolaris"}, apps)
	foldIntoProductionPool(cfg, "openserverless.dev", map[string]string{"APPSUITE__DB": "openserverless"}, apps)

	// Same name, different cluster, different secret. One flat map would hand an
	// app the wrong cluster's credentials.
	if got := cfg.PredefinedEnvProduction["api.nuvolaris.io"]["APPSUITE__DB"]; got != "nuvolaris" {
		t.Fatalf("first host = %q", got)
	}
	if got := cfg.PredefinedEnvProduction["openserverless.dev"]["APPSUITE__DB"]; got != "openserverless" {
		t.Fatalf("second host = %q", got)
	}
}

func TestFoldIntoProductionPoolKeepsAHandTypedHostValue(t *testing.T) {
	// A value typed by hand is the escape hatch for a producer that lives on
	// another installation; a later publish must not silently replace it.
	apps := map[string]*AppConfig{}
	cfg := &trustableConfig{PredefinedEnvProduction: map[string]map[string]string{
		"api.nuvolaris.io": {"ELSEWHERE__DB": "typed by hand"},
	}}
	foldIntoProductionPool(cfg, "api.nuvolaris.io", map[string]string{"ELSEWHERE__DB": "resolved"}, apps)
	if got := cfg.PredefinedEnvProduction["api.nuvolaris.io"]["ELSEWHERE__DB"]; got != "typed by hand" {
		t.Fatalf("hand-typed production value was clobbered: %q", got)
	}
}

func TestPredefinedEnvProductionSurvivesMergeConfigs(t *testing.T) {
	base := &trustableConfig{PredefinedEnvProduction: map[string]map[string]string{
		"api.nuvolaris.io": {"BASE__A": "1"},
	}}
	ws := &trustableConfig{PredefinedEnvProduction: map[string]map[string]string{
		"api.nuvolaris.io":   {"WS__B": "2"},
		"openserverless.dev": {"WS__C": "3"},
	}}
	merged := mergeConfigs(base, ws)
	if merged.PredefinedEnvProduction["api.nuvolaris.io"]["BASE__A"] != "1" {
		t.Fatalf("base value lost in merge")
	}
	if merged.PredefinedEnvProduction["api.nuvolaris.io"]["WS__B"] != "2" {
		t.Fatalf("workspace value lost in merge")
	}
	if merged.PredefinedEnvProduction["openserverless.dev"]["WS__C"] != "3" {
		t.Fatalf("workspace-only host lost in merge")
	}
}

// --- consuming ------------------------------------------------------------

func TestGeneratedEnvFillsDeclaredEmptyNamesFromThePool(t *testing.T) {
	isolateSharedWorkspace(t)
	workbench := makeWorkbench(t, "consumer")

	cfg := &trustableConfig{
		Apps: map[string]*AppConfig{
			"consumer": {Development: map[string]string{"APPSUITE__DB": "", "OWN": "kept"}},
		},
		PredefinedEnv: map[string]string{
			"APPSUITE__DB": "postgres://shared",
			"UNDECLARED":   "must not appear",
		},
	}
	if err := saveWorkspaceConfig(cfg); err != nil {
		t.Fatalf("save config: %s", err)
	}
	if err := generateAppEnvFiles("consumer"); err != nil {
		t.Fatalf("generate: %s", err)
	}
	body, err := os.ReadFile(filepath.Join(workbench, ".env"))
	if err != nil {
		t.Fatalf("read .env: %s", err)
	}
	env := string(body)
	if !strings.Contains(env, "APPSUITE__DB=postgres://shared") {
		t.Fatalf("declared-and-empty name was not filled:\n%s", env)
	}
	// The pool is never a source of new variables: an app that does not name a
	// pool variable must never see it.
	if strings.Contains(env, "UNDECLARED") {
		t.Fatalf("an undeclared pool name reached the app:\n%s", env)
	}
	if !strings.Contains(env, "OWN=kept") {
		t.Fatalf("the app's own value was lost:\n%s", env)
	}
}

func TestGeneratedEnvNeverOverwritesATypedValue(t *testing.T) {
	isolateSharedWorkspace(t)
	workbench := makeWorkbench(t, "consumer")

	cfg := &trustableConfig{
		Apps:          map[string]*AppConfig{"consumer": {Development: map[string]string{"APPSUITE__DB": "mine"}}},
		PredefinedEnv: map[string]string{"APPSUITE__DB": "from the pool"},
	}
	if err := saveWorkspaceConfig(cfg); err != nil {
		t.Fatalf("save config: %s", err)
	}
	if err := generateAppEnvFiles("consumer"); err != nil {
		t.Fatalf("generate: %s", err)
	}
	body, _ := os.ReadFile(filepath.Join(workbench, ".env"))
	if !strings.Contains(string(body), "APPSUITE__DB=mine") {
		t.Fatalf("a value the user typed must always win:\n%s", body)
	}
}

func TestGeneratedProductionEnvUsesTheAppsOwnHostPool(t *testing.T) {
	isolateSharedWorkspace(t)
	workbench := makeWorkbench(t, "consumer")

	cfg := &trustableConfig{
		Apps: map[string]*AppConfig{"consumer": {Production: map[string]string{
			"OPS_APIHOST":  "https://openserverless.dev",
			"APPSUITE__DB": "",
		}}},
		PredefinedEnvProduction: map[string]map[string]string{
			"api.nuvolaris.io":   {"APPSUITE__DB": "WRONG CLUSTER"},
			"openserverless.dev": {"APPSUITE__DB": "right cluster"},
		},
	}
	if err := saveWorkspaceConfig(cfg); err != nil {
		t.Fatalf("save config: %s", err)
	}
	if err := generateAppEnvFiles("consumer"); err != nil {
		t.Fatalf("generate: %s", err)
	}
	body, err := os.ReadFile(filepath.Join(workbench, ".env.production"))
	if err != nil {
		t.Fatalf("read .env.production: %s", err)
	}
	if !strings.Contains(string(body), "APPSUITE__DB=right cluster") {
		t.Fatalf("production value came from the wrong host:\n%s", body)
	}
	if strings.Contains(string(body), "WRONG CLUSTER") {
		t.Fatalf("another cluster's credentials leaked into .env.production:\n%s", body)
	}
}

func TestGeneratedEnvDoesNotUseTheDevelopmentPoolForProduction(t *testing.T) {
	isolateSharedWorkspace(t)
	workbench := makeWorkbench(t, "consumer")

	cfg := &trustableConfig{
		Apps: map[string]*AppConfig{"consumer": {Production: map[string]string{
			"OPS_APIHOST":  "https://openserverless.dev",
			"APPSUITE__DB": "",
		}}},
		PredefinedEnv: map[string]string{"APPSUITE__DB": "development secret"},
	}
	if err := saveWorkspaceConfig(cfg); err != nil {
		t.Fatalf("save config: %s", err)
	}
	if err := generateAppEnvFiles("consumer"); err != nil {
		t.Fatalf("generate: %s", err)
	}
	body, _ := os.ReadFile(filepath.Join(workbench, ".env.production"))
	// The two pools must never cross: a development credential in production
	// points the deployed app at the developer's own services.
	if strings.Contains(string(body), "development secret") {
		t.Fatalf("the development pool reached .env.production:\n%s", body)
	}
}

// --- the publish gate -----------------------------------------------------

func TestMissingProductionSharedBlocksAndNamesTheProducer(t *testing.T) {
	cfg := &trustableConfig{Apps: map[string]*AppConfig{
		"appsuite": {},
		"consumer": {Production: map[string]string{"APPSUITE__DB": ""}},
	}}
	missing := missingProductionShared("consumer", "https://openserverless.dev/", cfg)
	if len(missing) != 1 {
		t.Fatalf("expected one blocking variable, got %#v", missing)
	}
	if missing[0].Name != "APPSUITE__DB" || missing[0].App != "appsuite" {
		t.Fatalf("the message must name the producer: %#v", missing[0])
	}
	if missing[0].Host != "openserverless.dev" {
		t.Fatalf("host not normalized: %q", missing[0].Host)
	}
}

func TestMissingProductionSharedIsSatisfiedByTheHostPool(t *testing.T) {
	cfg := &trustableConfig{
		Apps: map[string]*AppConfig{
			"appsuite": {},
			"consumer": {Production: map[string]string{"APPSUITE__DB": ""}},
		},
		PredefinedEnvProduction: map[string]map[string]string{
			"openserverless.dev": {"APPSUITE__DB": "resolved"},
		},
	}
	if missing := missingProductionShared("consumer", "openserverless.dev", cfg); len(missing) != 0 {
		t.Fatalf("a host that has the value must not block: %#v", missing)
	}
	// ...but only for that host.
	if missing := missingProductionShared("consumer", "api.nuvolaris.io", cfg); len(missing) != 1 {
		t.Fatalf("another host must still block: %#v", missing)
	}
}

func TestMissingProductionSharedIsSatisfiedByAHandTypedValue(t *testing.T) {
	// The escape hatch for a producer on another installation.
	cfg := &trustableConfig{Apps: map[string]*AppConfig{
		"appsuite": {},
		"consumer": {Production: map[string]string{"APPSUITE__DB": "typed by hand"}},
	}}
	if missing := missingProductionShared("consumer", "openserverless.dev", cfg); len(missing) != 0 {
		t.Fatalf("a hand-typed value must satisfy the gate: %#v", missing)
	}
}

func TestAnAppNeverBlocksOnAVariableItProducesItself(t *testing.T) {
	// Its own publish resolves it in the same request.
	cfg := &trustableConfig{Apps: map[string]*AppConfig{
		"appsuite": {Production: map[string]string{"APPSUITE__DB": ""}},
	}}
	if missing := missingProductionShared("appsuite", "openserverless.dev", cfg); len(missing) != 0 {
		t.Fatalf("an app blocked on its own variable: %#v", missing)
	}
}

func TestOrdinaryProductionVariablesDoNotBlockPublish(t *testing.T) {
	// An empty production value that no app produces is the user's business.
	cfg := &trustableConfig{Apps: map[string]*AppConfig{
		"consumer": {Production: map[string]string{"SOME_KEY": ""}},
	}}
	if missing := missingProductionShared("consumer", "openserverless.dev", cfg); len(missing) != 0 {
		t.Fatalf("a plain empty variable must not block a publish: %#v", missing)
	}
}

// --- endpoints ------------------------------------------------------------

func TestSharedSaveRefusesUnprefixedVariableNames(t *testing.T) {
	isolateSharedWorkspace(t)
	// Deliberately NO workbench: validation must reject the request before
	// anything reaches opsLoginForApp. `ops` is on PATH on a developer machine,
	// so a handler that got that far would run a real login against a real
	// cluster from a unit test.

	for _, name := range []string{"POSTGRES_URL", "OTHER__DB", "appsuite__DB", "APPSUITE__", "APPSUITE_DB"} {
		body, _ := json.Marshal(map[string]interface{}{
			"vars": []map[string]string{{"name": name, "path": "postgres.url"}},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/shared/appsuite", strings.NewReader(string(body)))
		rec := httptest.NewRecorder()
		handleShared(rec, req)

		// Assert the STATUS and the MESSAGE, not merely that nothing was
		// written: with no ops binary present the resolve fails first, so a
		// "nothing written" assertion passes even with the gate deleted.
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("name %q: status = %d, want 400 (body %q)", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "must start with") {
			t.Fatalf("name %q: body = %q", name, rec.Body.String())
		}
		if _, err := os.Stat(sharedEnvPath("appsuite")); !os.IsNotExist(err) {
			t.Fatalf("name %q: the refusal must be total, but a file was written", name)
		}
	}
}

func TestSharedSaveRequiresAWorkbench(t *testing.T) {
	isolateSharedWorkspace(t)
	body, _ := json.Marshal(map[string]interface{}{
		"vars": []map[string]string{{"name": "APPSUITE__DB", "path": "postgres.url"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/shared/appsuite", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handleShared(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSharedEndpointsValidateTheAppName(t *testing.T) {
	isolateSharedWorkspace(t)
	for _, path := range []string{"/api/shared/..%2Fetc", "/api/shared/tree/bad%20name", "/api/shared/bad.name"} {
		rec := httptest.NewRecorder()
		handleShared(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", path, rec.Code)
		}
	}
}

func TestSharedGetReturnsExpandedNames(t *testing.T) {
	isolateSharedWorkspace(t)
	makeWorkbench(t, "appsuite")
	if err := saveSharedEnv("appsuite", map[string]string{"<app>__DB": "<.postgres.url>"}); err != nil {
		t.Fatalf("save: %s", err)
	}

	rec := httptest.NewRecorder()
	handleShared(rec, httptest.NewRequest(http.MethodGet, "/api/shared/appsuite", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Prefix string             `json:"prefix"`
		Vars   []sharedVarRequest `json:"vars"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %s", err)
	}
	if resp.Prefix != "APPSUITE__" {
		t.Fatalf("prefix = %q", resp.Prefix)
	}
	// The picker pre-checks by path, and shows the effective name.
	if len(resp.Vars) != 1 || resp.Vars[0].Name != "APPSUITE__DB" || resp.Vars[0].Path != "postgres.url" {
		t.Fatalf("vars = %#v", resp.Vars)
	}
}

func TestSharedListReportsTheProducingApp(t *testing.T) {
	isolateSharedWorkspace(t)
	makeWorkbench(t, "appsuite")
	if err := saveSharedEnv("appsuite", map[string]string{"<app>__DB": "<.postgres.url>"}); err != nil {
		t.Fatalf("save: %s", err)
	}
	if err := saveWorkspaceConfig(&trustableConfig{Apps: map[string]*AppConfig{"appsuite": {}}}); err != nil {
		t.Fatalf("save config: %s", err)
	}

	rec := httptest.NewRecorder()
	handleShared(rec, httptest.NewRequest(http.MethodGet, "/api/shared", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Vars []sharedListEntry `json:"vars"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %s", err)
	}
	if len(resp.Vars) != 1 || resp.Vars[0].App != "appsuite" || resp.Vars[0].Name != "APPSUITE__DB" {
		t.Fatalf("vars = %#v", resp.Vars)
	}
}

func TestPredefinedEnvPostCannotAlterAnAppProducedKey(t *testing.T) {
	isolateSharedWorkspace(t)
	if err := saveWorkspaceConfig(&trustableConfig{
		Apps:          map[string]*AppConfig{"appsuite": {}},
		PredefinedEnv: map[string]string{"APPSUITE__DB": "resolved", "MINE": "typed"},
	}); err != nil {
		t.Fatalf("save config: %s", err)
	}

	// A stale tab posting the whole table without the derived row, or with it
	// rewritten. Dropping it would break a consumer until the next refresh.
	body, _ := json.Marshal(map[string]interface{}{
		"vars": []map[string]string{
			{"name": "MINE", "value": "edited"},
			{"name": "APPSUITE__DB", "value": "tampered"},
		},
	})
	rec := httptest.NewRecorder()
	handlePostPredefinedEnv(rec, httptest.NewRequest(http.MethodPost, "/api/predefined-env", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	saved, err := loadWorkspaceConfig()
	if err != nil {
		t.Fatalf("reload: %s", err)
	}
	if saved.PredefinedEnv["APPSUITE__DB"] != "resolved" {
		t.Fatalf("an app-produced value was altered by POST: %q", saved.PredefinedEnv["APPSUITE__DB"])
	}
	if saved.PredefinedEnv["MINE"] != "edited" {
		t.Fatalf("a hand-typed edit was lost: %q", saved.PredefinedEnv["MINE"])
	}
}

func TestGetPredefinedEnvMarksAppProducedRows(t *testing.T) {
	isolateSharedWorkspace(t)
	if err := saveWorkspaceConfig(&trustableConfig{
		Apps:          map[string]*AppConfig{"appsuite": {}},
		PredefinedEnv: map[string]string{"APPSUITE__DB": "resolved", "MINE": "typed"},
		PredefinedEnvProduction: map[string]map[string]string{
			"openserverless.dev": {"APPSUITE__DB": "prod"},
		},
	}); err != nil {
		t.Fatalf("save config: %s", err)
	}

	rec := httptest.NewRecorder()
	handleGetPredefinedEnv(rec, httptest.NewRequest(http.MethodGet, "/api/predefined-env", nil))
	var resp struct {
		Vars       []PredefinedEnvVar            `json:"vars"`
		Production map[string][]PredefinedEnvVar `json:"production"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %s", err)
	}
	byName := map[string]PredefinedEnvVar{}
	for _, v := range resp.Vars {
		byName[v.Name] = v
	}
	// This is how the page knows which rows it may not edit.
	if byName["APPSUITE__DB"].App != "appsuite" {
		t.Fatalf("app-produced row not marked: %#v", byName["APPSUITE__DB"])
	}
	if byName["MINE"].App != "" {
		t.Fatalf("a hand-typed row must not be marked: %#v", byName["MINE"])
	}
	if len(resp.Production["openserverless.dev"]) != 1 {
		t.Fatalf("production pool not exposed: %#v", resp.Production)
	}
}

// --- frontend invariants --------------------------------------------------

func TestShareButtonIsOnTheAppListAndNotTheEnvEditor(t *testing.T) {
	applist, err := os.ReadFile("web/applist.html")
	if err != nil {
		t.Fatalf("read applist.html: %s", err)
	}
	if !strings.Contains(string(applist), "openSharedPicker(") {
		t.Fatalf("the Share action must be on the app list: the pool it writes into is workspace-wide")
	}
	appconfig, err := os.ReadFile("web/appconfig.html")
	if err != nil {
		t.Fatalf("read appconfig.html: %s", err)
	}
	if strings.Contains(string(appconfig), "SharedPicker.mount") {
		t.Fatalf("the Share picker moved to the app list; appconfig.html must not mount it too")
	}
}

func TestWorkbenchEnvModalIsReadOnly(t *testing.T) {
	body, err := os.ReadFile("web/app.html")
	if err != nil {
		t.Fatalf("read app.html: %s", err)
	}
	src := string(body)
	if !strings.Contains(src, "readOnly: true") {
		t.Fatalf("app.html must register its env table read-only")
	}
	// Editing belongs to the app list's Env action.
	if strings.Contains(src, "saveEnvVars()") {
		t.Fatalf("app.html still offers a Save path for env vars")
	}
	if strings.Contains(src, "envTable.add()") {
		t.Fatalf("app.html still offers Add Variable")
	}
}

func TestEnvTableReadOnlySuppressesMutation(t *testing.T) {
	body, err := os.ReadFile("web/js/envtable.js")
	if err != nil {
		t.Fatalf("read envtable.js: %s", err)
	}
	src := string(body)
	// A stray caller must not be able to mutate a table the user cannot see is
	// editable.
	for _, guard := range []string{
		"EnvTable.prototype.add = function () {\n        if (this.readOnly) return;",
		"if (this.readOnly) return this.missingNames();",
	} {
		if !strings.Contains(src, guard) {
			t.Fatalf("missing read-only guard: %q", guard)
		}
	}
	if !strings.Contains(src, "EnvTable.prototype.addFromShared") {
		t.Fatalf("addFromShared is what the env editor's Add from shared button calls")
	}
}
