package main

// Sharing service secrets between apps — see spec/18-shared.md.
//
// A producing app declares which of its own ~/.ops/config.json secrets it
// publishes, in a committed .env.shared at the root of its repo:
//
//	<app>__POSTGRES_URL=<.postgres.url>
//
// Both halves are placeholders. `<app>` is the literal string, not the app's
// name: the producing app is implied by the repo the file lives in, so the file
// survives a rename, a fork, or an install under a different name. The value is
// a dot-path into ~/.ops/config.json in angle brackets — never a value, because
// the file is committed exactly like .env.dist.
//
// Resolution expands `<app>` to the app name uppercased and looks the path up
// against a live `ops ide login`, then stores the result in the workspace pool
// (predefined_env, surfaced as "Shared Variables"). Consumers never log in as
// the producer: they declare the expanded name and generateAppEnvFiles fills it
// from the pool. Production values are kept per apihost, because the same
// variable name means a different secret on a different cluster.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sharedEnvFileName is the committed declaration at the root of a producing
// app's repo. It holds pointers, never values.
const sharedEnvFileName = ".env.shared"

// sharedAppPlaceholder is the literal left-hand placeholder. It is not the app's
// name: keeping it generic is what lets the same file work after a rename or a
// clone under a different name.
const sharedAppPlaceholder = "<app>"

// sharedNameSeparator divides the producing app from the variable name in a
// resolved name. Doubled so the producer stays machine-recoverable by splitting
// on the first occurrence even when the app name itself contains an underscore.
const sharedNameSeparator = "__"

// sharedEnvPath returns the .env.shared of an app's workbench checkout.
// Resolution needs a checkout anyway — ops ide login runs with Dir set to it.
func sharedEnvPath(app string) string {
	return filepath.Join(WorkbenchDir, app, sharedEnvFileName)
}

// loadSharedEnv reads an app's .env.shared in its stored template form,
// `<app>__NAME` -> `<.path>`. A missing file is not an error: most apps share
// nothing.
func loadSharedEnv(app string) map[string]string {
	return parseEnvFile(sharedEnvPath(app))
}

// saveSharedEnv writes the template form, sorted, so the committed diff is
// stable regardless of Go's map iteration order.
func saveSharedEnv(app string, vars map[string]string) error {
	path := sharedEnvPath(app)
	// An app that shares nothing declares nothing; remove a stale file rather
	// than committing an empty one.
	if len(vars) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString("=")
		b.WriteString(vars[name])
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// sharedVarPrefix is the prefix every variable an app shares must carry once
// expanded: the app name uppercased plus the separator.
func sharedVarPrefix(app string) string {
	return strings.ToUpper(app) + sharedNameSeparator
}

// expandSharedName turns the stored `<app>__NAME` into the effective
// `APPNAME__NAME`. A name that does not carry the placeholder is returned
// unchanged, so a hand-written file that already spells the app out still works.
func expandSharedName(name, app string) string {
	if strings.HasPrefix(name, sharedAppPlaceholder) {
		return strings.ToUpper(app) + strings.TrimPrefix(name, sharedAppPlaceholder)
	}
	return name
}

// templateSharedName is the inverse: the form stored in the file, with the app's
// own name folded back into the placeholder.
func templateSharedName(name, app string) string {
	prefix := sharedVarPrefix(app)
	if strings.HasPrefix(strings.ToUpper(name), prefix) {
		return sharedAppPlaceholder + sharedNameSeparator + name[len(prefix):]
	}
	return name
}

// sharedPathOf unwraps `<.postgres.url>` into `postgres.url`. A value not in
// that form is rejected rather than used: a bare value in this file is a leaked
// secret, not a pointer, and must never be treated as one.
func sharedPathOf(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "<") || !strings.HasSuffix(value, ">") || len(value) < 3 {
		return "", false
	}
	path := strings.TrimPrefix(strings.TrimSuffix(value[1:len(value)-1], "."), ".")
	if path == "" {
		return "", false
	}
	return path, true
}

// wrapSharedPath is the inverse, producing the stored `<.path>` form.
func wrapSharedPath(path string) string {
	return "<." + strings.TrimPrefix(strings.TrimSpace(path), ".") + ">"
}

// splitSharedVarName recovers the producing app from a resolved name. Splitting
// on the FIRST separator is what makes an app name containing an underscore
// unambiguous.
func splitSharedVarName(name string) (string, bool) {
	idx := strings.Index(name, sharedNameSeparator)
	if idx <= 0 || idx+len(sharedNameSeparator) >= len(name) {
		return "", false
	}
	return name[:idx], true
}

// sharedProducerOf reports which known app produces a name, if any. Ownership is
// recoverable from the name alone, which is why nothing records it: a name is
// app-produced exactly when its prefix matches an app that exists.
func sharedProducerOf(name string, apps map[string]*AppConfig) (string, bool) {
	prefix, ok := splitSharedVarName(name)
	if !ok {
		return "", false
	}
	for app := range apps {
		if strings.EqualFold(app, prefix) {
			return app, true
		}
	}
	return "", false
}

// sharedHostKey folds the forms of one apihost into a single production-pool
// key, so https://api.nuvolaris.io/ and api.nuvolaris.io are not two pools.
//
// Deliberately not license.go's normalizeAPIHost: that one keeps the scheme and
// rejects a schemeless host, because a license names hosts exactly. Here the
// same cluster written either way must land in one pool, so the scheme is
// stripped rather than required.
func sharedHostKey(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	return strings.TrimSuffix(host, "/")
}

// removeOpsConfig deletes ~/.ops/config.json. A missing file is success.
//
// This runs before EVERY ops ide login. The command merges into that single
// global file rather than replacing it, so blocks written by a previous login
// for a different app survive and are indistinguishable from the current app's.
// That is not cosmetic: a path picked under a stale block resolves on the
// machine that authored the .env.shared and resolves to nothing on a fresh
// installation, producing a declaration that works for its author and nobody
// else. The same file drives MCP generation and appServiceRuntimeEnv, so a
// stale block is a wrong service binding.
//
// Consequence: a failed login now leaves NO config rather than a stale one.
// That is the safer failure — nothing reads a wrong binding — but it means a
// failed restore leaves the file absent until the next login.
func removeOpsConfig() {
	path := opsConfigPath()
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove %s before ops ide login: %s", path, err)
	}
}

// opsConfigTree reads ~/.ops/config.json as generic JSON for the picker.
//
// Deliberately NOT redacted: the picker exists precisely so the user can see
// and choose secrets, in a local file they already own. The UI masks them on
// screen instead. The auth block is excluded — it is credentials for the CLI
// itself, never something an app shares.
func opsConfigTree() (map[string]interface{}, error) {
	path := opsConfigPath()
	if path == "" {
		return map[string]interface{}{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{}, nil
		}
		return nil, err
	}
	var tree map[string]interface{}
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	delete(tree, "auth")
	return tree, nil
}

// lookupOpsPath resolves a dot-path to a leaf. Only string/number/bool leaves
// are shareable: an object node is a group of secrets, not a value, and
// stringifying one would put a JSON blob into an env var.
func lookupOpsPath(tree map[string]interface{}, path string) (string, bool) {
	segments := strings.Split(path, ".")
	var node interface{} = tree
	for _, segment := range segments {
		obj, ok := node.(map[string]interface{})
		if !ok {
			return "", false
		}
		node, ok = obj[segment]
		if !ok {
			return "", false
		}
	}
	switch v := node.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		// 'f' with precision -1 so a port is 5432, not 5432.000000.
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case json.Number:
		return v.String(), true
	}
	return "", false
}

// opsLoginForApp logs in as one app. generateAppEnvFiles writes the
// OPS_APIHOST/OPS_USER/OPS_PASSWORD the command reads from the workbench .env,
// exactly as the launch and publish paths do.
//
// Callers must hold the runtime lifecycle lock: this rewrites the single global
// ~/.ops/config.json, so running it while another app's launch is mid-flight
// would hand that launch the wrong service bindings.
func opsLoginForApp(app string, production bool) error {
	if app == "" {
		return nil
	}
	workbenchPath := filepath.Join(WorkbenchDir, app)
	if _, err := os.Stat(workbenchPath); err != nil {
		return fmt.Errorf("no workbench for %s", app)
	}
	if err := generateAppEnvFilesNoShared(app); err != nil {
		return fmt.Errorf("generate env for %s: %w", app, err)
	}
	removeOpsConfig()
	args := []string{"ide", "login"}
	if production {
		args = append(args, "--mode=production")
	}
	cmd := exec.Command("ops", args...)
	cmd.Dir = workbenchPath
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ops ide login for %s: %s", app, strings.TrimSpace(string(output)))
	}
	return nil
}

// refreshSharedFrom logs in as one producing app and resolves its .env.shared
// into effective name -> value. An unresolvable pointer is logged and skipped,
// never fatal: an app must stay usable when a service it does not itself need is
// unavailable. Only names are logged, never values.
func refreshSharedFrom(app string, production bool) (map[string]string, error) {
	declared := loadSharedEnv(app)
	if len(declared) == 0 {
		return nil, nil
	}
	if err := opsLoginForApp(app, production); err != nil {
		return nil, err
	}
	tree, err := opsConfigTree()
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]string)
	for rawName, rawValue := range declared {
		name := expandSharedName(rawName, app)
		if !predefinedEnvNamePattern.MatchString(name) {
			log.Printf("Shared: skipping invalid variable name %q declared by %s", rawName, app)
			continue
		}
		// A .env.shared written by hand can carry a name without the app's own
		// prefix. Skip it rather than failing — the app must stay installable —
		// but never let it into the flat pool, where it could collide with
		// another app's variable of the same name.
		if !strings.HasPrefix(name, sharedVarPrefix(app)) {
			log.Printf("Shared: skipping %s declared by %s, missing the %s prefix", name, app, sharedVarPrefix(app))
			continue
		}
		path, ok := sharedPathOf(rawValue)
		if !ok {
			log.Printf("Shared: skipping %s declared by %s, value is not a <.path> pointer", name, app)
			continue
		}
		value, ok := lookupOpsPath(tree, path)
		if !ok {
			log.Printf("Shared: %s declared by %s does not resolve (%s not found in ops config)", name, app, path)
			continue
		}
		resolved[name] = value
	}
	return resolved, nil
}

// sharedProducingApps lists the apps that declare anything, sorted so the
// refresh order — and therefore the log — is deterministic.
func sharedProducingApps(cfg *trustantConfig) []string {
	var apps []string
	for app := range cfg.Apps {
		if len(loadSharedEnv(app)) > 0 {
			apps = append(apps, app)
		}
	}
	sort.Strings(apps)
	return apps
}

// foldIntoSharedPool writes resolved values into the development pool.
//
// An app-produced name is OVERWRITTEN: it belongs to its producer, and a
// refresh that could not update it would serve a stale credential forever. A
// name the user typed by hand is KEPT: the palette is theirs. Ownership comes
// from the name, so no bookkeeping records it.
func foldIntoSharedPool(wsCfg *trustantConfig, resolved map[string]string, apps map[string]*AppConfig) []string {
	if len(resolved) == 0 {
		return nil
	}
	if wsCfg.PredefinedEnv == nil {
		wsCfg.PredefinedEnv = make(map[string]string)
	}
	names := make([]string, 0, len(resolved))
	for name := range resolved {
		names = append(names, name)
	}
	sort.Strings(names)

	var stored, dropped []string
	for _, name := range names {
		_, appProduced := sharedProducerOf(name, apps)
		if !appProduced {
			if _, exists := wsCfg.PredefinedEnv[name]; exists {
				continue
			}
		}
		if _, exists := wsCfg.PredefinedEnv[name]; !exists && len(wsCfg.PredefinedEnv) >= maxPredefinedEnvVars {
			dropped = append(dropped, name)
			continue
		}
		wsCfg.PredefinedEnv[name] = resolved[name]
		stored = append(stored, name)
	}
	if len(dropped) > 0 {
		log.Printf("Shared: dropped %d variables, the pool is at its limit of %d: %v", len(dropped), maxPredefinedEnvVars, dropped)
	}
	return stored
}

// foldIntoProductionPool is the same for one apihost. Production values are
// kept per host: the same variable name means a different secret on a different
// cluster, and one flat map would hand an app the wrong one.
func foldIntoProductionPool(wsCfg *trustantConfig, host string, resolved map[string]string, apps map[string]*AppConfig) []string {
	host = sharedHostKey(host)
	if host == "" || len(resolved) == 0 {
		return nil
	}
	if wsCfg.PredefinedEnvProduction == nil {
		wsCfg.PredefinedEnvProduction = make(map[string]map[string]string)
	}
	if wsCfg.PredefinedEnvProduction[host] == nil {
		wsCfg.PredefinedEnvProduction[host] = make(map[string]string)
	}
	pool := wsCfg.PredefinedEnvProduction[host]

	names := make([]string, 0, len(resolved))
	for name := range resolved {
		names = append(names, name)
	}
	sort.Strings(names)

	var stored []string
	for _, name := range names {
		// A value typed in by hand for this host is the escape hatch for a
		// producer that lives on another installation. A later publish of the
		// producer must not silently replace it.
		if _, appProduced := sharedProducerOf(name, apps); !appProduced {
			if _, exists := pool[name]; exists {
				continue
			}
		}
		pool[name] = resolved[name]
		stored = append(stored, name)
	}
	return stored
}

// pruneSharedPool removes every pool entry produced by one app, from the
// development pool and from every per-host production pool. It returns how many
// entries it removed and does NOT save: the caller writes, so that the prune and
// whatever else it is doing to the config land in a single write.
//
// The pool is otherwise append-only — foldIntoSharedPool only inserts and
// overwrites — so this is the one path by which an app's exports leave it.
// Without it a deleted app's resolved secrets stay in the workspace config
// forever, and worse, they stop being recognised as app-produced: ownership is
// derived by matching the name's prefix against the apps that exist, so once the
// app is gone its orphans are indistinguishable from variables the user typed by
// hand. They then become editable in the UI, are never refreshed again, and are
// reclaimed by any app later created with the same name.
//
// Matching is on the name prefix directly rather than through sharedProducerOf,
// because at the point of deletion the app may already be gone from cfg.Apps —
// which would make sharedProducerOf report "not app-produced" for exactly the
// keys being pruned.
func pruneSharedPool(wsCfg *trustantConfig, app string) int {
	app = strings.TrimSpace(app)
	if wsCfg == nil || app == "" {
		return 0
	}

	producedBy := func(name string) bool {
		prefix, ok := splitSharedVarName(name)
		return ok && strings.EqualFold(prefix, app)
	}

	removed := 0
	for name := range wsCfg.PredefinedEnv {
		if producedBy(name) {
			delete(wsCfg.PredefinedEnv, name)
			removed++
		}
	}
	// An empty map would serialize as `"predefined_env": {}`; the workspace file
	// uses omitempty to stay small, so drop it entirely.
	if len(wsCfg.PredefinedEnv) == 0 {
		wsCfg.PredefinedEnv = nil
	}

	for host, pool := range wsCfg.PredefinedEnvProduction {
		for name := range pool {
			if producedBy(name) {
				delete(pool, name)
				removed++
			}
		}
		if len(pool) == 0 {
			delete(wsCfg.PredefinedEnvProduction, host)
		}
	}
	if len(wsCfg.PredefinedEnvProduction) == 0 {
		wsCfg.PredefinedEnvProduction = nil
	}

	return removed
}

// removeSharedPoolVar removes one entry by name from the development pool and
// from every per-host production pool, whoever produced it. Returns how many
// entries it removed; it does not save.
//
// Removing by name is safe where the bulk POST is not. That POST carries the
// whole set, so honouring an absent key would let a stale tab silently drop a
// live export — which is why handlePostPredefinedEnv carries app-produced
// entries over. A request naming one key states its intent unambiguously and
// cannot be issued by accident.
func removeSharedPoolVar(wsCfg *trustantConfig, name string) int {
	name = strings.TrimSpace(name)
	if wsCfg == nil || name == "" {
		return 0
	}

	removed := 0
	if _, ok := wsCfg.PredefinedEnv[name]; ok {
		delete(wsCfg.PredefinedEnv, name)
		removed++
	}
	if len(wsCfg.PredefinedEnv) == 0 {
		wsCfg.PredefinedEnv = nil
	}

	for host, pool := range wsCfg.PredefinedEnvProduction {
		if _, ok := pool[name]; ok {
			delete(pool, name)
			removed++
		}
		if len(pool) == 0 {
			delete(wsCfg.PredefinedEnvProduction, host)
		}
	}
	if len(wsCfg.PredefinedEnvProduction) == 0 {
		wsCfg.PredefinedEnvProduction = nil
	}

	return removed
}

// refreshSharedPool re-resolves every producing app into the development pool,
// then logs back in as restoreApp so the caller's own service bindings are the
// ones left in ~/.ops/config.json.
//
// Called before every launch. Service credentials are regenerated on every
// ops ide login, so a pool refreshed only when the picker saves would hand a
// consumer a stale secret and the failure would look like an application bug.
// Caller must hold the runtime lifecycle lock.
func refreshSharedPool(restoreApp string) {
	cfg, err := loadTrustantConfig()
	if err != nil {
		log.Printf("Warning: failed to load config for shared refresh: %s", err)
		return
	}
	producers := sharedProducingApps(cfg)
	if len(producers) == 0 {
		return
	}

	resolved := make(map[string]string)
	for _, app := range producers {
		values, err := refreshSharedFrom(app, false)
		if err != nil {
			log.Printf("Warning: shared refresh from %s failed, keeping previous values: %s", app, err)
			continue
		}
		for name, value := range values {
			resolved[name] = value
		}
	}

	// Restore the caller's login before anything else runs: the loop above left
	// the last producer's bindings in the global config.
	if restoreApp != "" {
		if err := opsLoginForApp(restoreApp, false); err != nil {
			log.Printf("Warning: failed to restore ops login for %s after shared refresh: %s", restoreApp, err)
		}
	}

	if len(resolved) == 0 {
		return
	}
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		log.Printf("Warning: failed to load workspace config for shared refresh: %s", err)
		return
	}
	stored := foldIntoSharedPool(wsCfg, resolved, cfg.Apps)
	if len(stored) == 0 {
		return
	}
	if err := saveWorkspaceConfig(wsCfg); err != nil {
		log.Printf("Warning: failed to save shared variables: %s", err)
		return
	}
	log.Printf("Shared: refreshed %d variables into the pool: %v", len(stored), stored)
}

// refreshSharedForApp resolves one app's declarations into the pool. Used when
// the picker saves and when an app carrying a .env.shared is installed.
// Caller must hold the runtime lifecycle lock.
func refreshSharedForApp(app string) ([]string, error) {
	resolved, err := refreshSharedFrom(app, false)
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	cfg, err := loadTrustantConfig()
	if err != nil {
		return nil, err
	}
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		return nil, err
	}
	stored := foldIntoSharedPool(wsCfg, resolved, cfg.Apps)
	if len(stored) == 0 {
		return nil, nil
	}
	if err := saveWorkspaceConfig(wsCfg); err != nil {
		return nil, err
	}
	log.Printf("Shared: stored %d variables produced by %s: %v", len(stored), app, stored)
	return stored, nil
}

// resolveProductionShared stores one app's declarations for one apihost, after
// the publish path has already logged in with --mode=production. Only the app
// being published is resolved: a production login is a real operation against a
// real cluster, and sweeping every producer would log into clusters the user
// never asked to touch.
// Caller must hold the runtime lifecycle lock.
func resolveProductionShared(app, host string) ([]string, error) {
	declared := loadSharedEnv(app)
	if len(declared) == 0 {
		return nil, nil
	}
	tree, err := opsConfigTree()
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]string)
	for rawName, rawValue := range declared {
		name := expandSharedName(rawName, app)
		if !strings.HasPrefix(name, sharedVarPrefix(app)) || !predefinedEnvNamePattern.MatchString(name) {
			continue
		}
		path, ok := sharedPathOf(rawValue)
		if !ok {
			continue
		}
		value, ok := lookupOpsPath(tree, path)
		if !ok {
			log.Printf("Shared: %s declared by %s does not resolve on %s", name, app, host)
			continue
		}
		resolved[name] = value
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	cfg, err := loadTrustantConfig()
	if err != nil {
		return nil, err
	}
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		return nil, err
	}
	stored := foldIntoProductionPool(wsCfg, host, resolved, cfg.Apps)
	if len(stored) == 0 {
		return nil, nil
	}
	if err := saveWorkspaceConfig(wsCfg); err != nil {
		return nil, err
	}
	log.Printf("Shared: stored %d production variables for %s on %s: %v", len(stored), app, host, stored)
	return stored, nil
}

// missingShared is one entry of the publish gate's answer: the variable, and
// the app that would have to be published to supply it.
type missingShared struct {
	Name string `json:"name"`
	App  string `json:"app"`
	Host string `json:"host"`
}

// missingProductionShared returns the app-produced production variables that
// have no value for the target host.
//
// Unlike the development side this BLOCKS the publish: a launch with a missing
// value costs a broken dev server, a publish with one deploys an app pointed at
// nothing. A value the user typed for the app, or one already in that host's
// pool, satisfies it.
func missingProductionShared(appName, host string, cfg *trustantConfig) []missingShared {
	appCfg := cfg.Apps[appName]
	if appCfg == nil || len(appCfg.Production) == 0 {
		return nil
	}
	host = sharedHostKey(host)
	pool := cfg.PredefinedEnvProduction[host]

	names := make([]string, 0, len(appCfg.Production))
	for name := range appCfg.Production {
		names = append(names, name)
	}
	sort.Strings(names)

	var missing []missingShared
	for _, name := range names {
		producer, appProduced := sharedProducerOf(name, cfg.Apps)
		if !appProduced {
			continue
		}
		// An app never blocks on a variable it produces itself: its own publish
		// is what resolves it, in the same request.
		if strings.EqualFold(producer, appName) {
			continue
		}
		if strings.TrimSpace(appCfg.Production[name]) != "" {
			continue
		}
		if strings.TrimSpace(pool[name]) != "" {
			continue
		}
		missing = append(missing, missingShared{Name: name, App: producer, Host: host})
	}
	return missing
}

// commitSharedEnv commits .env.shared, mirroring commitEnvDist: the declaration
// is part of the repo's contract, so it must travel with a clone. Scoped
// pathspec so the user's other dirty files are untouched; never pushes.
func commitSharedEnv(workbenchPath string) {
	if _, err := os.Stat(filepath.Join(workbenchPath, ".git")); err != nil {
		return
	}
	ensureGitIdentity(workbenchPath)

	addCmd := exec.Command("git", "add", "--", sharedEnvFileName)
	addCmd.Dir = workbenchPath
	if output, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("Warning: failed to stage %s: %s (%s)", sharedEnvFileName, err, strings.TrimSpace(string(output)))
		return
	}

	// Nothing staged is a no-op, not an error.
	diffCmd := exec.Command("git", "diff", "--cached", "--quiet", "--", sharedEnvFileName)
	diffCmd.Dir = workbenchPath
	if err := diffCmd.Run(); err == nil {
		return
	}

	commitCmd := exec.Command("git", "commit", "-m", "trustant: update "+sharedEnvFileName, "--", sharedEnvFileName)
	commitCmd.Dir = workbenchPath
	if output, err := commitCmd.CombinedOutput(); err != nil {
		log.Printf("Warning: failed to commit %s: %s (%s)", sharedEnvFileName, err, strings.TrimSpace(string(output)))
	}
}

// --- HTTP ---------------------------------------------------------------

// sharedVarRequest is one row of the picker: the effective name the user chose
// and the ops-config path it points at.
type sharedVarRequest struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleShared dispatches /api/shared and /api/shared/... It is the only
// expiredGuard call site for the feature.
func handleShared(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/shared"), "/")

	if rest == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleSharedList(w, r)
		return
	}

	if strings.HasPrefix(rest, "tree/") {
		app := strings.TrimPrefix(rest, "tree/")
		if !namePattern.MatchString(app) {
			http.Error(w, "Invalid app name", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleSharedTree(w, app)
		return
	}

	app := rest
	if !namePattern.MatchString(app) {
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		handleSharedGet(w, app)
	case http.MethodPost:
		handleSharedSave(w, r, app)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSharedTree logs in as the app and returns its ops config for the picker.
func handleSharedTree(w http.ResponseWriter, app string) {
	unlock := lockRuntimeLifecycle("shared tree " + app)
	defer unlock()

	if err := opsLoginForApp(app, false); err != nil {
		http.Error(w, "Failed to log in as "+app+": "+err.Error(), http.StatusBadGateway)
		return
	}
	tree, err := opsConfigTree()
	if err != nil {
		http.Error(w, "Failed to read ops configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"app":    app,
		"prefix": sharedVarPrefix(app),
		"tree":   tree,
	})
}

// handleSharedGet returns the app's declarations in effective form, so the
// picker can pre-check what is already shared.
func handleSharedGet(w http.ResponseWriter, app string) {
	declared := loadSharedEnv(app)
	vars := make([]sharedVarRequest, 0, len(declared))
	for rawName, rawValue := range declared {
		path, ok := sharedPathOf(rawValue)
		if !ok {
			continue
		}
		vars = append(vars, sharedVarRequest{Name: expandSharedName(rawName, app), Path: path})
	}
	sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"app":    app,
		"prefix": sharedVarPrefix(app),
		"vars":   vars,
	})
}

// handleSharedSave writes the app's whole declaration and resolves it into the
// pool. The request carries the full selection: saving replaces the file, so a
// variable the user unchecked is dropped.
func handleSharedSave(w http.ResponseWriter, r *http.Request, app string) {
	var request struct {
		Vars []sharedVarRequest `json:"vars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(request.Vars) > maxPredefinedEnvVars {
		http.Error(w, fmt.Sprintf("Too many shared variables: %d exceeds the limit of %d", len(request.Vars), maxPredefinedEnvVars), http.StatusBadRequest)
		return
	}

	prefix := sharedVarPrefix(app)
	declared := make(map[string]string)
	for _, v := range request.Vars {
		name := strings.TrimSpace(v.Name)
		path := strings.TrimSpace(v.Path)
		if name == "" || path == "" {
			continue
		}
		if !predefinedEnvNamePattern.MatchString(name) {
			http.Error(w, "Invalid variable name: "+name, http.StatusBadRequest)
			return
		}
		// The refusal is TOTAL — nothing is written when one name is wrong. A
		// partial save is exactly what would put a colliding entry into the flat
		// pool, leaving a consumer reading another app's database while
		// believing it was this one's.
		if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
			http.Error(w, fmt.Sprintf("Shared variable %q must start with %q", name, prefix), http.StatusBadRequest)
			return
		}
		stored := templateSharedName(name, app)
		if _, exists := declared[stored]; exists {
			http.Error(w, "Duplicate variable name: "+name, http.StatusBadRequest)
			return
		}
		declared[stored] = wrapSharedPath(path)
	}

	workbenchPath := filepath.Join(WorkbenchDir, app)
	if _, err := os.Stat(workbenchPath); err != nil {
		http.Error(w, "App "+app+" has no workbench; launch it once before sharing", http.StatusBadRequest)
		return
	}

	unlock := lockRuntimeLifecycle("shared save " + app)
	defer unlock()

	if err := saveSharedEnv(app, declared); err != nil {
		http.Error(w, "Failed to write "+sharedEnvFileName+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	commitSharedEnv(workbenchPath)

	stored, err := refreshSharedForApp(app)
	if err != nil {
		// The declaration is written and committed; only the resolution failed.
		// Report it without losing the edit — the next launch resolves it.
		log.Printf("Warning: failed to resolve shared variables for %s: %s", app, err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"app":      app,
			"declared": len(declared),
			"resolved": 0,
			"warning":  "Saved, but the values could not be resolved: " + err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"app":      app,
		"declared": len(declared),
		"resolved": len(stored),
		"vars":     stored,
	})
}

// sharedListEntry describes one app-produced variable for the Configure page
// and the add-from-shared picker. Values are not included: the pool endpoint
// carries those.
type sharedListEntry struct {
	Name string `json:"name"`
	App  string `json:"app"`
	Path string `json:"path"`
}

// handleSharedList returns every app-produced variable across the workspace.
func handleSharedList(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadTrustantConfig()
	if err != nil {
		http.Error(w, "Failed to read configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}
	entries := make([]sharedListEntry, 0)
	for _, app := range sharedProducingApps(cfg) {
		for rawName, rawValue := range loadSharedEnv(app) {
			path, ok := sharedPathOf(rawValue)
			if !ok {
				continue
			}
			entries = append(entries, sharedListEntry{
				Name: expandSharedName(rawName, app),
				App:  app,
				Path: path,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"vars": entries})
}
