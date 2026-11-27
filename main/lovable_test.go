package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLovableRepo(t *testing.T) {
	cases := []struct {
		in, repo, branch string
		bad              bool
	}{
		{in: "user/my-app", repo: "user/my-app"},
		{in: "user/my-app@dev", repo: "user/my-app", branch: "dev"},
		{in: "https://github.com/user/my-app", repo: "user/my-app"},
		{in: "https://github.com/user/my-app.git", repo: "user/my-app"},
		{in: "https://github.com/user/my-app/", repo: "user/my-app"},
		{in: "https://github.com/user/my-app@feature/x", repo: "user/my-app", branch: "feature/x"},
		{in: "https://github.com/user/my-app/tree/release", repo: "user/my-app", branch: "release"},
		{in: "git@github.com:user/my-app.git", repo: "user/my-app"},
		{in: "  github.com/user/my-app  ", repo: "user/my-app"},
		{in: "my-app", bad: true},
		{in: "https://gitlab.com/user/my-app", bad: true},
		{in: "user/my-app@-x", bad: true},
		{in: "user/my-app@a..b", bad: true},
		{in: "user/my app", bad: true},
	}
	for _, c := range cases {
		repo, branch, err := parseLovableRepo(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%q: expected error, got %q %q", c.in, repo, branch)
			}
			continue
		}
		if err != nil || repo != c.repo || branch != c.branch {
			t.Errorf("%q: got (%q, %q, %v), want (%q, %q)", c.in, repo, branch, err, c.repo, c.branch)
		}
	}
}

func TestRoutePathFromFile(t *testing.T) {
	for in, want := range map[string]string{
		"index.tsx":          "/",
		"history.tsx":        "/history",
		"posts.$id.tsx":      "/posts/$id",
		"settings/index.tsx": "/settings",
		"_layout.about.tsx":  "/about",
	} {
		if got := routePathFromFile(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func writeFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func lovableFixture() map[string]string {
	return map[string]string{
		"package.json": `{
  "name": "pomodoro-lovable",
  "private": true,
  "type": "module",
  "scripts": {"dev": "vite dev", "build": "vite build", "test": "vitest", "lint": "eslint ."},
  "dependencies": {
    "@tanstack/react-router": "^1.0.0",
    "@tanstack/react-start": "^1.0.0",
    "@supabase/supabase-js": "^2.0.0",
    "react": "^19.0.0",
    "react-dom": "^19.0.0",
    "lucide-react": "^0.400.0"
  },
  "devDependencies": {
    "@lovable.dev/vite-tanstack-config": "^1.0.0",
    "@testing-library/react": "^16.0.0",
    "vitest": "^3.0.0",
    "vite": "^7.0.0",
    "tailwindcss": "^4.0.0",
    "@tailwindcss/vite": "^4.0.0",
    "nitro": "^3.0.0"
  }
}
`,
		"tsconfig.json": `{
  // Lovable ships JSONC
  "compilerOptions": {
    "target": "ES2022", /* bundler */
    "types": ["vitest/globals"],
    "paths": {"@/*": ["./src/*"]},
  },
  "include": ["src", "vitest.config.ts"]
}
`,
		"eslint.config.js":         "export default [{ ignores: [\"dist\"] }];\n",
		"bun.lock":                 "lock",
		".lovable/project.json":    "{}",
		"vitest.config.ts":         "export default {}",
		"src/server.ts":            "export {}",
		"src/start.ts":             "export {}",
		"src/styles.css":           "@import \"tailwindcss\";\n",
		"src/lib/error-capture.ts": "export {}",
		"src/routes/__root.tsx": `export const Route = createRootRoute({
  head: () => ({ meta: [{ title: "Lovable App" }, { name: "description", content: "Root desc" }, { property: "og:image", content: "https://lovable.dev/og.png" }] }),
});`,
		"src/routes/index.tsx": `export const Route = createFileRoute("/")({
  component: Timer,
  head: () => ({ meta: [{ title: "Pomodoro Timer" }, { property: "og:title", content: "Pomodoro" }] }),
});`,
		"src/routes/history.tsx":              `export const Route = createFileRoute("/history")({ component: History });`,
		"src/integrations/supabase/client.ts": `import { createClient } from "@supabase/supabase-js";`,
		"supabase/migrations/001_init.sql":    "create table sessions (id uuid primary key);",
		"public/favicon.ico":                  "x",
		"AGENTS.md":                           "# Notes\n<!-- LOVABLE:BEGIN -->\nlovable stuff\n<!-- LOVABLE:END -->\nkeep me\n",
	}
}

func stubNpm(t *testing.T) {
	t.Helper()
	oldView, oldAvail := npmViewVersion, npmAvailable
	npmAvailable = func() bool { return true }
	npmViewVersion = func(spec string) (string, error) {
		if spec == "lucide-react@^0.400.0" {
			return "", errors.New("No matching version")
		}
		if !strings.Contains(spec[1:], "@") {
			return "9.9.9", nil
		}
		return "1.0.0", nil
	}
	t.Cleanup(func() { npmViewVersion, npmAvailable = oldView, oldAvail })
}

func TestDetectLovableRejectsPlainRepo(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{"package.json": `{"name":"x","dependencies":{"react":"^19"}}`})
	if _, _, err := convertLovableProject(dir, "myapp1"); !errors.Is(err, errNotLovable) {
		t.Fatalf("expected errNotLovable, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "template.md")); err == nil {
		t.Fatal("a rejected repo must not be modified")
	}
}

func TestConvertLovableProject(t *testing.T) {
	stubNpm(t)
	dir := t.TempDir()
	writeFixture(t, dir, lovableFixture())

	inv, report, err := convertLovableProject(dir, "Pomodoro1")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Pages != 2 || len(inv.Migrations) != 1 || len(inv.SupabaseFiles) == 0 || inv.Lockfile != "bun.lock" {
		t.Fatalf("unexpected inventory: %+v", inv)
	}

	var pkg map[string]interface{}
	data, _ := os.ReadFile(filepath.Join(dir, "package.json"))
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg["name"] != "pomodoro1" {
		t.Errorf("name = %v", pkg["name"])
	}
	if !strings.HasPrefix(string(data), "{\n  \"name\"") {
		t.Errorf("key order not preserved:\n%s", data)
	}
	ops := pkg["openserverless"].(map[string]interface{})
	if ops["devel"] != "npm run dev" || ops["deploy"] != "npm run build" {
		t.Errorf("openserverless = %v", ops)
	}
	scripts := pkg["scripts"].(map[string]interface{})
	if scripts["dev"] != "vite" || scripts["test"] != nil || scripts["lint"] != "eslint ." {
		t.Errorf("scripts = %v", scripts)
	}
	deps := pkg["dependencies"].(map[string]interface{})
	dev := pkg["devDependencies"].(map[string]interface{})
	for _, gone := range []string{"@tanstack/react-router", "@tanstack/react-start"} {
		if deps[gone] != nil {
			t.Errorf("%s not removed", gone)
		}
	}
	for _, gone := range []string{"@lovable.dev/vite-tanstack-config", "@testing-library/react", "vitest", "nitro"} {
		if dev[gone] != nil {
			t.Errorf("%s not removed", gone)
		}
	}
	if deps["react-router-dom"] != "^9.9.9" || dev["@vitejs/plugin-react"] != "^9.9.9" {
		t.Errorf("required deps not added and pinned: %v %v", deps["react-router-dom"], dev["@vitejs/plugin-react"])
	}
	if deps["lucide-react"] != "^9.9.9" {
		t.Errorf("unresolvable range not pinned: %v", deps["lucide-react"])
	}
	if deps["react"] != "^19.0.0" {
		t.Errorf("resolvable range changed: %v", deps["react"])
	}

	vite, _ := os.ReadFile(filepath.Join(dir, "vite.config.ts"))
	for _, want := range []string{`host: "0.0.0.0"`, "strictPort: true", `outDir: "web"`, "allowedHosts: true"} {
		if !strings.Contains(string(vite), want) {
			t.Errorf("vite.config.ts lacks %s", want)
		}
	}

	ts, _ := os.ReadFile(filepath.Join(dir, "tsconfig.json"))
	if !strings.Contains(string(ts), `"vite/client"`) || strings.Contains(string(ts), "vitest") || !strings.Contains(string(ts), `"paths"`) {
		t.Errorf("tsconfig.json:\n%s", ts)
	}

	html, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	h := string(html)
	if !strings.Contains(h, "<title>Pomodoro Timer</title>") {
		t.Errorf("page title must win over root:\n%s", h)
	}
	if strings.Contains(h, "og:image") {
		t.Errorf("og:image must not be inherited from root:\n%s", h)
	}
	if !strings.Contains(h, `content="Root desc"`) || !strings.Contains(h, `property="og:title"`) || !strings.Contains(h, `/favicon.ico`) || !strings.Contains(h, `src="/src/main.tsx"`) {
		t.Errorf("index.html missing merged meta/favicon/entry:\n%s", h)
	}

	for _, gone := range []string{"bun.lock", ".lovable", "vitest.config.ts", "src/server.ts", "src/start.ts", "src/lib/error-capture.ts"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s not deleted", gone)
		}
	}
	for _, kept := range []string{"src/routes/index.tsx", "src/main.tsx", "src/App.tsx", "src/styles.css"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s missing", kept)
		}
	}

	agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if strings.Contains(string(agents), "LOVABLE") || !strings.Contains(string(agents), "keep me") {
		t.Errorf("AGENTS.md:\n%s", agents)
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), "packages/**/*.zip") {
		t.Errorf(".gitignore:\n%s", gi)
	}
	eslint, _ := os.ReadFile(filepath.Join(dir, "eslint.config.js"))
	if !strings.Contains(string(eslint), `ignores: ["web", "dist"]`) {
		t.Errorf("eslint.config.js:\n%s", eslint)
	}

	tpl, _ := os.ReadFile(filepath.Join(dir, "template.md"))
	if !strings.HasPrefix(string(tpl), "---\nname: lovable-conversion\n") {
		t.Errorf("template.md front matter:\n%.120s", tpl)
	}
	if n := strings.Count("\n"+lovableConversionNotebook, "\n---\n"); n != 5 {
		t.Errorf("notebook must have 6 steps (5 separators), got %d", n)
	}
	rep, _ := os.ReadFile(filepath.Join(dir, lovableReportFile))
	if !strings.Contains(string(rep), "## Phase 0") || !strings.Contains(string(rep), "lucide-react ^0.400.0 -> ^9.9.9") {
		t.Errorf("report:\n%s", rep)
	}
	if len(report.Flagged) == 0 {
		t.Error("supabase usage must be flagged")
	}
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return strings.TrimSpace(string(out))
}

func bareFromFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(src, 0755)
	writeFixture(t, src, files)
	gitT(t, src, "init", "-q", "-b", "main")
	gitT(t, src, "add", "-A")
	gitT(t, src, "commit", "-q", "-m", "lovable")
	bare := filepath.Join(root, "bare")
	gitT(t, root, "clone", "-q", "--bare", src, bare)
	return bare
}

func TestImportLovableIntoWorkspace(t *testing.T) {
	stubNpm(t)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	bare := bareFromFixture(t, lovableFixture())
	if bareDefaultBranch(bare) != "main" || !bareHasBranch(bare, "main") || bareHasBranch(bare, "nope") {
		t.Fatal("branch helpers")
	}
	if _, err := importLovableIntoWorkspace(bare, "main", "pomodoro1"); err != nil {
		t.Fatal(err)
	}
	if n := gitT(t, bare, "rev-list", "--count", "main"); n != "2" {
		t.Fatalf("expected the conversion commit on top, got %s commits", n)
	}
	files := gitT(t, bare, "ls-tree", "-r", "--name-only", "main")
	for _, want := range []string{"template.md", lovableReportFile, "index.html", "src/main.tsx"} {
		if !strings.Contains(files, want) {
			t.Errorf("%s not committed", want)
		}
	}
	if strings.Contains(files, "bun.lock") {
		t.Error("bun.lock still committed")
	}
}

func TestImportRejectsNonLovable(t *testing.T) {
	bare := bareFromFixture(t, map[string]string{"package.json": `{"name":"x"}`})
	_, err := importLovableIntoWorkspace(bare, "main", "plainapp")
	if !errors.Is(err, errNotLovable) {
		t.Fatalf("expected errNotLovable, got %v", err)
	}
	if n := gitT(t, bare, "rev-list", "--count", "main"); n != "1" {
		t.Fatal("rejected import must not commit")
	}
}
