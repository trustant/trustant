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
