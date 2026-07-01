package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func isolateOpenServerlessCheckerInstall(t *testing.T) string {
	t.Helper()
	origCheckerPath := openServerlessCheckerInstallPathOverride
	checkerInstallPath := filepath.Join(t.TempDir(), "bin", "check_openserverless_actions.sh")
	openServerlessCheckerInstallPathOverride = checkerInstallPath
	t.Cleanup(func() { openServerlessCheckerInstallPathOverride = origCheckerPath })
	return checkerInstallPath
}

func TestManagedOllamaDetectionRequiresGeneratedModelMarker(t *testing.T) {
	customOllama := map[string]interface{}{
		"options": map[string]interface{}{
			"baseURL": "http://localhost:11434/v1",
		},
		"models": map[string]interface{}{
			"gemma4:latest": map[string]interface{}{
				"name": "Gemma4",
			},
		},
	}
	if isTrustableManagedOpenCodeProvider("ollama", customOllama) {
		t.Fatal("custom ollama provider without generated marker should be preserved")
	}

	generatedOllama := map[string]interface{}{
		"options": map[string]interface{}{
			"baseURL": "http://localhost:11434/v1",
		},
		"models": map[string]interface{}{
			"qwen3.5:cloud": map[string]interface{}{
				"variants": map[string]interface{}{
					"disabled_variant": map[string]interface{}{
						"disabled": true,
					},
				},
			},
		},
	}
	if !isTrustableManagedOpenCodeProvider("ollama", generatedOllama) {
		t.Fatal("generated ollama provider should be removed")
	}
}

func TestDisabledProvidersForCustomConfigKeepsCustomProviderSelectable(t *testing.T) {
	filtered := disabledProvidersForCustomConfig(
		[]string{"openai", "ollama", "ollama2", "opencode"},
		map[string]interface{}{
			"openai":  map[string]interface{}{},
			"ollama2": map[string]interface{}{},
		},
	)

	for _, disabled := range filtered {
		if disabled == "openai" || disabled == "ollama2" {
			t.Fatalf("custom provider %q should not remain disabled: %#v", disabled, filtered)
		}
	}
	if len(filtered) != 2 || filtered[0] != "ollama" || filtered[1] != "opencode" {
		t.Fatalf("unexpected disabled providers after filtering: %#v", filtered)
	}
}

func TestDefaultOpenCodeLSPConfigIncludesPython(t *testing.T) {
	lsp := defaultOpenCodeLSPConfig()
	python, ok := lsp["python"].(map[string]interface{})
	if !ok {
		t.Fatalf("python LSP config missing: %#v", lsp)
	}
	command, ok := python["command"].([]string)
	if !ok || len(command) != 1 || command[0] != "pylsp" {
		t.Fatalf("unexpected python LSP command: %#v", python["command"])
	}
}

func TestBuildLaunchMCPFromOpsConfig(t *testing.T) {
	var cfg opsConfig
	cfg.S3.Host = "seaweedfs"
	cfg.S3.Port = 9000
	cfg.S3.Bucket.Data = "app-data"
	cfg.S3.Bucket.Static = "app-web"
	cfg.S3.Access.Key = "key"
	cfg.S3.Secret.Key = "secret"
	cfg.Postgres.Database = "appdb"
	cfg.Postgres.URL = "postgresql://app:pw@pg:5432/appdb"
	cfg.Redis.URL = "redis://app:pw@redis:6379"
	cfg.Redis.Port = 6379
	cfg.Redis.Password = "pw"
	cfg.Redis.Service = "redis"
	cfg.Redis.Prefix = "app:"
	cfg.Milvus.Host = "milvus"
	cfg.Milvus.Port = 19530
	cfg.Milvus.Token = "app:tok"
	cfg.Milvus.DB.Name = "appdb"

	mcp := buildMCPFromOpsConfig(&cfg)

	s3 := mcp["s3"].(map[string]interface{})
	if cmd := s3["command"].([]string); len(cmd) != 1 || cmd[0] != "mcp-s3" {
		t.Fatalf("unexpected S3 command: %#v", s3["command"])
	}
	s3Env := s3["environment"].(map[string]string)
	if s3Env["S3_ENDPOINT"] != "http://seaweedfs:9000" {
		t.Fatalf("unexpected S3_ENDPOINT: %#v", s3Env)
	}
	if s3Env["S3_USE_PATH_STYLE"] != "true" {
		t.Fatalf("unexpected S3_USE_PATH_STYLE: %#v", s3Env)
	}

	postgres := mcp["postgres"].(map[string]interface{})
	if cmd := postgres["command"].([]string); len(cmd) != 2 || cmd[0] != "postgres-mcp" || cmd[1] != "--access-mode=unrestricted" {
		t.Fatalf("unexpected postgres command: %#v", postgres["command"])
	}
	postgresEnv := postgres["environment"].(map[string]string)
	if postgresEnv["DATABASE_URI"] != "postgresql://app:pw@pg:5432/appdb" {
		t.Fatalf("unexpected postgres DATABASE_URI: %#v", postgresEnv)
	}

	redis := mcp["redis"].(map[string]interface{})
	// Per spec/4-launch.md the redis command has no --ssl/--cluster-mode flags,
	// and the environment block carries exactly REDIS_USERNAME/HOST/PORT/PWD.
	wantRedisCmd := []string{
		"redis-mcp-server",
		"--host", "redis",
		"--port", "6379",
		"--username", "app",
		"--password", "pw",
	}
	if cmd := redis["command"].([]string); !reflect.DeepEqual(cmd, wantRedisCmd) {
		t.Fatalf("unexpected redis command: %#v", redis["command"])
	}
	redisEnv := redis["environment"].(map[string]string)
	wantRedisEnv := map[string]string{
		"REDIS_USERNAME": "app",
		"REDIS_HOST":     "redis",
		"REDIS_PORT":     "6379",
		"REDIS_PWD":      "pw",
	}
	if !reflect.DeepEqual(redisEnv, wantRedisEnv) {
		t.Fatalf("unexpected redis environment: %#v", redisEnv)
	}

	for _, name := range []string{"redis", "milvus"} {
		server := mcp[name].(map[string]interface{})
		if server["enabled"] != true {
			t.Fatalf("%s MCP should be enabled: %#v", name, server)
		}
	}

	// No service blocks -> no mcp section.
	if got := buildMCPFromOpsConfig(&opsConfig{}); got != nil {
		t.Fatalf("empty ops config should yield nil mcp, got %#v", got)
	}
}

// The opencode.json is a single, self-contained file fully regenerated in the
// app's workbench project folder on every launch: provider + model defaults +
// lsp, instructions pointing at the project's own contract/opencode.md, the
// checker script, and the openserverless MCP server wired in. There is no
// merge — any pre-existing content in the file (custom providers/lsp/mcp, stale
// managed entries) is discarded so the result always reflects the current
// config.
func TestGenerateOpencodeConfigInProjectDir(t *testing.T) {
	origWorkbench := WorkbenchDir
	t.Cleanup(func() { WorkbenchDir = origWorkbench })
	WorkbenchDir = t.TempDir()
	checkerInstallPath := isolateOpenServerlessCheckerInstall(t)

	app := "demo"
	appDir := filepath.Join(WorkbenchDir, app)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir app dir: %s", err)
	}

	// Seed an existing project file with custom + stale entries that must NOT
	// survive the full regeneration.
	seed := map[string]interface{}{
		"provider": map[string]interface{}{
			"custom-provider": map[string]interface{}{"options": map[string]interface{}{}},
		},
		"lsp": map[string]interface{}{
			"custom-lsp": map[string]interface{}{"command": []string{"my-lsp"}},
		},
		"mcp": map[string]interface{}{
			"postgres": map[string]interface{}{
				"type":        "local",
				"command":     []string{"postgres-mcp", "--access-mode=unrestricted"},
				"environment": map[string]interface{}{"DATABASE_URI": "postgres://stale:stale@old/db"},
			},
		},
	}
	seedData, _ := json.Marshal(seed)
	if err := os.WriteFile(filepath.Join(appDir, "opencode.json"), seedData, 0644); err != nil {
		t.Fatalf("seed project config: %s", err)
	}

	cfg := &trustableConfig{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434/v1",
		APIKey:   "dummy",
		Models:   map[string]*ModelLimits{"qwen3:latest": {MaxToken: 131072, MaxOutput: 32768}},
		Opencode: &opencodeConfig{Default: "qwen3:latest", Small: "qwen3:latest"},
	}

	if err := generateOpencodeConfigForApp(cfg, app); err != nil {
		t.Fatalf("generateOpencodeConfigForApp: %s", err)
	}

	data, err := os.ReadFile(filepath.Join(appDir, "opencode.json"))
	if err != nil {
		t.Fatalf("read project config: %s", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse project config: %s", err)
	}

	// Full config: provider + model + lsp present.
	if got["model"] != "ollama/qwen3:latest" {
		t.Fatalf("unexpected model: %#v", got["model"])
	}
	providers, ok := got["provider"].(map[string]interface{})
	if !ok || providers["ollama"] == nil {
		t.Fatalf("generated provider missing: %#v", got["provider"])
	}
	// No merge: the seeded custom provider/lsp must be gone.
	if _, present := providers["custom-provider"]; present {
		t.Fatalf("custom provider should be discarded by full regeneration: %#v", providers)
	}
	lsp, ok := got["lsp"].(map[string]interface{})
	if !ok || lsp["typescript"] == nil || lsp["python"] == nil {
		t.Fatalf("generated lsp missing: %#v", got["lsp"])
	}
	if _, present := lsp["custom-lsp"]; present {
		t.Fatalf("custom lsp should be discarded by full regeneration: %#v", lsp)
	}

	// instructions must point first at the project's own OpenServerless
	// contract, then at opencode.md.
	instr, ok := got["instructions"].([]interface{})
	wantContract := filepath.Join(appDir, ".openserverless-contract.md")
	wantMd := filepath.Join(appDir, "opencode.md")
	if !ok || len(instr) != 2 || instr[0] != wantContract || instr[1] != wantMd {
		t.Fatalf("instructions should reference %s then %s, got %#v", wantContract, wantMd, got["instructions"])
	}

	// The contract and opencode.md must be written in the project dir, not under
	// ~/.config. The checker is installed once in the configured bin path and is
	// executable; it is not duplicated into every app repo.
	if _, err := os.Stat(wantContract); err != nil {
		t.Fatalf(".openserverless-contract.md not written to project dir: %s", err)
	}
	if _, err := os.Stat(wantMd); err != nil {
		t.Fatalf("opencode.md not written to project dir: %s", err)
	}
	if _, err := os.Stat(filepath.Join(appDir, "scripts", "check_openserverless_actions.sh")); !os.IsNotExist(err) {
		t.Fatalf("checker should not be copied into app repo, stat err=%v", err)
	}
	checkerInfo, err := os.Stat(checkerInstallPath)
	if err != nil {
		t.Fatalf("check_openserverless_actions.sh not installed: %s", err)
	}
	if checkerInfo.Mode()&0111 == 0 {
		t.Fatalf("check_openserverless_actions.sh should be executable, mode=%s", checkerInfo.Mode())
	}
	// The action tools are provided by the openserverless MCP server, which must
	// always be wired into the mcp section.
	mcp, ok := got["mcp"].(map[string]interface{})
	if !ok {
		t.Fatalf("mcp section missing: %#v", got["mcp"])
	}
	oss, ok := mcp["openserverless"].(map[string]interface{})
	if !ok {
		t.Fatalf("openserverless MCP server missing: %#v", mcp)
	}
	if cmd, ok := oss["command"].([]interface{}); !ok || len(cmd) != 1 || cmd[0] != "openserverless-mcp" {
		t.Fatalf("unexpected openserverless command: %#v", oss["command"])
	}
	// No merge: if a postgres MCP is present it must be the freshly generated one,
	// never the stale seeded DATABASE_URI.
	if pg, present := mcp["postgres"].(map[string]interface{}); present {
		env, _ := pg["environment"].(map[string]interface{})
		if uri, _ := env["DATABASE_URI"].(string); uri == "postgres://stale:stale@old/db" {
			t.Fatalf("stale seeded postgres DATABASE_URI survived full regeneration: %#v", pg)
		}
	}

	// A Claude-format .mcp.json must be emitted with the same servers, translated
	// to the mcpServers/stdio schema.
	mcpData, err := os.ReadFile(filepath.Join(appDir, ".mcp.json"))
	if err != nil {
		t.Fatalf(".mcp.json not written to project dir: %s", err)
	}
	var claude struct {
		MCPServers map[string]map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcpData, &claude); err != nil {
		t.Fatalf("parse .mcp.json: %s", err)
	}
	cOss, ok := claude.MCPServers["openserverless"]
	if !ok {
		t.Fatalf("openserverless missing from .mcp.json: %#v", claude.MCPServers)
	}
	if cOss["type"] != "stdio" || cOss["command"] != "openserverless-mcp" {
		t.Fatalf("unexpected .mcp.json openserverless entry: %#v", cOss)
	}
}

// When vite.config.* contains AgentiReact(), the agentireact remote MCP server
// is added to opencode.json and translated to an http server in .mcp.json.
func TestGenerateOpencodeConfigAddsAgentiReactWhenViteConfigOptsIn(t *testing.T) {
	origWorkbench := WorkbenchDir
	t.Cleanup(func() { WorkbenchDir = origWorkbench })
	WorkbenchDir = t.TempDir()
	isolateOpenServerlessCheckerInstall(t)

	app := "demo"
	appDir := filepath.Join(WorkbenchDir, app)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir app dir: %s", err)
	}
	vite := "import AgentiReact from 'vite-plugin-agentireact'\nexport default { plugins: [AgentiReact()] }\n"
	if err := os.WriteFile(filepath.Join(appDir, "vite.config.ts"), []byte(vite), 0644); err != nil {
		t.Fatalf("write vite config: %s", err)
	}

	cfg := &trustableConfig{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434/v1",
		APIKey:   "dummy",
		Opencode: &opencodeConfig{Default: "qwen3:latest", Small: "qwen3:latest"},
	}
	if err := generateOpencodeConfigForApp(cfg, app); err != nil {
		t.Fatalf("generateOpencodeConfigForApp: %s", err)
	}

	data, err := os.ReadFile(filepath.Join(appDir, "opencode.json"))
	if err != nil {
		t.Fatalf("read opencode.json: %s", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse opencode.json: %s", err)
	}
	mcp, _ := got["mcp"].(map[string]interface{})
	ar, ok := mcp["agentireact"].(map[string]interface{})
	if !ok {
		t.Fatalf("agentireact MCP server missing: %#v", mcp)
	}
	if ar["type"] != "remote" || ar["url"] != "http://localhost:5173/mcp" {
		t.Fatalf("unexpected agentireact entry: %#v", ar)
	}

	// In .mcp.json (Claude format), remote -> http.
	mcpData, err := os.ReadFile(filepath.Join(appDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %s", err)
	}
	var claude struct {
		MCPServers map[string]map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcpData, &claude); err != nil {
		t.Fatalf("parse .mcp.json: %s", err)
	}
	cAR, ok := claude.MCPServers["agentireact"]
	if !ok {
		t.Fatalf("agentireact missing from .mcp.json: %#v", claude.MCPServers)
	}
	if cAR["type"] != "http" || cAR["url"] != "http://localhost:5173/mcp" {
		t.Fatalf("unexpected .mcp.json agentireact entry: %#v", cAR)
	}
}

func TestOpenServerlessCheckerPassesValidActionShape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "hello")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte("from hello import main\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "hello.py"), []byte("def main(args, ctx=None):\n    return {'ok': True}\n"), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checker should pass, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "OpenServerless action contract check passed") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerAllowsGeneratedPostgresWrapper(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "contacts")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := `#--kind python:default
#--web true
#--timeout 300000
import types
import os
import contacts

def init_postgresql(args, ctx):
    dburl = args.get("POSTGRES_URL") or os.getenv("POSTGRES_URL")
    import psycopg
    ctx.POSTGRESQL = psycopg.connect(dburl)

def main(args, ctx=None):
    if ctx is None:
        ctx = types.SimpleNamespace()
        init_postgresql(args, ctx)
    return {"body": contacts.main(args, ctx=ctx)}
`
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte(wrapper), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `def main(args, ctx=None):
    data = dict(args)
    ignored = {"body", "POSTGRES_URL", "__ow_method", "__ow_headers", "__ow_path"}
    merged = {k: v for k, v in data.items() if k not in ignored}
    return {"ok": True, "merged": merged}
`
	if err := os.WriteFile(filepath.Join(actionDir, "contacts.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checker should allow generated postgres wrappers, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	output := string(out)
	if strings.Contains(output, "Generated wrapper appears") || strings.Contains(output, "POSTGRES_URL") {
		t.Fatalf("checker should not report standard wrapper wiring or ignored POSTGRES_URL, got=%s", strings.TrimSpace(output))
	}
}

func TestOpenServerlessCheckerFailsNestedActionShape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "auth", "register")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir nested action: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte("print('bad shape')\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should fail for nested action, output=%s", strings.TrimSpace(string(out)))
	}
	output := string(out)
	if !strings.Contains(output, "Invalid nested action path") || !strings.Contains(output, ".openserverless-contract.md") {
		t.Fatalf("checker output should explain nested action and contract recovery, got=%s", strings.TrimSpace(output))
	}
}

func TestOpenServerlessCheckerIgnoresOpsIdeDeployZips(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "hello")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte("from hello import main\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "hello.py"), []byte("def main(args, ctx=None):\n    return {'ok': True}\n"), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "hello.zip"), []byte("deploy artifact\n"), 0644); err != nil {
		t.Fatalf("write deploy zip artifact: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checker should ignore deploy zips, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if strings.Contains(string(out), "zip") {
		t.Fatalf("checker should not report generated zip artifacts, got=%s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerWarnsOnFragileRouteIDParsing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "contacts")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte("from contacts import main\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `def main(args, ctx=None):
    method = args.get("__ow_method", "GET")
    path = args.get("__ow_path", "")
    if method == "DELETE":
        return {"ok": True, "path": path}
    return {"ok": True}
`
	if err := os.WriteFile(filepath.Join(actionDir, "contacts.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fragile route-id parsing should warn, not fail, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "route-id extraction helper") {
		t.Fatalf("checker should warn about fragile route id parsing, got=%s", strings.TrimSpace(string(out)))
	}
}

// Without an AgentiReact() opt-in, no agentireact server is added.
func TestGenerateOpencodeConfigSkipsAgentiReactWithoutOptIn(t *testing.T) {
	origWorkbench := WorkbenchDir
	t.Cleanup(func() { WorkbenchDir = origWorkbench })
	WorkbenchDir = t.TempDir()
	isolateOpenServerlessCheckerInstall(t)

	app := "demo"
	appDir := filepath.Join(WorkbenchDir, app)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir app dir: %s", err)
	}
	// A vite config that does NOT use AgentiReact.
	if err := os.WriteFile(filepath.Join(appDir, "vite.config.js"), []byte("export default {}\n"), 0644); err != nil {
		t.Fatalf("write vite config: %s", err)
	}

	cfg := &trustableConfig{Provider: "ollama", BaseURL: "http://localhost:11434/v1", APIKey: "dummy"}
	if err := generateOpencodeConfigForApp(cfg, app); err != nil {
		t.Fatalf("generateOpencodeConfigForApp: %s", err)
	}
	data, _ := os.ReadFile(filepath.Join(appDir, "opencode.json"))
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse opencode.json: %s", err)
	}
	mcp, _ := got["mcp"].(map[string]interface{})
	if _, present := mcp["agentireact"]; present {
		t.Fatalf("agentireact should be absent without opt-in: %#v", mcp)
	}
}

func TestNormalizeOpenCodeAgentColorMapsLegacyNames(t *testing.T) {
	cases := map[string]string{
		"blue":      "primary",
		"purple":    "secondary",
		"green":     "success",
		"yellow":    "warning",
		"red":       "error",
		"cyan":      "info",
		"primary":   "primary",
		"#1a2B3c":   "#1a2B3c",
		"not-valid": "primary",
	}
	for input, want := range cases {
		if got := normalizeOpenCodeAgentColor(input); got != want {
			t.Fatalf("normalizeOpenCodeAgentColor(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestOpenCodeProjectIDIsStablePerApp(t *testing.T) {
	first := openCodeProjectID("truorderingestion")
	second := openCodeProjectID("truorderingestion")
	other := openCodeProjectID("truk8s")
	if first != second {
		t.Fatalf("openCodeProjectID should be stable: %q != %q", first, second)
	}
	if first == other {
		t.Fatalf("openCodeProjectID should differ per app: %q", first)
	}
	if len(first) != 40 {
		t.Fatalf("openCodeProjectID length = %d, want 40", len(first))
	}
	for _, ch := range first {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			t.Fatalf("openCodeProjectID contains non-hex character %q in %q", ch, first)
		}
	}
}
