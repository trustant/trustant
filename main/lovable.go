package main

// Lovable project import (issue #246, spec/21-lovable.md).
//
// "Lovable project repo" in Add App clones a Lovable (TanStack Start SSR)
// repository and converts it so it runs as a Trustant app. The conversion is
// split in two:
//
//   - here, at creation time, the deterministic part: detection, inventory,
//     package.json / vite / tsconfig / SPA shell, Lovable-only file removal,
//     ignore files. The result must build, because launch runs `ops ide
//     deploy` and aborts on a failed build.
//   - the agent, on the first launch, runs the embedded conversion notebook
//     (template.md) for everything that needs judgement or platform tools:
//     routing, api.ts, Supabase -> OpenServerless actions, deploy/validation.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed lovable-conversion.md
var lovableConversionNotebook string

// lovableBootstrapNotebook is the apps.<name>.bootstrap value that asks the
// first launch to auto-run template.md (see launch.go).
const lovableBootstrapNotebook = "lovable-conversion"

// lovableReportFile is committed by the Go phase and completed by the agent.
const lovableReportFile = "LOVABLE_CONVERSION.md"

var errNotLovable = errors.New("Not a Lovable project: no Lovable/TanStack Start markers found. Use \"My Application Starter\" instead")

var lovableBranchPattern = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]*$`)

// parseLovableRepo accepts `owner/repo`, `https://github.com/owner/repo`
// (optionally `.git`, a trailing slash or `/tree/<branch>`), `github.com/...`
// and `git@github.com:owner/repo`, each with an optional `@branch` suffix.
func parseLovableRepo(input string) (repo, branch string, err error) {
	s := strings.TrimSpace(input)
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "https://www.github.com/", "github.com/", "git@github.com:"} {
		if strings.HasPrefix(strings.ToLower(s), prefix) {
			s = s[len(prefix):]
			break
		}
	}
	if at := strings.LastIndex(s, "@"); at >= 0 {
		branch = s[at+1:]
		s = s[:at]
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) >= 4 && parts[2] == "tree" && branch == "" {
		branch = strings.Join(parts[3:], "/")
		parts = parts[:2]
	}
	if len(parts) != 2 {
		return "", "", fmt.Errorf("Repository must be owner/repo or a GitHub URL, optionally with @branch")
	}
	repo = parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	if !repoPattern.MatchString(repo) {
		return "", "", fmt.Errorf("Repository must be owner/repo or a GitHub URL, optionally with @branch")
	}
	if branch != "" && (!lovableBranchPattern.MatchString(branch) || strings.Contains(branch, "..") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".lock")) {
		return "", "", fmt.Errorf("Invalid branch name %q", branch)
	}
	return repo, branch, nil
}

// detectLovable reports the Lovable markers found in dir (Phase 0).
func detectLovable(dir string) []string {
	var markers []string
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		for _, m := range []string{"@lovable.dev/vite-tanstack-config", "@tanstack/react-start"} {
			if bytes.Contains(data, []byte(`"`+m+`"`)) {
				markers = append(markers, m)
			}
		}
	}
	for _, p := range []string{"src/routes/__root.tsx", ".lovable"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			markers = append(markers, p)
		}
	}
	return markers
}

// ---- Phase 0: inventory ----------------------------------------------------

type lovableRoute struct {
	File      string            `json:"file"`
	Path      string            `json:"path"`
	Component string            `json:"component,omitempty"`
	Title     string            `json:"title,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

type lovableInventory struct {
	Markers         []string       `json:"markers"`
	Routes          []lovableRoute `json:"routes"`
	Pages           int            `json:"pages"`
	SupabaseFiles   []string       `json:"supabase_files,omitempty"`
	Migrations      []string       `json:"migrations,omitempty"`
	ServerFunctions []string       `json:"server_functions,omitempty"`
	StorageFiles    []string       `json:"storage_files,omitempty"`
	RealtimeFiles   []string       `json:"realtime_files,omitempty"`
	RPCFiles        []string       `json:"rpc_files,omitempty"`
	AuthFiles       []string       `json:"auth_files,omitempty"`
	ReactQuery      bool           `json:"react_query,omitempty"`
	Lockfile        string         `json:"lockfile,omitempty"`
}

var (
	reTitle      = regexp.MustCompile(`title:\s*["'\x60]([^"'\x60]+)["'\x60]`)
	reMetaEntry  = regexp.MustCompile(`\{\s*(?:name|property)\s*:\s*["']([^"']+)["']\s*,\s*content\s*:\s*["'\x60]([^"'\x60]*)["'\x60]\s*\}`)
	reComponent  = regexp.MustCompile(`component\s*:\s*([A-Za-z_][A-Za-z0-9_]*)`)
	reAuthMarker = regexp.MustCompile(`supabase\.auth\.|signInWith|signUp\(|requireSupabaseAuth|attachSupabaseAuth|onAuthStateChange`)
)

// routePathFromFile maps a TanStack flat file route to its URL path.
func routePathFromFile(rel string) string {
	name := strings.TrimSuffix(strings.TrimSuffix(rel, ".tsx"), ".ts")
	name = strings.ReplaceAll(name, ".", "/")
	segments := []string{}
	for _, seg := range strings.Split(name, "/") {
		if seg == "index" || seg == "" || strings.HasPrefix(seg, "_") {
			continue
		}
		segments = append(segments, seg)
	}
	return "/" + strings.Join(segments, "/")
}

func inventoryLovable(dir string) lovableInventory {
	inv := lovableInventory{Markers: detectLovable(dir)}
	routesDir := filepath.Join(dir, "src", "routes")
	filepath.WalkDir(routesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".tsx") || strings.HasSuffix(p, ".ts")) {
			return nil
		}
		rel, _ := filepath.Rel(routesDir, p)
		rel = filepath.ToSlash(rel)
		data, _ := os.ReadFile(p)
		route := lovableRoute{File: "src/routes/" + rel, Meta: map[string]string{}}
		if strings.HasPrefix(filepath.Base(rel), "__root") {
			route.Path = "__root"
		} else {
			route.Path = routePathFromFile(rel)
			inv.Pages++
		}
		if m := reComponent.FindSubmatch(data); m != nil {
			route.Component = string(m[1])
		}
		if m := reTitle.FindSubmatch(data); m != nil {
			route.Title = string(m[1])
		}
		for _, m := range reMetaEntry.FindAllSubmatch(data, -1) {
			route.Meta[string(m[1])] = string(m[2])
		}
		inv.Routes = append(inv.Routes, route)
		return nil
	})

	add := func(list *[]string, rel string) {
		for _, existing := range *list {
			if existing == rel {
				return
			}
		}
		*list = append(*list, rel)
	}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "web", "dist", ".output":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(rel, "supabase/migrations/") && strings.HasSuffix(rel, ".sql") {
			inv.Migrations = append(inv.Migrations, rel)
			return nil
		}
		if !strings.HasPrefix(rel, "src/") || !(strings.HasSuffix(rel, ".ts") || strings.HasSuffix(rel, ".tsx")) {
			return nil
		}
		data, _ := os.ReadFile(p)
		text := string(data)
		if strings.Contains(text, "@supabase/supabase-js") || strings.Contains(rel, "integrations/supabase") || strings.Contains(text, "VITE_SUPABASE_") {
			add(&inv.SupabaseFiles, rel)
		}
		if strings.HasSuffix(rel, ".server.ts") || strings.Contains(text, "createServerFn") {
			add(&inv.ServerFunctions, rel)
		}
		if strings.Contains(text, ".storage.from(") || strings.Contains(text, ".storage\n") {
			add(&inv.StorageFiles, rel)
		}
		if strings.Contains(text, ".channel(") {
			add(&inv.RealtimeFiles, rel)
		}
		if strings.Contains(text, ".rpc(") {
			add(&inv.RPCFiles, rel)
		}
		if reAuthMarker.MatchString(text) {
			add(&inv.AuthFiles, rel)
		}
		if strings.Contains(text, "@tanstack/react-query") && !strings.HasPrefix(rel, "src/routes/__root") && rel != "src/router.tsx" {
			inv.ReactQuery = true
		}
		return nil
	})
	sort.Strings(inv.Migrations)
	for _, lock := range []string{"bun.lock", "bun.lockb", "package-lock.json", "pnpm-lock.yaml", "yarn.lock"} {
		if _, err := os.Stat(filepath.Join(dir, lock)); err == nil {
			inv.Lockfile = lock
			break
		}
	}
	return inv
}

// ---- ordered JSON objects ----------------------------------------------------
// package.json and tsconfig.json keep their key order so the conversion commit
// is a readable diff rather than an alphabetical rewrite.

type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseOrderedObject(data []byte) (*orderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	obj := &orderedObject{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		obj.set(key, raw)
	}
	return obj, nil
}

func (o *orderedObject) set(key string, raw json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
}

func (o *orderedObject) setValue(key string, v interface{}) {
	data, _ := json.Marshal(v)
	o.set(key, data)
}

func (o *orderedObject) delete(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *orderedObject) child(key string) *orderedObject {
	if raw, ok := o.vals[key]; ok {
		if child, err := parseOrderedObject(raw); err == nil {
			return child
		}
	}
	return &orderedObject{vals: map[string]json.RawMessage{}}
}

func (o *orderedObject) setChild(key string, child *orderedObject) {
	o.set(key, child.marshal())
}

func (o *orderedObject) marshal() json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func (o *orderedObject) indented() []byte {
	var out bytes.Buffer
	json.Indent(&out, o.marshal(), "", "  ")
	out.WriteByte('\n')
	return out.Bytes()
}

func (o *orderedObject) stringValue(key string) string {
	var s string
	json.Unmarshal(o.vals[key], &s)
	return s
}

// stripJSONComments removes // and /* */ comments and trailing commas, so a
// JSONC tsconfig parses. String contents are left alone.
func stripJSONComments(data []byte) []byte {
	var out bytes.Buffer
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(data) && data[i+1] == '/' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
			continue
		}
		if c == '/' && i+1 < len(data) && data[i+1] == '*' {
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i++
			continue
		}
		out.WriteByte(c)
	}
	return regexp.MustCompile(`,(\s*[}\]])`).ReplaceAll(out.Bytes(), []byte("$1"))
}

// ---- Phase 1-2, 6: deterministic conversion --------------------------------

// lovableRemovedDeps are dropped from dependencies and devDependencies.
var lovableRemovedDeps = []string{
	"@lovable.dev/vite-tanstack-config", "@tanstack/react-start", "@tanstack/react-router",
	"@tanstack/router-plugin", "@tanstack/router-devtools", "@tanstack/react-router-devtools",
	"@tanstack/router-vite-plugin", "@tanstack/start", "nitro", "vinxi", "vite-tsconfig-paths",
	"vitest", "jsdom", "happy-dom", "@cloudflare/vite-plugin", "wrangler", "lovable-tagger",
}

var lovableRemovedDepPrefixes = []string{"@testing-library/", "@vitest/"}

// lovableRequiredDeps must exist for the converted SPA to build.
var lovableRequiredDeps = map[string]string{
	"react": "dependencies", "react-dom": "dependencies",
	"vite": "devDependencies", "@vitejs/plugin-react": "devDependencies",
	"@tailwindcss/vite": "devDependencies", "tailwindcss": "devDependencies",
	"typescript": "devDependencies", "@types/node": "devDependencies",
}

// lovableDeletedPaths are SSR/Lovable-only artifacts removed in Phase 1/3/4.
// src/routes, src/router.tsx and src/routeTree.gen.ts stay: the agent reads
// them while rewriting routing and deletes them itself.
var lovableDeletedPaths = []string{
	"bun.lock", "bun.lockb", "bunfig.toml", "package-lock.json", ".lovable",
	"vitest.config.ts", "vitest.config.mts", "vite.config.js", "vite.config.mts",
	"wrangler.toml", "wrangler.jsonc", "wrangler.json",
	"src/server.ts", "src/start.ts", "src/test",
	"src/lib/lovable-error-reporting.ts", "src/lib/error-capture.ts", "src/lib/error-page.ts",
}

const lovableViteConfig = `import { defineConfig, type ProxyOptions } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { env } from "node:process";
import path from "node:path";

// The managed watcher injects the OpenServerless API host.
const apiTarget = env["OPS_HOST"] || env["OPSDEV_HOST"] || "";
const proxies: Record<string, ProxyOptions> = apiTarget
  ? { "/api/my": { target: apiTarget, changeOrigin: true } }
  : {};

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: "web" },
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "./src") } },
  server: {
    host: "0.0.0.0",
    port: 5173,
    strictPort: true, // never silently drift to 5174 behind the ingress
    allowedHosts: true,
    proxy: proxies,
  },
});
`

const lovableMainTsx = `import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "@/styles.css";
import App from "@/App";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
`

// lovablePlaceholderApp keeps the app buildable between creation and the
// agent's routing rewrite (Phase 3 replaces it). Launch runs a real build.
const lovablePlaceholderApp = `// Placeholder written by the Trustant Lovable import. The conversion notebook
// (template.md, step 1) replaces it with the converted pages and routing.
export default function App() {
  return (
    <main className="min-h-screen flex items-center justify-center p-8 text-center">
      <div>
        <h1 className="text-2xl font-semibold">Converting your Lovable project…</h1>
        <p className="mt-2 opacity-70">
          The Trustant agent is rewriting routing and the backend. This page is replaced when it finishes.
        </p>
      </div>
    </main>
  );
}
`

// npmViewVersion resolves `pkg@range` against the configured registry and
// returns the highest matching version. Variable so tests can stub it.
var npmViewVersion = func(spec string) (string, error) {
	cmd := exec.Command("npm", "view", spec, "version", "--json")
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.Output(); close(done) }()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		<-done
		return "", fmt.Errorf("npm view %s timed out", spec)
	}
	if err != nil {
		return "", err
	}
	var single string
	if json.Unmarshal(out, &single) == nil && single != "" {
		return single, nil
	}
	var many []string
	if json.Unmarshal(out, &many) == nil && len(many) > 0 {
		return many[len(many)-1], nil
	}
	return "", fmt.Errorf("no version for %s", spec)
}

var npmAvailable = func() bool {
	_, err := exec.LookPath("npm")
	return err == nil
}

type lovableReport struct {
	Applied []string
	Flagged []string
}

func (r *lovableReport) apply(format string, a ...interface{}) {
	r.Applied = append(r.Applied, fmt.Sprintf(format, a...))
}

func (r *lovableReport) flag(format string, a ...interface{}) {
	r.Flagged = append(r.Flagged, fmt.Sprintf(format, a...))
}

func isRemovedLovableDep(name string) bool {
	for _, d := range lovableRemovedDeps {
		if d == name {
			return true
		}
	}
	for _, p := range lovableRemovedDepPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func convertPackageJSON(dir, appName string, inv lovableInventory, report *lovableReport) error {
	path := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read package.json: %w", err)
	}
	pkg, err := parseOrderedObject(data)
	if err != nil {
		return fmt.Errorf("parse package.json: %w", err)
	}
	pkg.setValue("name", strings.ToLower(appName))

	scripts := pkg.child("scripts")
	kept := &orderedObject{vals: map[string]json.RawMessage{}}
	kept.setValue("dev", "vite")
	kept.setValue("build", "vite build")
	kept.setValue("preview", "vite preview")
	kept.setValue("typecheck", "tsc --noEmit")
	for _, k := range []string{"lint", "format"} {
		if raw, ok := scripts.vals[k]; ok {
			kept.set(k, raw)
		}
	}
	pkg.setChild("scripts", kept)
	// This block is what makes the app visible from the workspace: without it
	// the watcher serves a static build bound to 127.0.0.1 only.
	pkg.setValue("openserverless", map[string]string{"devel": "npm run dev", "deploy": "npm run build"})
	report.apply("package.json: renamed to %q, scripts dev/build/preview/typecheck, added the openserverless devel/deploy block", strings.ToLower(appName))

	removed := []string{}
	sections := map[string]*orderedObject{}
	for _, section := range []string{"dependencies", "devDependencies"} {
		deps := pkg.child(section)
		for _, name := range append([]string(nil), deps.keys...) {
			if isRemovedLovableDep(name) {
				deps.delete(name)
				removed = append(removed, name)
			}
		}
		sections[section] = deps
	}
	has := func(name string) bool {
		_, a := sections["dependencies"].vals[name]
		_, b := sections["devDependencies"].vals[name]
		return a || b
	}
	added := []string{}
	required := make([]string, 0, len(lovableRequiredDeps))
	for name := range lovableRequiredDeps {
		required = append(required, name)
	}
	sort.Strings(required)
	for _, name := range required {
		if !has(name) {
			sections[lovableRequiredDeps[name]].setValue(name, "latest")
			added = append(added, name)
		}
	}
	if inv.Pages > 1 && !has("react-router-dom") {
		sections["dependencies"].setValue("react-router-dom", "latest")
		added = append(added, "react-router-dom")
	}
	if len(removed) > 0 {
		report.apply("package.json: removed %s", strings.Join(removed, ", "))
	}
	if len(added) > 0 {
		report.apply("package.json: added %s", strings.Join(added, ", "))
	}
	if inv.ReactQuery {
		report.flag("@tanstack/react-query is kept because pages use it; drop it if the converted pages no longer do")
	}
	if has("@supabase/supabase-js") {
		report.flag("@supabase/supabase-js is kept until the agent replaces every Supabase call site (notebook step 2), then it must be removed")
	}

	registryPass(sections, report)
	for _, section := range []string{"dependencies", "devDependencies"} {
		pkg.setChild(section, sections[section])
	}
	return os.WriteFile(path, pkg.indented(), 0644)
}

// registryPass (Phase 1.2) replaces ranges the workspace registry cannot
// resolve — Lovable pins are masked by bun.lock — with the latest version.
func registryPass(sections map[string]*orderedObject, report *lovableReport) {
	if !npmAvailable() {
		report.flag("npm is not available: the registry compatibility pass was skipped; if `npm install` fails with \"No matching version\", pin the latest available version")
		return
	}
	type job struct {
		section, name, spec string
	}
	var jobs []job
	for _, section := range []string{"dependencies", "devDependencies"} {
		deps := sections[section]
		for _, name := range deps.keys {
			spec := deps.stringValue(name)
			if spec == "" || strings.Contains(spec, ":") || strings.HasPrefix(spec, "file") {
				continue // workspace:, npm:, git:, file: specs are left alone
			}
			jobs = append(jobs, job{section, name, spec})
		}
	}
	var mu sync.Mutex
	pinned := []string{}
	var unresolved []string
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if j.spec != "latest" {
				if _, err := npmViewVersion(j.name + "@" + j.spec); err == nil {
					return
				}
			}
			latest, err := npmViewVersion(j.name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				unresolved = append(unresolved, j.name)
				return
			}
			sections[j.section].setValue(j.name, "^"+latest)
			if j.spec != "latest" {
				pinned = append(pinned, fmt.Sprintf("%s %s -> ^%s", j.name, j.spec, latest))
			}
		}(j)
	}
	wg.Wait()
	sort.Strings(pinned)
	sort.Strings(unresolved)
	if len(pinned) > 0 {
		report.apply("registry pass: pinned unresolvable ranges to the latest available version: %s", strings.Join(pinned, "; "))
	} else {
		report.apply("registry pass: every dependency range resolves against the workspace registry")
	}
	if len(unresolved) > 0 {
		report.flag("registry pass could not resolve %s; `npm install` may fail until they are fixed", strings.Join(unresolved, ", "))
	}
}

func convertTsconfig(dir string, report *lovableReport) {
	path := filepath.Join(dir, "tsconfig.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	cfg, err := parseOrderedObject(stripJSONComments(data))
	if err != nil {
		report.flag("tsconfig.json could not be parsed (%s): set compilerOptions.types to [\"vite/client\", \"node\"] and drop vitest entries by hand", err)
		return
	}
	opts := cfg.child("compilerOptions")
	opts.setValue("types", []string{"vite/client", "node"})
	cfg.setChild("compilerOptions", opts)
	if raw, ok := cfg.vals["include"]; ok {
		var include []string
		if json.Unmarshal(raw, &include) == nil {
			filtered := include[:0]
			for _, entry := range include {
				if !strings.Contains(entry, "vitest") {
					filtered = append(filtered, entry)
				}
			}
			cfg.setValue("include", filtered)
		}
	}
	if err := os.WriteFile(path, cfg.indented(), 0644); err == nil {
		report.apply("tsconfig.json: types [vite/client, node], vitest entries dropped")
	}
}

func convertEslint(dir string, report *lovableReport) {
	path := filepath.Join(dir, "eslint.config.js")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(data)
	re := regexp.MustCompile(`ignores:\s*\[`)
	if re.MatchString(text) && !strings.Contains(text, `"web"`) {
		text = re.ReplaceAllString(text, `ignores: ["web", `)
		os.WriteFile(path, []byte(text), 0644)
		report.apply("eslint.config.js: web/ ignored")
	}
	if strings.Contains(text, "@tanstack") || strings.Contains(text, "server-only") {
		report.flag("eslint.config.js still has TanStack-specific rules (e.g. the server-only restriction): remove them")
	}
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// writeIndexHTML builds the SPA shell, merging head() metadata: the index
// page wins over the root, and og:image is never inherited from the root.
func writeIndexHTML(dir string, inv lovableInventory, report *lovableReport) error {
	var root, index *lovableRoute
	for i := range inv.Routes {
		switch inv.Routes[i].Path {
		case "__root":
			root = &inv.Routes[i]
		case "/":
			index = &inv.Routes[i]
		}
	}
	title := ""
	meta := map[string]string{}
	if root != nil {
		title = root.Title
		for k, v := range root.Meta {
			if k == "og:image" || k == "twitter:image" {
				continue
			}
			meta[k] = v
		}
	}
	if index != nil {
		if index.Title != "" {
			title = index.Title
		}
		for k, v := range index.Meta {
			meta[k] = v
		}
	}
	if title == "" {
		title = "App"
	}
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n  <head>\n")
	b.WriteString("    <meta charset=\"UTF-8\" />\n    <meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\" />\n")
	b.WriteString("    <title>" + htmlEscape(title) + "</title>\n")
	for _, fav := range []string{"favicon.svg", "favicon.ico", "favicon.png"} {
		if _, err := os.Stat(filepath.Join(dir, "public", fav)); err == nil {
			b.WriteString("    <link rel=\"icon\" href=\"/" + fav + "\" />\n")
			break
		}
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		attr := "name"
		if strings.HasPrefix(k, "og:") {
			attr = "property"
		}
		b.WriteString(fmt.Sprintf("    <meta %s=\"%s\" content=\"%s\" />\n", attr, htmlEscape(k), htmlEscape(meta[k])))
	}
	b.WriteString("  </head>\n  <body>\n    <div id=\"root\"></div>\n    <script type=\"module\" src=\"/src/main.tsx\"></script>\n  </body>\n</html>\n")
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(b.String()), 0644); err != nil {
		return err
	}
	report.apply("index.html: SPA shell with title %q and %d meta tags merged from route head() definitions", title, len(keys))
	return nil
}

func appendIgnoreEntries(path string, entries []string) bool {
	data, _ := os.ReadFile(path)
	existing := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		existing[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, e := range entries {
		if !existing[e] {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return false
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += strings.Join(missing, "\n") + "\n"
	return os.WriteFile(path, []byte(text), 0644) == nil
}

var reLovableBlock = regexp.MustCompile(`(?s)[^\n]*LOVABLE:BEGIN.*?LOVABLE:END[^\n]*\n?`)

// convertLovableProject runs the deterministic phases on a working tree.
func convertLovableProject(dir, appName string) (lovableInventory, *lovableReport, error) {
	report := &lovableReport{}
	inv := inventoryLovable(dir)
	if len(inv.Markers) == 0 {
		return inv, report, errNotLovable
	}
	if err := convertPackageJSON(dir, appName, inv, report); err != nil {
		return inv, report, err
	}
	if err := os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte(lovableViteConfig), 0644); err != nil {
		return inv, report, err
	}
	report.apply("vite.config.ts: plain Vite, outDir web/, 0.0.0.0:5173 strictPort, /api/my proxy to OPS_HOST")
	convertTsconfig(dir, report)
	convertEslint(dir, report)
	if err := writeIndexHTML(dir, inv, report); err != nil {
		return inv, report, err
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.tsx"), []byte(lovableMainTsx), 0644); err != nil {
		return inv, report, err
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "App.tsx"), []byte(lovablePlaceholderApp), 0644); err != nil {
		return inv, report, err
	}
	report.apply("src/main.tsx and a placeholder src/App.tsx (replaced by the agent's routing step)")
	if _, err := os.Stat(filepath.Join(dir, "src", "styles.css")); err != nil {
		report.flag("src/styles.css not found: src/main.tsx imports it; point it at the project's global stylesheet")
	}

	var deleted []string
	for _, rel := range lovableDeletedPaths {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err == nil {
			if os.RemoveAll(p) == nil {
				deleted = append(deleted, rel)
			}
		}
	}
	if len(deleted) > 0 {
		report.apply("removed SSR/Lovable-only files: %s", strings.Join(deleted, ", "))
	}

	if appendIgnoreEntries(filepath.Join(dir, ".gitignore"), []string{"node_modules", "packages/**/*.zip", "__pycache__/", "*.pyc", ".ops/"}) {
		report.apply(".gitignore: watcher artifacts, Python caches and .ops/ ignored")
	}
	if appendIgnoreEntries(filepath.Join(dir, ".prettierignore"), []string{"web/"}) {
		report.apply(".prettierignore: web/ ignored")
	}
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", "README.md"} {
		p := filepath.Join(dir, rel)
		if info, err := os.Lstat(p); err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		data, _ := os.ReadFile(p)
		if cleaned := reLovableBlock.ReplaceAll(data, nil); !bytes.Equal(cleaned, data) {
			os.WriteFile(p, cleaned, 0644)
			report.apply("%s: Lovable app-local block removed", rel)
		}
	}
	os.MkdirAll(filepath.Join(dir, "packages"), 0755)

	if len(inv.SupabaseFiles)+len(inv.Migrations)+len(inv.ServerFunctions) > 0 {
		report.flag("database/backend conversion pending: %d Supabase file(s), %d migration(s), %d server function file(s) — notebook steps 3-4", len(inv.SupabaseFiles), len(inv.Migrations), len(inv.ServerFunctions))
	}
	if len(inv.AuthFiles) > 0 {
		report.flag("auth detected in %s — notebook step 4 converts it to Redis sessions", strings.Join(inv.AuthFiles, ", "))
	}

	if err := os.WriteFile(filepath.Join(dir, "template.md"), []byte(lovableTemplateDocument()), 0644); err != nil {
		return inv, report, err
	}
	report.apply("template.md: the Lovable conversion notebook, run automatically on the first launch")
	if err := os.WriteFile(filepath.Join(dir, lovableReportFile), []byte(renderLovableReport(appName, inv, report)), 0644); err != nil {
		return inv, report, err
	}
	return inv, report, nil
}

// lovableTemplateDocument is template.md in the TruACP working-copy format.
func lovableTemplateDocument() string {
	return "---\nname: " + lovableBootstrapNotebook + "\nrepo: \nfile: \nedited: false\n---\n\n" + strings.TrimLeft(lovableConversionNotebook, "\n")
}

func renderLovableReport(appName string, inv lovableInventory, report *lovableReport) string {
	var b strings.Builder
	b.WriteString("# Lovable conversion report — " + appName + "\n\n")
	b.WriteString("Phases 0–2 and the deterministic part of phase 6 were applied by Trustant when the app was imported. ")
	b.WriteString("The conversion notebook (`template.md`) completes phases 3–7 and appends its results below.\n\n")
	b.WriteString("## Phase 0 — Inventory\n\n```json\n")
	invJSON, _ := json.MarshalIndent(inv, "", "  ")
	b.Write(invJSON)
	b.WriteString("\n```\n\n## Applied by Trustant\n\n")
	for _, a := range report.Applied {
		b.WriteString("- " + a + "\n")
	}
	b.WriteString("\n## Flagged for attention\n\n")
	if len(report.Flagged) == 0 {
		b.WriteString("- none\n")
	}
	for _, f := range report.Flagged {
		b.WriteString("- " + f + "\n")
	}
	b.WriteString("\n## Agent phases (3–7)\n\n_Pending: filled in by the conversion notebook._\n")
	return b.String()
}

// importLovableIntoWorkspace checks out branch from the freshly cloned bare
// repository, converts it, commits, and pushes the commit back into the bare
// repository. GitHub is not touched: the user's next push publishes it as an
// ordinary commit, never a force-push (Lovable syncs the branch).
func importLovableIntoWorkspace(bareRepo, branch, appName string) (*lovableReport, error) {
	tmp, err := os.MkdirTemp("", "lovable-import-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	work := filepath.Join(tmp, appName)
	if out, err := exec.Command("git", "clone", "--branch", branch, bareRepo, work).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("checkout %s: %s", branch, strings.TrimSpace(string(out)))
	}
	_, report, err := convertLovableProject(work, appName)
	if err != nil {
		return report, err
	}
	if err := ensureGitIdentity(work); err != nil {
		return report, err
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-m", "Convert Lovable project for Trustant (phases 0-2)\n\nSee " + lovableReportFile + "; template.md completes the conversion on first launch."},
		{"push", "origin", "HEAD:refs/heads/" + branch},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			return report, fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
		}
	}
	return report, nil
}

// bareDefaultBranch returns the branch HEAD points to in a bare repository.
func bareDefaultBranch(bareRepo string) string {
	out, err := exec.Command("git", "-C", bareRepo, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(string(out))
}

func bareHasBranch(bareRepo, branch string) bool {
	return exec.Command("git", "-C", bareRepo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}
