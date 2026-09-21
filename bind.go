package main

// Importing variables from the shared pool (spec/19-import.md).
//
// The counterpart of shared.go. That file is the EXPORT side: an app publishes
// its own service secrets into the workspace pool under `<APP>__<NAME>`. This
// file is the IMPORT side: an app declares, in .env.dist, which pool variables
// fill its own variables.
//
// .env.dist is a source file here, not a generated one. Its value field — which
// the old parser discarded — carries the matching pattern:
//
//	DATABASE_PASSWORD=                 exact:    pool entry named DATABASE_PASSWORD
//	EXT_POSTGRESQLURL=*__POSTGRESDB    wildcard: any pool entry matching that shape
//
// An empty pattern is the pre-existing meaning of every .env.dist ever written,
// so old files keep resolving exactly as they did.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// EnvBinding is one .env.dist line: the app's own variable name, and the
// pattern selecting which pool variable fills it.
type EnvBinding struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// Exact reports whether this binding matches by name alone. An empty pattern is
// not a missing declaration: it is the exact-match case, and the only case that
// existed before this feature.
func (b EnvBinding) Exact() bool {
	return !strings.Contains(b.Pattern, "*")
}

// target returns the pool name an exact binding reads. A pattern without a `*`
// is still allowed to rename: `MY_URL=APPSUITE__POSTGRES_URL` binds MY_URL to
// that specific entry, which is what lets a consumer keep its own vocabulary
// without resorting to a wildcard.
func (b EnvBinding) target() string {
	if p := strings.TrimSpace(b.Pattern); p != "" {
		return p
	}
	return b.Name
}

// ImportResolution is one row of the launch popup: what the app asks for, what
// the pool can offer, and what it will get.
type ImportResolution struct {
	Name string `json:"name"`
	// Pattern is empty for a variable that is not an import at all — one that
	// already carries a value in the app config.
	Pattern string `json:"pattern"`
	// Matches are the pool names this binding can draw from, sorted. Always
	// populated for a wildcard so the frontend can render the pull-down, even
	// when the binding is already resolved and the user is only re-pointing it.
	Matches []string `json:"matches"`
	// Source is the pool name the value comes from; empty when the value was
	// typed by hand or supplied by the user through the popup.
	Source string `json:"source"`
	Value  string `json:"value"`
	// Pending is true when the launch cannot proceed until the user acts: no
	// match, or several with no choice recorded.
	Pending bool `json:"pending"`
}

// parseEnvDistBindings returns the bindings declared by a .env.dist body, in
// file order. It replaces parseEnvDistNames, which threw the value away; the
// value is now the pattern.
//
// Duplicate names keep the FIRST occurrence. A later line would otherwise
// silently reassign a variable declared above it, and the file is committed:
// the author of the first line is not there to notice.
func parseEnvDistBindings(content string) []EnvBinding {
	var bindings []EnvBinding
	seen := make(map[string]bool)
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:eq])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		// Everything after the first `=` is the pattern, so a pool name
		// containing `=` survives. Quotes are stripped: .env syntax allows them
		// and a quoted pattern would otherwise never match anything.
		pattern := strings.TrimSpace(line[eq+1:])
		pattern = strings.Trim(pattern, `"'`)
		bindings = append(bindings, EnvBinding{Name: name, Pattern: pattern})
	}
	return bindings
}

// parseEnvDistNames keeps the old name-only view for callers that only need the
// declared names.
func parseEnvDistNames(content string) []string {
	bindings := parseEnvDistBindings(content)
	names := make([]string, 0, len(bindings))
	for _, b := range bindings {
		names = append(names, b.Name)
	}
	return names
}

// readAppEnvDistBindings returns an app's declarations, preferring the workbench
// checkout and falling back to the workspace bare repo so an app that has never
// been launched can still be inspected. Missing file → nil.
func readAppEnvDistBindings(appName string) []EnvBinding {
	if data, err := os.ReadFile(filepath.Join(WorkbenchDir, appName, ".env.dist")); err == nil {
		return parseEnvDistBindings(string(data))
	}

	workspacePath := filepath.Join(WorkspaceDir, "workspace", appName)
	if _, err := os.Stat(workspacePath); err != nil {
		return nil
	}
	cmd := exec.Command("git", "show", "HEAD:.env.dist")
	cmd.Dir = workspacePath
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseEnvDistBindings(string(output))
}

// writeEnvDistBindings serializes the declarations back, one per line, in the
// order given. Unlike the generator it replaces, this preserves the pattern:
// the file is what the user wrote in the Import tab.
//
// An empty set removes the file rather than committing a zero-byte one — an app
// that imports nothing declares no contract. Reports whether anything changed so
// the caller can skip the commit.
func writeEnvDistBindings(path string, bindings []EnvBinding) (bool, error) {
	if len(bindings) == 0 {
		if _, err := os.Stat(path); err != nil {
			return false, nil
		}
		if err := os.Remove(path); err != nil {
			return false, err
		}
		return true, nil
	}

	var sb strings.Builder
	for _, b := range bindings {
		sb.WriteString(b.Name)
		sb.WriteString("=")
		sb.WriteString(b.Pattern)
		sb.WriteString("\n")
	}
	content := sb.String()

	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, err
	}
	return true, nil
}

// matchPoolVariables returns the pool names a pattern selects, sorted so the
// pull-down order is stable across refreshes.
//
// `*` matches any run of characters, including none. It is the only
// metacharacter: these patterns are written by hand into a committed file, and
// a full glob or regex would turn a typo into a silent mismatch.
func matchPoolVariables(pattern string, pool map[string]string) []string {
	var names []string
	for name := range pool {
		if matchWildcard(pattern, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// matchWildcard reports whether name matches a pattern whose only metacharacter
// is `*`. A pattern with no `*` is an equality test.
func matchWildcard(pattern, name string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == name
	}

	parts := strings.Split(pattern, "*")
	// The segment before the first `*` must be a prefix, and the one after the
	// last must be a suffix; everything between is matched in order, greedily.
	if !strings.HasPrefix(name, parts[0]) {
		return false
	}
	rest := name[len(parts[0]):]
	last := len(parts) - 1
	for _, part := range parts[1:last] {
		if part == "" {
			continue
		}
		idx := strings.Index(rest, part)
		if idx < 0 {
			return false
		}
		rest = rest[idx+len(part):]
	}
	if parts[last] == "" {
		return true
	}
	return strings.HasSuffix(rest, parts[last]) && len(rest) >= len(parts[last])
}

// A wildcard's chosen producer is recorded as a REFERENCE in the variable's own
// value, not as a second entry beside it:
//
//	"EXT_URL": "${{BILLING__POSTGRESDB}}"
//
// The value is the binding, so there is no bookkeeping key to hide from .env
// generation, from the env editor, or from a save that rebuilds the map from the
// posted rows. One variable, one entry.
//
// WHY the app config and not .env.dist: .env.dist is committed, and the choice
// is installation-specific. The same repo on another machine has a different set
// of producers installed, so exporting the choice would hand a clone a binding
// that names a producer it does not have.
//
// `${{...}}` rather than `${...}`: the single-brace form is shell syntax, and
// these values are written into a .env file that a shell may well source.
const (
	importRefOpen  = "${{"
	importRefClose = "}}"
)

// importRef renders a reference to a pool entry.
func importRef(source string) string {
	return importRefOpen + source + importRefClose
}

// importRefTarget returns the pool name a value refers to, and whether it is a
// reference at all. A value that merely contains the delimiters somewhere is not
// one: the whole value must be the reference, or an app could not hold a literal
// string that happens to look like one.
func importRefTarget(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if !strings.HasPrefix(v, importRefOpen) || !strings.HasSuffix(v, importRefClose) {
		return "", false
	}
	inner := strings.TrimSpace(v[len(importRefOpen) : len(v)-len(importRefClose)])
	if inner == "" || strings.Contains(inner, importRefOpen) {
		return "", false
	}
	return inner, true
}

// resolveImports answers, for every variable the app has, what it will be set to
// at launch — and which ones the user still has to decide.
//
// The result is the whole popup: variables already carrying a value, imports
// resolved from the pool, and imports that are pending. Returning all of them
// rather than only the pending ones is deliberate — the popup shows the complete
// picture, so the user can re-point a binding that is already resolved.
func resolveImports(appName string) ([]ImportResolution, error) {
	cfg, err := loadTrustableConfig()
	if err != nil {
		return nil, err
	}
	appCfg := cfg.Apps[appName]
	dev := map[string]string{}
	if appCfg != nil && appCfg.Development != nil {
		dev = appCfg.Development
	}

	var out []ImportResolution
	declared := make(map[string]bool)

	for _, b := range readAppEnvDistBindings(appName) {
		if isEnvDistFixedKey(b.Name) || isServiceRuntimeEnvKey(b.Name) {
			continue
		}
		declared[b.Name] = true
		out = append(out, resolveOneImport(b, dev, cfg.PredefinedEnv))
	}

	// Variables that carry their own value are not imports, but the popup states
	// that every variable ends up with a value, so it has to show them too.
	var plain []string
	for name, value := range dev {
		if declared[name] || isEnvDistFixedKey(name) || isServiceRuntimeEnvKey(name) {
			continue
		}
		plain = append(plain, name)
		_ = value
	}
	sort.Strings(plain)
	for _, name := range plain {
		out = append(out, ImportResolution{
			Name:    name,
			Value:   dev[name],
			Pending: strings.TrimSpace(dev[name]) == "",
		})
	}

	return out, nil
}

// resolveOneImport applies the precedence: a value the user typed wins, then a
// recorded choice, then an unambiguous match.
func resolveOneImport(b EnvBinding, dev, pool map[string]string) ImportResolution {
	res := ImportResolution{Name: b.Name, Pattern: b.Pattern}

	if !b.Exact() {
		res.Matches = matchPoolVariables(b.Pattern, pool)
	}

	if v := strings.TrimSpace(dev[b.Name]); v != "" {
		if choice, isRef := importRefTarget(v); isRef {
			// The value is a reference, so the binding tracks its producer: a
			// rotated credential is picked up rather than a stale copy.
			if poolValue, ok := pool[choice]; ok {
				res.Source, res.Value = choice, poolValue
				return res
			}
			// The referenced producer is gone. Re-pend rather than keep serving a
			// value from a producer that no longer exists — silently falling back
			// to another match would point the app at a different database.
			res.Pending = true
			return res
		}
		// A literal value typed by hand is the escape hatch for a producer that
		// is not installed here, so it outranks the pool.
		res.Value = v
		return res
	}

	if b.Exact() {
		if v, ok := pool[b.target()]; ok {
			res.Source, res.Value = b.target(), v
			return res
		}
		res.Pending = true
		return res
	}

	// Exactly one match needs no decision; zero or several do.
	if len(res.Matches) == 1 {
		res.Source, res.Value = res.Matches[0], pool[res.Matches[0]]
		return res
	}

	res.Pending = true
	return res
}

// pendingImports is the launch gate: the rows the user must resolve before the
// app can start. Replaces missingAppEnvKeys, which could only report names.
func pendingImports(appName string) ([]ImportResolution, error) {
	all, err := resolveImports(appName)
	if err != nil {
		return nil, err
	}
	var pending []ImportResolution
	for _, r := range all {
		if r.Pending {
			pending = append(pending, r)
		}
	}
	return pending, nil
}

// applyImportChoices records the user's answers from the popup. A choice naming
// a pool entry stores the entry name so the binding keeps tracking it; a choice
// carrying a literal value stores the value.
//
// Validation is total, like #227's save path: one bad row writes nothing. A
// partial apply would launch the app with some variables bound and others not,
// which is the state this whole feature exists to prevent.
func applyImportChoices(appName string, choices map[string]string, literals map[string]string) error {
	cfg, err := loadTrustableConfig()
	if err != nil {
		return err
	}
	for _, source := range choices {
		if strings.TrimSpace(source) == "" {
			continue
		}
		if _, ok := cfg.PredefinedEnv[source]; !ok {
			return fmt.Errorf("shared variable %q does not exist", source)
		}
	}

	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		return err
	}
	if wsCfg.Apps == nil {
		wsCfg.Apps = make(map[string]*AppConfig)
	}
	if wsCfg.Apps[appName] == nil {
		wsCfg.Apps[appName] = &AppConfig{}
	}
	appCfg := wsCfg.Apps[appName]
	if appCfg.Development == nil {
		appCfg.Development = make(map[string]string)
	}

	// The reference IS the value, so choosing a producer and typing a literal are
	// the same write. A literal simply replaces the reference, which is how an
	// override stops tracking the pool.
	for name, source := range choices {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		appCfg.Development[name] = importRef(source)
	}
	for name, value := range literals {
		if strings.TrimSpace(value) == "" {
			continue
		}
		appCfg.Development[name] = value
	}

	return saveWorkspaceConfig(wsCfg)
}

// --- HTTP ------------------------------------------------------------------

// handleImports dispatches /api/imports/<app>.
//
//	GET  — the app's declarations plus their current resolution, for the Import
//	       tab and for the launch popup.
//	POST — either a full rewrite of .env.dist (the Import tab's Save), or the
//	       user's answers from the launch popup.
func handleImports(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	app := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/imports"), "/")
	if !namePattern.MatchString(app) {
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleImportsGet(w, app)
	case http.MethodPost:
		handleImportsSave(w, r, app)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleImportsGet(w http.ResponseWriter, app string) {
	resolutions, err := resolveImports(app)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cfg, err := loadTrustableConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	poolNames := make([]string, 0, len(cfg.PredefinedEnv))
	for name := range cfg.PredefinedEnv {
		poolNames = append(poolNames, name)
	}
	sort.Strings(poolNames)

	bindings := readAppEnvDistBindings(app)
	if bindings == nil {
		bindings = []EnvBinding{}
	}
	if resolutions == nil {
		resolutions = []ImportResolution{}
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]interface{}{
		"app":         app,
		"bindings":    bindings,
		"resolutions": resolutions,
		"pool":        poolNames,
	})
}

// importsSaveRequest carries either side of the POST. `bindings` rewrites
// .env.dist wholesale, like the Export picker: a row the user removed must
// disappear, which a merge could not express. `choices` and `values` carry the
// launch popup's answers.
type importsSaveRequest struct {
	Bindings *[]EnvBinding     `json:"bindings"`
	Choices  map[string]string `json:"choices"`
	Values   map[string]string `json:"values"`
}

func handleImportsSave(w http.ResponseWriter, r *http.Request, app string) {
	var req importsSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Bindings != nil {
		if err := saveImportBindings(app, *req.Bindings); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if len(req.Choices) > 0 || len(req.Values) > 0 {
		if err := applyImportChoices(app, req.Choices, req.Values); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		// The app's .env must reflect the new values straight away: the popup is
		// shown mid-launch, and the launch continues from the config.
		if err := generateAppEnvFiles(app); err != nil {
			log.Printf("Warning: failed to regenerate env files for %s: %s", app, err)
		}
	}

	handleImportsGet(w, app)
}

// saveImportBindings validates and writes .env.dist. Validation is total: one
// bad row writes nothing, so a partial save can never leave .env and .env.dist
// overlapping — the disjunction is what makes each variable live in exactly one
// of the two.
func saveImportBindings(app string, bindings []EnvBinding) error {
	cfg, err := loadTrustableConfig()
	if err != nil {
		return err
	}
	appCfg := cfg.Apps[app]

	seen := make(map[string]bool)
	cleaned := make([]EnvBinding, 0, len(bindings))
	for _, b := range bindings {
		name := strings.TrimSpace(b.Name)
		if name == "" {
			continue
		}
		if !predefinedEnvNamePattern.MatchString(name) {
			return fmt.Errorf("invalid variable name %q", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate variable %q", name)
		}
		if isEnvDistFixedKey(name) || isServiceRuntimeEnvKey(name) {
			return fmt.Errorf("%q is supplied by the server and cannot be imported", name)
		}
		// The disjunction. A name that already has a value is owned by the env
		// editor; importing it too would give one variable two sources.
		if appCfg != nil && strings.TrimSpace(appCfg.Development[name]) != "" {
			return fmt.Errorf("%q already has a value in this app's environment — remove it there first", name)
		}
		seen[name] = true
		cleaned = append(cleaned, EnvBinding{Name: name, Pattern: strings.TrimSpace(b.Pattern)})
	}

	workbenchPath := filepath.Join(WorkbenchDir, app)
	if _, err := os.Stat(workbenchPath); err != nil {
		return fmt.Errorf("application %s has no workbench — launch it once first", app)
	}

	changed, err := writeEnvDistBindings(filepath.Join(workbenchPath, ".env.dist"), cleaned)
	if err != nil {
		return err
	}
	if changed {
		// Committed for the same reason .env.shared is: it is the contract a clone
		// reads to discover what the app needs.
		commitEnvDist(workbenchPath)
	}
	return nil
}
