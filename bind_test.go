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
	"reflect"
	"strings"
	"testing"
)

func TestParseEnvDistBindings(t *testing.T) {
	content := strings.Join([]string{
		"# a comment",
		"",
		"DATABASE_PASSWORD=",
		"EXT_POSTGRESQLURL=*__POSTGRESDB",
		"export EXPORTED=*__REDIS",
		`QUOTED="*__S3"`,
		"RENAMED=APPSUITE__POSTGRES_URL",
		"WITH_EQUALS=*__URL=x",
		"DATABASE_PASSWORD=second",
		"novalue",
		"=orphan",
	}, "\n")

	got := parseEnvDistBindings(content)
	want := []EnvBinding{
		{Name: "DATABASE_PASSWORD", Pattern: ""},
		{Name: "EXT_POSTGRESQLURL", Pattern: "*__POSTGRESDB"},
		{Name: "EXPORTED", Pattern: "*__REDIS"},
		{Name: "QUOTED", Pattern: "*__S3"},
		{Name: "RENAMED", Pattern: "APPSUITE__POSTGRES_URL"},
		// Everything after the first `=` is the pattern.
		{Name: "WITH_EQUALS", Pattern: "*__URL=x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseEnvDistBindings =\n%#v\nwant\n%#v", got, want)
	}
}

// An empty pattern is the exact-match case — the only case that existed before
// this feature, so every .env.dist ever written keeps resolving as it did.
func TestEnvBindingExact(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		exact   bool
	}{
		{"", true},
		{"APPSUITE__POSTGRES_URL", true},
		{"*__POSTGRESDB", false},
		{"*", false},
	} {
		if got := (EnvBinding{Pattern: tc.pattern}).Exact(); got != tc.exact {
			t.Errorf("Exact(%q) = %v, want %v", tc.pattern, got, tc.exact)
		}
	}
}

func TestMatchWildcard(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"APPSUITE__POSTGRESDB", "APPSUITE__POSTGRESDB", true},
		{"APPSUITE__POSTGRESDB", "BILLING__POSTGRESDB", false},
		{"*__POSTGRESDB", "APPSUITE__POSTGRESDB", true},
		{"*__POSTGRESDB", "BILLING__POSTGRESDB", true},
		{"*__POSTGRESDB", "APPSUITE__REDIS", false},
		// A `*` matches an empty run too.
		{"*__POSTGRESDB", "__POSTGRESDB", true},
		{"APPSUITE__*", "APPSUITE__POSTGRESDB", true},
		{"APPSUITE__*", "BILLING__POSTGRESDB", false},
		{"*POSTGRES*", "APPSUITE__POSTGRESDB", true},
		{"*POSTGRES*", "APPSUITE__REDIS", false},
		{"*", "ANYTHING", true},
		{"*", "", true},
		// The suffix must not overlap the prefix already consumed.
		{"AB*BC", "ABC", false},
	} {
		if got := matchWildcard(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchWildcard(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// Matches come back sorted: the pull-down order must not shift between refreshes.
func TestMatchPoolVariablesSorted(t *testing.T) {
	pool := map[string]string{
		"ZETA__POSTGRESDB":     "z",
		"APPSUITE__POSTGRESDB": "a",
		"BILLING__POSTGRESDB":  "b",
		"APPSUITE__REDIS":      "r",
	}
	got := matchPoolVariables("*__POSTGRESDB", pool)
	want := []string{"APPSUITE__POSTGRESDB", "BILLING__POSTGRESDB", "ZETA__POSTGRESDB"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matchPoolVariables = %v, want %v", got, want)
	}
	if got := matchPoolVariables("*__MILVUS", pool); len(got) != 0 {
		t.Fatalf("no match should return empty, got %v", got)
	}
}

// bindTestApp wires a workspace with a pool and an app carrying a .env.dist.
func bindTestApp(t *testing.T, dev map[string]string, pool map[string]string, dist string) string {
	t.Helper()

	root := t.TempDir()
	origWorkspace, origWorkbench := WorkspaceDir, WorkbenchDir
	WorkspaceDir = filepath.Join(root, "workspace")
	WorkbenchDir = filepath.Join(root, "workbench")
	t.Cleanup(func() {
		WorkspaceDir = origWorkspace
		WorkbenchDir = origWorkbench
	})

	workbenchPath := filepath.Join(WorkbenchDir, "demo")
	if err := os.MkdirAll(workbenchPath, 0755); err != nil {
		t.Fatalf("create workbench: %s", err)
	}
	if dist != "" {
		if err := os.WriteFile(filepath.Join(workbenchPath, ".env.dist"), []byte(dist), 0644); err != nil {
			t.Fatalf("write .env.dist: %s", err)
		}
	}
	if err := saveWorkspaceConfig(&trustantConfig{
		Apps:          map[string]*AppConfig{"demo": {Password: "p", Development: dev}},
		PredefinedEnv: pool,
	}); err != nil {
		t.Fatalf("save config: %s", err)
	}
	return workbenchPath
}

// An exact binding reads the pool entry of its own name; a wildcard with one
// match needs no decision; a wildcard with several pends.
func TestResolveImports(t *testing.T) {
	bindTestApp(t,
		map[string]string{},
		map[string]string{
			"DATABASE_PASSWORD":    "hunter2",
			"APPSUITE__POSTGRESDB": "postgres://appsuite",
			"BILLING__POSTGRESDB":  "postgres://billing",
			"APPSUITE__REDIS":      "redis://appsuite",
		},
		"DATABASE_PASSWORD=\nONE_MATCH=*__REDIS\nMANY=*__POSTGRESDB\nNO_MATCH=*__MILVUS\n")

	got, err := resolveImports("demo")
	if err != nil {
		t.Fatalf("resolveImports: %s", err)
	}
	byName := map[string]ImportResolution{}
	for _, r := range got {
		byName[r.Name] = r
	}

	if r := byName["DATABASE_PASSWORD"]; r.Value != "hunter2" || r.Pending {
		t.Errorf("exact binding = %+v, want hunter2 resolved", r)
	}
	if r := byName["ONE_MATCH"]; r.Value != "redis://appsuite" || r.Pending || r.Source != "APPSUITE__REDIS" {
		t.Errorf("single match = %+v, want auto-resolved", r)
	}
	if r := byName["MANY"]; !r.Pending || len(r.Matches) != 2 {
		t.Errorf("ambiguous binding = %+v, want pending with 2 matches", r)
	}
	if r := byName["NO_MATCH"]; !r.Pending || len(r.Matches) != 0 {
		t.Errorf("unmatched binding = %+v, want pending with no matches", r)
	}
}

// A recorded choice survives, and keeps tracking its producer: the value comes
// from the pool on every resolve, so a rotated credential reaches the app
// instead of the copy frozen in the config.
func TestResolveImportsRecordedChoiceTracksPool(t *testing.T) {
	bindTestApp(t,
		map[string]string{"MANY": importRef("BILLING__POSTGRESDB")},
		map[string]string{
			"APPSUITE__POSTGRESDB": "postgres://appsuite",
			"BILLING__POSTGRESDB":  "postgres://billing-rotated",
		},
		"MANY=*__POSTGRESDB\n")

	got, err := resolveImports("demo")
	if err != nil {
		t.Fatalf("resolveImports: %s", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d resolutions, want 1: %+v", len(got), got)
	}
	if got[0].Value != "postgres://billing-rotated" || got[0].Source != "BILLING__POSTGRESDB" {
		t.Fatalf("resolution = %+v, want the current value of the chosen producer", got[0])
	}
}

// A choice whose producer left the pool re-pends. Falling back to another match
// would silently point the app at a different database.
func TestResolveImportsStaleChoiceRepends(t *testing.T) {
	bindTestApp(t,
		map[string]string{"MANY": importRef("GONE__POSTGRESDB")},
		map[string]string{
			"APPSUITE__POSTGRESDB": "postgres://appsuite",
			"BILLING__POSTGRESDB":  "postgres://billing",
		},
		"MANY=*__POSTGRESDB\n")

	got, err := resolveImports("demo")
	if err != nil {
		t.Fatalf("resolveImports: %s", err)
	}
	if !got[0].Pending {
		t.Fatalf("resolution = %+v, want pending after its producer vanished", got[0])
	}
}

// A value typed by hand is the escape hatch for a producer that is not installed
// here, so it outranks the pool and resolves the binding.
func TestResolveImportsHandTypedValueWins(t *testing.T) {
	bindTestApp(t,
		map[string]string{"NO_MATCH": "postgres://typed-by-hand"},
		map[string]string{"APPSUITE__POSTGRESDB": "postgres://appsuite"},
		"NO_MATCH=*__MILVUS\n")

	got, err := resolveImports("demo")
	if err != nil {
		t.Fatalf("resolveImports: %s", err)
	}
	if got[0].Pending || got[0].Value != "postgres://typed-by-hand" {
		t.Fatalf("resolution = %+v, want the hand-typed value", got[0])
	}
}

// Every variable must end up with a value: one the app declares but never got is
// pending just as an unresolved import is.
func TestPendingImportsIncludesBlankConfigVar(t *testing.T) {
	bindTestApp(t,
		map[string]string{"FILLED": "v", "BLANK": ""},
		map[string]string{},
		"")

	pending, err := pendingImports("demo")
	if err != nil {
		t.Fatalf("pendingImports: %s", err)
	}
	if len(pending) != 1 || pending[0].Name != "BLANK" {
		t.Fatalf("pending = %+v, want only BLANK", pending)
	}
}

// Applying a choice records the producer and the value, so the launch that
// follows reads a resolved config.
func TestApplyImportChoices(t *testing.T) {
	bindTestApp(t,
		map[string]string{},
		map[string]string{
			"APPSUITE__POSTGRESDB": "postgres://appsuite",
			"BILLING__POSTGRESDB":  "postgres://billing",
		},
		"MANY=*__POSTGRESDB\n")

	if err := applyImportChoices("demo", map[string]string{"MANY": "BILLING__POSTGRESDB"}, nil); err != nil {
		t.Fatalf("applyImportChoices: %s", err)
	}

	cfg, err := loadWorkspaceConfig()
	if err != nil {
		t.Fatalf("load config: %s", err)
	}
	dev := cfg.Apps["demo"].Development
	if dev["MANY"] != importRef("BILLING__POSTGRESDB") {
		t.Errorf("stored value = %q, want a reference to the chosen producer", dev["MANY"])
	}

	pending, err := pendingImports("demo")
	if err != nil {
		t.Fatalf("pendingImports: %s", err)
	}
	if len(pending) != 0 {
		t.Fatalf("still pending after the choice: %+v", pending)
	}
}

// A choice naming something that is not in the pool is refused, and nothing is
// written: a partial apply would launch the app half-bound.
func TestApplyImportChoicesRejectsUnknownSource(t *testing.T) {
	bindTestApp(t, map[string]string{}, map[string]string{"APPSUITE__POSTGRESDB": "p"}, "MANY=*__POSTGRESDB\n")

	err := applyImportChoices("demo", map[string]string{"MANY": "NOT__IN__POOL"}, nil)
	if err == nil {
		t.Fatal("expected a refusal")
	}

	cfg, _ := loadWorkspaceConfig()
	if _, ok := cfg.Apps["demo"].Development["MANY"]; ok {
		t.Fatal("nothing must be written when the choice is refused")
	}
}

// A literal overrides the binding, and must clear any recorded producer: leaving
// it would resurrect the pool value on the next launch.
func TestApplyImportChoicesLiteralClearsRecordedChoice(t *testing.T) {
	bindTestApp(t,
		map[string]string{"MANY": importRef("BILLING__POSTGRESDB")},
		map[string]string{"BILLING__POSTGRESDB": "postgres://billing"},
		"MANY=*__POSTGRESDB\n")

	if err := applyImportChoices("demo", nil, map[string]string{"MANY": "postgres://manual"}); err != nil {
		t.Fatalf("applyImportChoices: %s", err)
	}

	cfg, _ := loadWorkspaceConfig()
	dev := cfg.Apps["demo"].Development
	if dev["MANY"] != "postgres://manual" {
		t.Errorf("value = %q, want the literal", dev["MANY"])
	}
	if _, isRef := importRefTarget(dev["MANY"]); isRef {
		t.Error("a literal override must replace the reference, not leave it in place")
	}
}

// The disjunction: a name that already has a value in .env is owned by the env
// editor, and importing it too would give one variable two sources.
func TestSaveImportBindingsRejectsNameWithValue(t *testing.T) {
	bindTestApp(t, map[string]string{"ALPHA": "a"}, map[string]string{}, "")

	err := saveImportBindings("demo", []EnvBinding{{Name: "ALPHA", Pattern: "*__X"}})
	if err == nil || !strings.Contains(err.Error(), "already has a value") {
		t.Fatalf("err = %v, want a refusal naming the conflict", err)
	}
}

// Refusal is total: one bad row writes nothing, so a partial save can never
// leave the two files overlapping.
func TestSaveImportBindingsRefusalIsTotal(t *testing.T) {
	workbenchPath := bindTestApp(t, map[string]string{"ALPHA": "a"}, map[string]string{}, "")

	err := saveImportBindings("demo", []EnvBinding{
		{Name: "GOOD", Pattern: "*__X"},
		{Name: "ALPHA", Pattern: "*__Y"},
	})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if _, err := os.Stat(filepath.Join(workbenchPath, ".env.dist")); !os.IsNotExist(err) {
		t.Fatalf(".env.dist must not be written at all: err=%v", err)
	}
}

func TestSaveImportBindingsRejectsDuplicatesAndServerKeys(t *testing.T) {
	bindTestApp(t, map[string]string{}, map[string]string{}, "")

	if err := saveImportBindings("demo", []EnvBinding{{Name: "A"}, {Name: "A"}}); err == nil {
		t.Error("duplicate name must be refused")
	}
	if err := saveImportBindings("demo", []EnvBinding{{Name: "OPS_USER"}}); err == nil {
		t.Error("server-supplied key must be refused")
	}
	if err := saveImportBindings("demo", []EnvBinding{{Name: "1BAD"}}); err == nil {
		t.Error("invalid variable name must be refused")
	}
}

// An unambiguous import reaches .env without the user being asked anything, and
// the bookkeeping key never does: it would be a stray variable whose value is
// the name of another variable.
func TestGenerateAppEnvFilesWritesImportsNotBookkeeping(t *testing.T) {
	workbenchPath := bindTestApp(t,
		map[string]string{"MANY": importRef("BILLING__POSTGRESDB")},
		map[string]string{
			"APPSUITE__REDIS":      "redis://appsuite",
			"APPSUITE__POSTGRESDB": "postgres://appsuite",
			"BILLING__POSTGRESDB":  "postgres://billing",
		},
		"ONE=*__REDIS\nMANY=*__POSTGRESDB\n")

	if err := generateAppEnvFiles("demo"); err != nil {
		t.Fatalf("generateAppEnvFiles: %s", err)
	}
	data, err := os.ReadFile(filepath.Join(workbenchPath, ".env"))
	if err != nil {
		t.Fatalf("read .env: %s", err)
	}
	env := string(data)

	if !strings.Contains(env, "ONE=redis://appsuite") {
		t.Errorf("unambiguous import missing from .env:\n%s", env)
	}
	if !strings.Contains(env, "MANY=postgres://billing") {
		t.Errorf("chosen import missing from .env:\n%s", env)
	}
	// The reference is storage, not a value: the app gets the resolved secret.
	if strings.Contains(env, importRefOpen) {
		t.Errorf("a reference leaked into .env instead of its value:\n%s", env)
	}
}

// End-to-end through the HTTP handler: declare imports, resolve, choose.
func TestImportsEndpointRoundTrip(t *testing.T) {
	root := t.TempDir()
	ow, ob := WorkspaceDir, WorkbenchDir
	WorkspaceDir = filepath.Join(root, "workspace")
	WorkbenchDir = filepath.Join(root, "workbench")
	defer func() { WorkspaceDir, WorkbenchDir = ow, ob }()

	wb := filepath.Join(WorkbenchDir, "demoapp")
	os.MkdirAll(wb, 0755)
	saveWorkspaceConfig(&trustantConfig{
		Apps: map[string]*AppConfig{"demoapp": {Password: "p", Development: map[string]string{}}},
		PredefinedEnv: map[string]string{
			"APPSUITE__POSTGRESDB": "postgres://appsuite",
			"BILLING__POSTGRESDB":  "postgres://billing",
		},
	})

	// 1. Declare an import through the Import tab.
	body := `{"bindings":[{"name":"EXT_URL","pattern":"*__POSTGRESDB"}]}`
	req := httptest.NewRequest("POST", "/api/imports/demoapp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handleImports(rec, req)
	if rec.Code != 200 {
		t.Fatalf("declare: %d %s", rec.Code, rec.Body.String())
	}

	// The file is what got written.
	data, err := os.ReadFile(filepath.Join(wb, ".env.dist"))
	if err != nil || string(data) != "EXT_URL=*__POSTGRESDB\n" {
		t.Fatalf(".env.dist = %q err=%v", data, err)
	}

	// 2. GET reports it pending with both matches.
	rec = httptest.NewRecorder()
	handleImports(rec, httptest.NewRequest("GET", "/api/imports/demoapp", nil))
	var got struct {
		Resolutions []ImportResolution `json:"resolutions"`
		Pool        []string           `json:"pool"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Resolutions) != 1 || !got.Resolutions[0].Pending || len(got.Resolutions[0].Matches) != 2 {
		t.Fatalf("resolutions = %+v", got.Resolutions)
	}
	if len(got.Pool) != 2 {
		t.Fatalf("pool = %v", got.Pool)
	}

	// 3. Choose one; the launch may now proceed.
	rec = httptest.NewRecorder()
	handleImports(rec, httptest.NewRequest("POST", "/api/imports/demoapp",
		strings.NewReader(`{"choices":{"EXT_URL":"BILLING__POSTGRESDB"}}`)))
	if rec.Code != 200 {
		t.Fatalf("choose: %d %s", rec.Code, rec.Body.String())
	}
	pending, _ := pendingImports("demoapp")
	if len(pending) != 0 {
		t.Fatalf("still pending: %+v", pending)
	}

	// 4. The value reached .env.
	env, err := os.ReadFile(filepath.Join(wb, ".env"))
	if err != nil {
		t.Fatalf("read .env: %s", err)
	}
	if !strings.Contains(string(env), "EXT_URL=postgres://billing") {
		t.Fatalf(".env = %s", env)
	}
	if strings.Contains(string(env), importRefOpen) {
		t.Fatalf("a reference leaked into .env instead of its value: %s", env)
	}
}

// The reference is the whole value or it is not one: an app must be able to hold
// a literal string that merely contains the delimiters.
func TestImportRefTarget(t *testing.T) {
	for _, tc := range []struct {
		value  string
		target string
		isRef  bool
	}{
		{"${{APPSUITE__POSTGRESDB}}", "APPSUITE__POSTGRESDB", true},
		{"  ${{APPSUITE__POSTGRESDB}}  ", "APPSUITE__POSTGRESDB", true},
		{"${{ APPSUITE__POSTGRESDB }}", "APPSUITE__POSTGRESDB", true},
		{"postgres://user:pass@host/db", "", false},
		{"", "", false},
		{"${{}}", "", false},
		// Single braces are shell syntax, not ours.
		{"${APPSUITE__POSTGRESDB}", "", false},
		// A value that merely embeds one is a literal, not a reference.
		{"prefix ${{X}} suffix", "", false},
		{"${{X}}${{Y}}", "", false},
	} {
		target, isRef := importRefTarget(tc.value)
		if isRef != tc.isRef || target != tc.target {
			t.Errorf("importRefTarget(%q) = (%q, %v), want (%q, %v)",
				tc.value, target, isRef, tc.target, tc.isRef)
		}
	}

	// Round trip.
	if target, isRef := importRefTarget(importRef("A__B")); !isRef || target != "A__B" {
		t.Errorf("round trip failed: (%q, %v)", target, isRef)
	}
}

// A reference is a value like any other, so it survives an env-editor save and
// keeps resolving afterwards. This is what the two-key scheme needed special
// handling for.
func TestReferenceSurvivesEnvEditorSave(t *testing.T) {
	bindTestApp(t,
		map[string]string{"MANY": importRef("BILLING__POSTGRESDB")},
		map[string]string{"BILLING__POSTGRESDB": "postgres://billing"},
		"MANY=*__POSTGRESDB\n")

	// What the editor renders is the resolved value, not the reference.
	rec := httptest.NewRecorder()
	handleGetAppConfig(rec, httptest.NewRequest("GET", "/api/appconfig/demo", nil),
		"demo", filepath.Join(WorkspaceDir, "workspace", "demo"))
	var shown AppEnvConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &shown); err != nil {
		t.Fatalf("decode: %s", err)
	}
	var row *EnvVar
	for i := range shown.Vars {
		if shown.Vars[i].Name == "MANY" {
			row = &shown.Vars[i]
		}
	}
	if row == nil {
		t.Fatal("MANY missing from the editor")
	}
	if row.DevValue != "postgres://billing" {
		t.Errorf("editor shows %q, want the resolved value", row.DevValue)
	}
	if !row.Imported || row.Source != "BILLING__POSTGRESDB" {
		t.Errorf("row = %+v, want it marked imported with its source", row)
	}
}
