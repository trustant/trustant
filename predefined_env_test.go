package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Predefined environment variables are a palette the user maintains on the
// Configure page. Two properties matter and are easy to break:
//
//  1. Saving anything else must not erase them. saveWorkspaceConfig writes the
//     whole struct, so every omitted field is lost unless something preserves it.
//  2. They must never reach an application on their own. The only path in is the
//     "Use predefined values" button followed by an explicit save.

func withPredefinedEnvWorkspace(t *testing.T, workspaceJSON string) string {
	t.Helper()
	origWorkspace := WorkspaceDir
	t.Cleanup(func() { WorkspaceDir = origWorkspace })

	root := t.TempDir()
	WorkspaceDir = filepath.Join(root, "workspace")
	if err := os.MkdirAll(WorkspaceDir, 0755); err != nil {
		t.Fatalf("mkdir workspace: %s", err)
	}
	if workspaceJSON != "" {
		path := filepath.Join(WorkspaceDir, "trustable.json")
		if err := os.WriteFile(path, []byte(workspaceJSON), 0644); err != nil {
			t.Fatalf("write workspace config: %s", err)
		}
	}
	return root
}

func readPredefinedEnvFromDisk(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(WorkspaceDir, "trustable.json"))
	if err != nil {
		t.Fatalf("read workspace config: %s", err)
	}
	var cfg trustableConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse workspace config: %s", err)
	}
	return cfg.PredefinedEnv
}

func TestPredefinedEnvRoundTrips(t *testing.T) {
	withPredefinedEnvWorkspace(t, "{}")

	body := `{"vars":[{"name":"STRIPE_KEY","value":"sk_live_1"},{"name":"API_URL","value":"https://api.example.com"}]}`
	recorder := httptest.NewRecorder()
	handlePredefinedEnv(recorder, httptest.NewRequest(http.MethodPost, "/api/predefined-env", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("save returned %d: %s", recorder.Code, recorder.Body.String())
	}

	stored := readPredefinedEnvFromDisk(t)
	if stored["STRIPE_KEY"] != "sk_live_1" || stored["API_URL"] != "https://api.example.com" {
		t.Fatalf("unexpected stored variables: %#v", stored)
	}

	// The GET is sorted so the table renders in a stable order.
	getRecorder := httptest.NewRecorder()
	handlePredefinedEnv(getRecorder, httptest.NewRequest(http.MethodGet, "/api/predefined-env", nil))
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("load returned %d: %s", getRecorder.Code, getRecorder.Body.String())
	}
	var payload struct {
		Vars []PredefinedEnvVar `json:"vars"`
	}
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("parse response: %s", err)
	}
	if len(payload.Vars) != 2 || payload.Vars[0].Name != "API_URL" || payload.Vars[1].Name != "STRIPE_KEY" {
		t.Fatalf("expected variables sorted by name, got %#v", payload.Vars)
	}
}

func TestPredefinedEnvRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"invalid name":   `{"vars":[{"name":"has-dash","value":"x"}]}`,
		"leading digit":  `{"vars":[{"name":"1BAD","value":"x"}]}`,
		"embedded space": `{"vars":[{"name":"A B","value":"x"}]}`,
		"duplicate name": `{"vars":[{"name":"DUP","value":"a"},{"name":"DUP","value":"b"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			withPredefinedEnvWorkspace(t, "{}")
			recorder := httptest.NewRecorder()
			handlePredefinedEnv(recorder, httptest.NewRequest(http.MethodPost, "/api/predefined-env", strings.NewReader(body)))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// A blank row is how the table shows "not filled in yet"; it must not fail the
// save the user just asked for. An empty value is kept: it records the name
// without yet having anything to offer.
func TestPredefinedEnvDropsBlankRowsButKeepsEmptyValues(t *testing.T) {
	withPredefinedEnvWorkspace(t, "{}")

	body := `{"vars":[{"name":"","value":"orphan"},{"name":"  ","value":""},{"name":"PENDING","value":""}]}`
	recorder := httptest.NewRecorder()
	handlePredefinedEnv(recorder, httptest.NewRequest(http.MethodPost, "/api/predefined-env", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("save returned %d: %s", recorder.Code, recorder.Body.String())
	}

	stored := readPredefinedEnvFromDisk(t)
	if len(stored) != 1 {
		t.Fatalf("expected only the named variable to survive, got %#v", stored)
	}
	if value, ok := stored["PENDING"]; !ok || value != "" {
		t.Fatalf("expected PENDING to be stored with an empty value, got %#v", stored)
	}
}

// The regression this feature is most likely to produce: POST /api/configuration
// is a full-document write, so a payload without predefined_env would erase it.
func TestPostConfigurationPreservesPredefinedEnv(t *testing.T) {
	withPredefinedEnvWorkspace(t, `{
  "provider": "ollama",
  "predefined_env": {"STRIPE_KEY": "sk_live_1"},
  "apps": {"demo": {"password": "pw", "development": {}, "production": {}}}
}`)

	// A payload that mentions neither predefined_env nor apps, as a stale tab
	// or any non-Configure client would send.
	body := `{"provider":"ollama","base_url":"http://localhost:11434/v1","api_key":"dummy"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/configuration", strings.NewReader(body))
	handlePostConfiguration(recorder, request)

	// The model probe runs and may fail in a test environment; only persistence
	// matters here, and the handler saves before probing.
	stored := readPredefinedEnvFromDisk(t)
	if stored["STRIPE_KEY"] != "sk_live_1" {
		t.Fatalf("saving the configuration erased the predefined variables: %#v", stored)
	}
}

// Predefined values are offered, never applied. If they leaked into the merged
// config's app maps or the generated .env, a value would reach an application
// without the user ever seeing it in the editor.
func TestPredefinedEnvIsNotAppliedToApplications(t *testing.T) {
	withPredefinedEnvWorkspace(t, `{
  "predefined_env": {"SHARED_SECRET": "value-from-palette"},
  "apps": {"demo": {"password": "pw", "development": {"DECLARED": ""}, "production": {}}}
}`)

	cfg, err := loadTrustableConfig()
	if err != nil {
		t.Fatalf("load merged config: %s", err)
	}
	if cfg.PredefinedEnv["SHARED_SECRET"] != "value-from-palette" {
		t.Fatalf("predefined variables did not survive the merge: %#v", cfg.PredefinedEnv)
	}
	app := cfg.Apps["demo"]
	if app == nil {
		t.Fatal("expected the demo app to be present")
	}
	if _, leaked := app.Development["SHARED_SECRET"]; leaked {
		t.Error("a predefined variable leaked into the app's development map")
	}
	if _, leaked := app.Production["SHARED_SECRET"]; leaked {
		t.Error("a predefined variable leaked into the app's production map")
	}
}

// The fill rule lives in envtable.js. These assertions pin the three properties
// that make the button safe to press, in the style of setup_test.go and
// screenshot_script_test.go.
func TestApplyPredefinedFillRuleIsConservative(t *testing.T) {
	source, err := os.ReadFile("web/js/envtable.js")
	if err != nil {
		t.Fatalf("read envtable.js: %s", err)
	}
	code := string(source)
	if !strings.Contains(code, "EnvTable.prototype.applyPredefined") {
		t.Fatal("envtable.js does not define applyPredefined")
	}
	fragment := code[strings.Index(code, "EnvTable.prototype.applyPredefined"):]
	if end := strings.Index(fragment, "EnvTable.prototype.save"); end > 0 {
		fragment = fragment[:end]
	}
	for name, needle := range map[string]string{
		"skips readonly rows":           "if (v.readonly) return;",
		"only fills empty dev values":   "if ((v.dev_value || '').trim() !== '') return;",
		"only fills declared variables": "hasOwnProperty.call(predefined, v.name)",
	} {
		if !strings.Contains(fragment, needle) {
			t.Errorf("applyPredefined no longer %s (missing %q)", name, needle)
		}
	}
	// The button must not save on the user's behalf.
	if strings.Contains(fragment, "fetch(") {
		t.Error("applyPredefined must not talk to the backend; the user presses Save")
	}
}

// The applist modal is the "screen that appears when there are variables
// missing" the feature targets.
func TestMissingEnvModalOffersPredefinedValues(t *testing.T) {
	page, err := os.ReadFile("web/applist.html")
	if err != nil {
		t.Fatalf("read applist.html: %s", err)
	}
	source := string(page)
	if !strings.Contains(source, `id="missingEnvPredefinedBtn"`) {
		t.Error("the missing-variables modal has no 'Use predefined values' button")
	}
	if !strings.Contains(source, "missingEnvTable.applyPredefined(") {
		t.Error("the button does not apply predefined values to the table")
	}
	if !strings.Contains(source, "/api/predefined-env") {
		t.Error("the modal never reads the predefined variables")
	}
}

// Importing the palette from a file. Two paths reach predefined_env at
// startup: ./.env.default when it exists, and otherwise the AI variables
// derived from the provider settings already resolved for Pi. Both write the
// workspace layer, both must be idempotent, and neither may overwrite a value
// the user set on the Configure page.

// withPredefinedEnvImportDir puts the process in a temp working directory
// holding a base trustable.json, because loadBaseConfig, loadTrustableConfig
// and the .env.default lookup are all CWD-relative.
func withPredefinedEnvImportDir(t *testing.T, baseJSON, workspaceJSON, defaultEnv string) {
	t.Helper()
	withPredefinedEnvWorkspace(t, workspaceJSON)

	dir := t.TempDir()
	if baseJSON == "" {
		baseJSON = "{}"
	}
	if err := os.WriteFile(filepath.Join(dir, "trustable.json"), []byte(baseJSON), 0644); err != nil {
		t.Fatalf("write base config: %s", err)
	}
	if defaultEnv != "" {
		if err := os.WriteFile(filepath.Join(dir, ".env.default"), []byte(defaultEnv), 0644); err != nil {
			t.Fatalf("write .env.default: %s", err)
		}
	}
	t.Chdir(dir)
}

func workspaceConfigBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(WorkspaceDir, "trustable.json"))
	if err != nil {
		t.Fatalf("read workspace config: %s", err)
	}
	return data
}

// Absence is a no-op, not an error: .env.default is optional.
func TestImportDefaultPredefinedEnvMissingFileIsANoOp(t *testing.T) {
	withPredefinedEnvImportDir(t, "{}", `{"predefined_env":{"KEEP":"mine"}}`, "")
	before := workspaceConfigBytes(t)

	if err := importDefaultPredefinedEnv(); err != nil {
		t.Fatalf("import: %s", err)
	}
	if got := string(workspaceConfigBytes(t)); got != string(before) {
		t.Fatalf("the workspace config was rewritten with nothing to import:\n%s", got)
	}
}

func TestImportDefaultPredefinedEnvImportsIntoEmptyPalette(t *testing.T) {
	withPredefinedEnvImportDir(t, "{}", "{}", "# a comment\n\nSTRIPE_KEY=sk_live_1\nAPI_URL=https://api.example.com\n")

	if err := importDefaultPredefinedEnv(); err != nil {
		t.Fatalf("import: %s", err)
	}
	stored := readPredefinedEnvFromDisk(t)
	if stored["STRIPE_KEY"] != "sk_live_1" || stored["API_URL"] != "https://api.example.com" {
		t.Fatalf("unexpected imported variables: %#v", stored)
	}
}

// Keep existing: the import runs on every start, so it must never clobber an
// edit made on the Configure page — including a name recorded with an empty
// value, which is the deliberate "no value yet" state.
func TestImportDefaultPredefinedEnvKeepsExistingValues(t *testing.T) {
	withPredefinedEnvImportDir(t, "{}",
		`{"predefined_env":{"STRIPE_KEY":"sk_mine","PENDING":""}}`,
		"STRIPE_KEY=sk_from_file\nPENDING=value_from_file\nFRESH=new\n")

	if err := importDefaultPredefinedEnv(); err != nil {
		t.Fatalf("import: %s", err)
	}
	stored := readPredefinedEnvFromDisk(t)
	if stored["STRIPE_KEY"] != "sk_mine" {
		t.Errorf("the file overwrote a value the user had set: %q", stored["STRIPE_KEY"])
	}
	if stored["PENDING"] != "" {
		t.Errorf("an empty recorded value was overwritten by the file: %q", stored["PENDING"])
	}
	if stored["FRESH"] != "new" {
		t.Errorf("a new name from the file was not imported: %#v", stored)
	}
}

// An unusable name must not fail the whole import; the rest of the file still
// applies, and the server's own name rule is what decides.
func TestImportDefaultPredefinedEnvSkipsInvalidNames(t *testing.T) {
	withPredefinedEnvImportDir(t, "{}", "{}", "has-dash=x\n1BAD=y\nGOOD=z\n")

	if err := importDefaultPredefinedEnv(); err != nil {
		t.Fatalf("import: %s", err)
	}
	stored := readPredefinedEnvFromDisk(t)
	if len(stored) != 1 || stored["GOOD"] != "z" {
		t.Fatalf("expected only the valid name to be imported, got %#v", stored)
	}
}

// Nothing new to add must not rewrite the file, or the config churns on every
// restart.
func TestImportDefaultPredefinedEnvDoesNotWriteWhenNothingIsAdded(t *testing.T) {
	withPredefinedEnvImportDir(t, "{}", `{"predefined_env":{"ONLY":"mine"}}`, "ONLY=from_file\n")
	before := workspaceConfigBytes(t)

	if err := importDefaultPredefinedEnv(); err != nil {
		t.Fatalf("import: %s", err)
	}
	if got := string(workspaceConfigBytes(t)); got != string(before) {
		t.Fatalf("the workspace config was rewritten with nothing to add:\n%s", got)
	}
}

// The cap is what stops the config file being used as a data store; the import
// must respect it rather than writing a map POST would then reject.
func TestImportDefaultPredefinedEnvStopsAtTheCap(t *testing.T) {
	existing := make(map[string]string, maxPredefinedEnvVars)
	var file strings.Builder
	for i := 0; i < maxPredefinedEnvVars; i++ {
		existing[fmt.Sprintf("EXISTING_%d", i)] = "x"
	}
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&file, "EXTRA_%d=y\n", i)
	}
	wsJSON, err := json.Marshal(map[string]interface{}{"predefined_env": existing})
	if err != nil {
		t.Fatalf("marshal workspace config: %s", err)
	}
	withPredefinedEnvImportDir(t, "{}", string(wsJSON), file.String())

	if err := importDefaultPredefinedEnv(); err != nil {
		t.Fatalf("import: %s", err)
	}
	if stored := readPredefinedEnvFromDisk(t); len(stored) != maxPredefinedEnvVars {
		t.Fatalf("expected the palette to stop at %d, got %d", maxPredefinedEnvVars, len(stored))
	}
}

// The upload path is frontend-only (no build step), so these assertions pin the
// properties that keep it consistent with parseEnvFile and the server's limits,
// in the style of setup_test.go and screenshot_script_test.go.
func TestConfigurePageImportsEnvFilesConsistently(t *testing.T) {
	source, err := os.ReadFile("web/configure.html")
	if err != nil {
		t.Fatalf("read configure.html: %s", err)
	}
	code := string(source)
	for name, needle := range map[string]string{
		"has an import control":         `id="predefinedEnvFile"`,
		"splits on the first '='":       "line.indexOf('=')",
		"skips comments":                "line.startsWith('#')",
		"enforces the server name rule": "/^[A-Za-z_][A-Za-z0-9_]*$/",
		"enforces the 256 cap":          "merged.length > 256",
		"resets the picker":             "input.value = '';",
		"rejects binary input":          `/[\0`,
	} {
		if !strings.Contains(code, needle) {
			t.Errorf("the file import no longer %s (missing %q)", name, needle)
		}
	}

	// Any text file must be selectable: env files are routinely named
	// .env.local, .env.production or env.txt, and an accept filter greys the
	// real ones out in the OS picker. The binary check above is what replaces it.
	control := code[strings.Index(code, `id="predefinedEnvFile"`):]
	if end := strings.Index(control, ">"); end > 0 {
		control = control[:end]
	}
	if strings.Contains(control, "accept=") {
		t.Errorf("the file picker grew an accept filter, which hides validly-named env files: %s", control)
	}
}

// The palette has no Save button: every mutation persists on its own. A palette
// that needed a separate click was silently lost whenever the user pressed the
// page's own Save Configuration, which navigates away on success.
func TestConfigurePagePersistsPaletteWithoutASaveButton(t *testing.T) {
	source, err := os.ReadFile("web/configure.html")
	if err != nil {
		t.Fatalf("read configure.html: %s", err)
	}
	code := string(source)

	// The button and its handler must be gone, not merely hidden.
	for name, needle := range map[string]string{
		"the Save Variables button":  "predefinedEnvSaveBtn",
		"the Save Variables label":   ">Save Variables<",
		"the old explicit save path": "function savePredefinedEnv(",
	} {
		if strings.Contains(code, needle) {
			t.Errorf("%s is back (found %q): the palette must save on edit", name, needle)
		}
	}

	for name, needle := range map[string]string{
		"routes every mutation through one save path": "async function persistPredefinedEnv(",
		"reads the rows out of the DOM at save time":  "function collectPredefinedEnvRows()",
		"uses that read to build the payload":         "predefinedEnvVars = collectPredefinedEnvRows();",
		"marks the name inputs":                       `data-predefined-env="name"`,
		"marks the value inputs":                      `data-predefined-env="value"`,
		"posts to the palette endpoint":               "'/api/predefined-env'",
	} {
		if !strings.Contains(code, needle) {
			t.Errorf("the palette save no longer %s (missing %q)", name, needle)
		}
	}

	// The row inputs must track typing, not only blur.
	rows := code[strings.Index(code, "function renderPredefinedEnv()"):]
	rows = rows[:strings.Index(rows, "// Typing fires oninput per keystroke")]
	if strings.Contains(rows, "onchange=\"updatePredefinedEnv") {
		t.Error("the palette rows went back to onchange, which only commits on blur")
	}
	if strings.Count(rows, "oninput=\"updatePredefinedEnv") != 2 {
		t.Errorf("expected both palette row inputs to bind with oninput:\n%s", rows)
	}
	// ...and blur must flush a pending debounce, or the last keystrokes of an
	// edit the user then navigates away from never reach the server.
	if strings.Count(rows, "onblur=\"flushPredefinedEnv()") != 2 {
		t.Errorf("expected both palette row inputs to flush on blur:\n%s", rows)
	}

	// Typing must be debounced rather than posting once per keystroke.
	if !strings.Contains(code, "PREDEFINED_ENV_DEBOUNCE_MS") {
		t.Error("the edit path no longer debounces, so every keystroke posts")
	}
	// A debounced edit still in flight when the page goes away is exactly the
	// loss this issue was about.
	for _, ev := range []string{"beforeunload", "pagehide"} {
		if !strings.Contains(code, "addEventListener('"+ev+"', flushPredefinedEnv)") {
			t.Errorf("a pending palette edit is no longer flushed on %s", ev)
		}
	}

	// A zero-count save is exactly what the dropped-row bug looked like, so it
	// must never be reported as success.
	if !strings.Contains(code, "if (data.count === 0)") {
		t.Error("a zero-count save is no longer called out separately from a real save")
	}
}

// Adding a row must NOT post: a fresh row has a blank name, the server drops
// blank-name rows, and the resulting "Saved 0 variables" is indistinguishable
// from the silently-dropped-row bug. It persists on the first edit instead.
func TestConfigurePageDoesNotSaveAnEmptyNewRow(t *testing.T) {
	source, err := os.ReadFile("web/configure.html")
	if err != nil {
		t.Fatalf("read configure.html: %s", err)
	}
	code := string(source)
	body := code[strings.Index(code, "function addPredefinedEnvRow()"):]
	body = body[:strings.Index(body, "function removePredefinedEnvRow")]
	if strings.Contains(body, "persistPredefinedEnv") {
		t.Errorf("adding a blank row now posts a no-op save:\n%s", body)
	}
}

// Importing a file must store the merge, not park it in the table behind a
// button that no longer exists.
func TestConfigurePageSavesImportedVariablesImmediately(t *testing.T) {
	source, err := os.ReadFile("web/configure.html")
	if err != nil {
		t.Fatalf("read configure.html: %s", err)
	}
	code := string(source)
	body := code[strings.Index(code, "async function importPredefinedEnvFile("):]
	body = body[:strings.Index(body, "function setPredefinedEnvStatus")]
	if !strings.Contains(body, "persistPredefinedEnv") {
		t.Errorf("an imported env file is no longer saved on import:\n%s", body)
	}
	if strings.Contains(code, "press Save Variables to store them") {
		t.Error("the import still tells the user to press a button that is gone")
	}
}

// .env.default is a LOCAL TESTING affordance and must never be baked into the
// image. Shipping one would hand every deployment a palette nobody chose, and a
// committed seed is a standing invitation to put a credential in it. An empty
// palette in a deployed pod is the intended state, not a bug — the user fills it
// in on the Configure page.
func TestImageDoesNotShipADefaultEnvPalette(t *testing.T) {
	dockerfile, err := os.ReadFile("image/Dockerfile")
	if err != nil {
		t.Fatalf("read image/Dockerfile: %s", err)
	}
	if strings.Contains(string(dockerfile), defaultEnvFileName) {
		t.Errorf("image/Dockerfile copies %s into the image; it is for local testing only",
			defaultEnvFileName)
	}
	if _, err := os.Stat("image/env.default"); err == nil {
		t.Error("image/env.default exists; a palette seed must not be committed to the build context")
	}
}
