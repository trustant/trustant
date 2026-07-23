package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeActionDeployArtifact(t *testing.T, actionDir string) string {
	t.Helper()
	archive := actionDir + ".zip"
	if err := os.WriteFile(archive, []byte("ops ide deploy artifact\n"), 0644); err != nil {
		t.Fatalf("write deploy artifact: %s", err)
	}
	deployedAt := time.Now().Add(time.Second)
	if err := os.Chtimes(archive, deployedAt, deployedAt); err != nil {
		t.Fatalf("set deploy artifact time: %s", err)
	}
	return archive
}

func isolateOpenServerlessCheckerInstall(t *testing.T) string {
	t.Helper()
	origCheckerPath := openServerlessCheckerInstallPathOverride
	origFrontendPath := frontendCheckerInstallPathOverride
	origAppPath := appCheckerInstallPathOverride
	root := t.TempDir()
	checkerInstallPath := filepath.Join(root, "bin", "check_openserverless_actions.sh")
	openServerlessCheckerInstallPathOverride = checkerInstallPath
	frontendCheckerInstallPathOverride = filepath.Join(root, "bin", "check_trustable_frontend.sh")
	appCheckerInstallPathOverride = filepath.Join(root, "bin", "check_trustable_app.sh")
	t.Cleanup(func() {
		openServerlessCheckerInstallPathOverride = origCheckerPath
		frontendCheckerInstallPathOverride = origFrontendPath
		appCheckerInstallPathOverride = origAppPath
	})
	return checkerInstallPath
}

func TestBrowserMCPUsesOnlyManagedDevelopmentAndConfiguredExternalTargets(t *testing.T) {
	t.Setenv("OPS_APIHOST", "https://cluster.example.test:8443")
	origWorkspace := WorkspaceDir
	WorkspaceDir = "/home/trustable/workspace"
	t.Cleanup(func() { WorkspaceDir = origWorkspace })

	config := browserMCPConfig("/home/trustable/workbench/demo")
	command := config["command"].([]string)
	if len(command) != 1 || command[0] != "trustable-browser-mcp" {
		t.Fatalf("unexpected browser MCP command: %#v", command)
	}
	environment := config["environment"].(map[string]string)
	if got := environment["TRUSTABLE_BROWSER_EXTERNAL_ORIGIN"]; got != "https://vite.cluster.example.test:8443" {
		t.Fatalf("unexpected browser external origin: %q", got)
	}
	if got := environment["TRUSTABLE_BROWSER_ARTIFACT_DIR"]; got != "/home/trustable/workspace/.trustable/browser/demo" {
		t.Fatalf("unexpected browser artifact dir: %q", got)
	}
}

func TestModelAllowedForPiBlocksNonAgentModels(t *testing.T) {
	cases := []string{
		"Qwen3-Embedding-8B",
		"bestia/embedding:952mb",
		"bestia/rerank:q8",
		"bestia/tiny:1b",
		"bestia/small:3b",
		"nomic-embed-text:latest",
		"gte-Qwen2",
	}
	for _, model := range cases {
		if ok, reason := modelAllowedForPi("bestia", model, nil); ok || reason == "" {
			t.Fatalf("%s should be blocked for Pi, ok=%v reason=%q", model, ok, reason)
		}
	}

	allowed := []string{
		"bestia/coding:30b",
		"qwen3.6:35b",
		"qwen3.6-27b",
		"gpt-oss-20b",
	}
	for _, model := range allowed {
		if ok, reason := modelAllowedForPi("bestia", model, nil); !ok {
			t.Fatalf("%s should be allowed for Pi, reason=%q", model, reason)
		}
	}
}

func TestModelAllowedForPiHonorsCatalogMetadata(t *testing.T) {
	disabled := false
	if ok, reason := modelAllowedForPi("trustable", "qwen3.6-27b", &ModelLimits{
		Enabled: &disabled,
		Reason:  "temporarily unavailable",
	}); ok || reason != "temporarily unavailable" {
		t.Fatalf("disabled catalog model should be blocked with reason, ok=%v reason=%q", ok, reason)
	}

	if ok, reason := modelAllowedForPi("trustable", "custom-safe-model", &ModelLimits{Roles: []string{"coding"}}); !ok {
		t.Fatalf("coding role should allow model, reason=%q", reason)
	}
	if ok, reason := modelAllowedForPi("trustable", "custom-vector-model", &ModelLimits{Roles: []string{"embedding"}}); ok || reason == "" {
		t.Fatalf("embedding role should block model, ok=%v reason=%q", ok, reason)
	}
}

func TestValidatePiModelSelectionRejectsDisallowedSelectedModel(t *testing.T) {
	cfg := &trustableConfig{
		Provider: "bestia",
		Models: map[string]*ModelLimits{
			"bestia/embedding:952mb": {MaxInput: 8192},
			"qwen3.6:35b":            {MaxInput: 131072},
		},
		Pi: &piConfig{Default: "bestia/embedding:952mb"},
	}
	err := validatePiModelSelection(cfg)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected disallowed model validation error, got %v", err)
	}
}

func TestValidatePiModelSelectionAllowsDeferredDiscovery(t *testing.T) {
	cfg := &trustableConfig{
		Provider: "bestia",
		Models:   map[string]*ModelLimits{},
		Pi:       &piConfig{Default: ""},
	}
	if err := validatePiModelSelection(cfg); err != nil {
		t.Fatalf("empty provider-choice config should be allowed before discovery: %s", err)
	}
}

func TestTrustableConfigDoesNotMigrateLegacyOpenCodeSelection(t *testing.T) {
	// Issue #51 requires an explicit first-run Pi selection; accepting this old
	// object would hide the cutover and could silently choose the wrong model.
	var cfg trustableConfig
	if err := json.Unmarshal([]byte(`{
		"provider": "trustable",
		"opencode": {"default": "legacy-default", "small": "legacy-small"}
	}`), &cfg); err != nil {
		t.Fatalf("unmarshal legacy configuration: %s", err)
	}
	if cfg.Pi != nil || piDefaultModel(&cfg) != "" {
		t.Fatalf("legacy opencode selection must not populate Pi: %#v", cfg.Pi)
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
	cfg.MongoDB.URI = "mongodb://app:pw@mongodb:27017/appdb"

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

	mongodb := mcp["mongodb"].(map[string]interface{})
	if cmd := mongodb["command"].([]string); len(cmd) != 1 || cmd[0] != "mongodb-mcp-server" {
		t.Fatalf("unexpected mongodb command: %#v", mongodb["command"])
	}
	mongodbEnv := mongodb["environment"].(map[string]string)
	if mongodbEnv["MDB_MCP_CONNECTION_STRING"] != "mongodb://app:pw@mongodb:27017/appdb" {
		t.Fatalf("unexpected mongodb connection string: %#v", mongodbEnv)
	}

	for _, name := range []string{"redis", "milvus", "mongodb"} {
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

func TestBuildLaunchMCPMongoDBFromOfficialConfigOnly(t *testing.T) {
	var cfg opsConfig
	cfg.MongoDB.Host = "mongodb"
	cfg.MongoDB.Port = 27017
	cfg.MongoDB.Database = "appdb"
	cfg.MongoDB.Username = "app"
	cfg.MongoDB.Password = "pw"
	cfg.MongoDB.AuthSource = "admin"

	mcp := buildMCPFromOpsConfig(&cfg)
	mongodb := mcp["mongodb"].(map[string]interface{})
	env := mongodb["environment"].(map[string]string)
	if got := env["MDB_MCP_CONNECTION_STRING"]; got != "mongodb://app:pw@mongodb:27017/appdb?authSource=admin" {
		t.Fatalf("unexpected derived mongodb connection string: %s", got)
	}

	var noOfficialCapability opsConfig
	noOfficialCapability.MongoDB.Host = "mongodb"
	if got := buildMCPFromOpsConfig(&noOfficialCapability); got != nil {
		t.Fatalf("incomplete mongodb block should not enable MCP, got %#v", got)
	}
}

func TestGeneratedAppEnvIncludesPersistentSecretsWithoutOverridingManagedValues(t *testing.T) {
	origWorkspace := WorkspaceDir
	origWorkbench := WorkbenchDir
	t.Cleanup(func() {
		WorkspaceDir = origWorkspace
		WorkbenchDir = origWorkbench
	})

	root := t.TempDir()
	home := filepath.Join(root, "home")
	WorkspaceDir = filepath.Join(root, "workspace")
	WorkbenchDir = filepath.Join(root, "workbench")
	t.Setenv("HOME", home)

	if err := os.MkdirAll(filepath.Join(home, ".ops"), 0755); err != nil {
		t.Fatalf("mkdir ops config dir: %s", err)
	}
	opsConfigJSON := `{
  "mongodb": {
    "uri": "mongodb://app:pw@mongodb:27017/appdb"
  }
}`
	if err := os.WriteFile(filepath.Join(home, ".ops", "config.json"), []byte(opsConfigJSON), 0644); err != nil {
		t.Fatalf("write ops config: %s", err)
	}
	if err := os.MkdirAll(WorkspaceDir, 0755); err != nil {
		t.Fatalf("mkdir workspace: %s", err)
	}
	workspaceConfig := `{
  "apps": {
    "truapp": {
      "password": "secret",
      "development": {
        "MONGODB_URI": "mongodb://stale:pw@old/db",
        "CUSTOM": "dev"
      },
      "production": {
        "MONGODB_URI": "mongodb://prod:pw@old/db"
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(WorkspaceDir, "trustable.json"), []byte(workspaceConfig), 0644); err != nil {
		t.Fatalf("write workspace config: %s", err)
	}
	if err := os.MkdirAll(filepath.Join(WorkbenchDir, "truapp"), 0755); err != nil {
		t.Fatalf("mkdir workbench: %s", err)
	}
	secretPath := appSecretStorePath("truapp")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0700); err != nil {
		t.Fatalf("mkdir app secret store: %s", err)
	}
	if err := os.WriteFile(secretPath, []byte("JWT_SECRET=persistent-test-value\nOPS_PASSWORD=must-not-win\nMONGODB_URI=must-not-leak\n"), 0600); err != nil {
		t.Fatalf("write app secret store: %s", err)
	}

	if err := generateAppEnvFiles("truapp"); err != nil {
		t.Fatalf("generate env: %s", err)
	}

	env := parseEnvFile(filepath.Join(WorkbenchDir, "truapp", ".env"))
	if got := env["MONGODB_URI"]; got != "" {
		t.Fatalf("MONGODB_URI must not be written to app .env, got %q", got)
	}
	if got := env["CUSTOM"]; got != "dev" {
		t.Fatalf("expected ordinary development env to remain, got %q", got)
	}
	if got := env["JWT_SECRET"]; got != "persistent-test-value" {
		t.Fatalf("expected persistent app secret in generated env, got %q", got)
	}
	if got := env["OPS_PASSWORD"]; got != "secret" {
		t.Fatalf("persistent secrets must not override managed OPS_PASSWORD, got %q", got)
	}

	runtimeEnv := appServiceRuntimeEnv([]string{"BASE=1"})
	hasMongoRuntime := false
	for _, item := range runtimeEnv {
		if item == "MONGODB_URI=mongodb://app:pw@mongodb:27017/appdb" {
			hasMongoRuntime = true
		}
	}
	if !hasMongoRuntime {
		t.Fatalf("MongoDB runtime env should be available to launch processes, got %#v", runtimeEnv)
	}
}

// Project generation follows the Pi/ACP contract even when the workbench path
// is a symlink: standard instructions, MCP config, contract, and checkers are
// generated without creating or rewriting an OpenCode configuration.
func TestGenerateProjectAssetsInProjectDir(t *testing.T) {
	origWorkbench := WorkbenchDir
	t.Cleanup(func() { WorkbenchDir = origWorkbench })
	durableWorkbench := t.TempDir()
	canonicalDurableWorkbench, err := filepath.EvalSymlinks(durableWorkbench)
	if err != nil {
		t.Fatalf("resolve durable workbench: %v", err)
	}
	workbenchAliasParent := t.TempDir()
	WorkbenchDir = filepath.Join(workbenchAliasParent, "workbench")
	if err := os.Symlink(durableWorkbench, WorkbenchDir); err != nil {
		t.Fatalf("symlink workbench: %s", err)
	}
	checkerInstallPath := isolateOpenServerlessCheckerInstall(t)

	app := "demo"
	appDir := filepath.Join(WorkbenchDir, app)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir app dir: %s", err)
	}

	// A stale file may remain in an older app checkout. The Pi asset generator
	// must ignore it rather than treating it as current runtime configuration.
	seedData := []byte(`{"legacy":true}`)
	if err := os.WriteFile(filepath.Join(appDir, "opencode.json"), seedData, 0644); err != nil {
		t.Fatalf("seed project config: %s", err)
	}

	if err := generateProjectAssetsForApp(app); err != nil {
		t.Fatalf("generateProjectAssetsForApp: %s", err)
	}

	data, err := os.ReadFile(filepath.Join(appDir, "opencode.json"))
	if err != nil {
		t.Fatalf("read legacy project config: %s", err)
	}
	if string(data) != string(seedData) {
		t.Fatalf("legacy opencode.json must not be rewritten, got %s", data)
	}
	wantContract := filepath.Join(canonicalDurableWorkbench, app, ".openserverless-contract.md")

	// AGENTS.md, CLAUDE.md, and the contract belong in the project directory.
	// Checkers remain user-local executables and are not copied into app repos.
	agentsData, err := os.ReadFile(filepath.Join(appDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md not written to project dir: %s", err)
	}
	agents := string(agentsData)
	if !strings.Contains(agents, "TRUSTABLE-MANAGED-AGENTS-BEGIN") ||
		!strings.Contains(agents, "Ignore `CLAUDE.md`") ||
		!strings.Contains(agents, ".openserverless-contract.md") {
		t.Fatalf("AGENTS.md missing Trustable managed guardrails: %s", agents)
	}
	if _, err := os.Stat(wantContract); err != nil {
		t.Fatalf(".openserverless-contract.md not written to project dir: %s", err)
	}
	if _, err := os.Stat(filepath.Join(appDir, "CLAUDE.md")); err != nil {
		t.Fatalf("CLAUDE.md not written to project dir: %s", err)
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
	for _, path := range []string{frontendCheckerInstallPathOverride, appCheckerInstallPathOverride} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Trustable checker not installed at %s: %s", path, err)
		}
		if info.Mode()&0111 == 0 {
			t.Fatalf("Trustable checker should be executable at %s, mode=%s", path, info.Mode())
		}
	}
	// The shared .mcp.json is the sole project-local MCP configuration.
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
	if cOss["lifecycle"] != "eager" {
		t.Fatalf("openserverless must connect when the Pi session starts: %#v", cOss)
	}
	cOssEnv, ok := cOss["env"].(map[string]interface{})
	if !ok || cOssEnv["OPENSERVERLESS_SECRETS_FILE"] != appSecretStorePath(app) {
		t.Fatalf(".mcp.json persistent secret store missing: %#v", cOss)
	}
	cBrowser, ok := claude.MCPServers["browser"]
	if !ok || cBrowser["type"] != "stdio" || cBrowser["command"] != "trustable-browser-mcp" {
		t.Fatalf("unexpected .mcp.json browser entry: %#v", cBrowser)
	}
	if cBrowser["lifecycle"] != "eager" {
		t.Fatalf("browser must connect when the Pi session starts: %#v", cBrowser)
	}
}

func TestManagedAppAgentsPreservesExistingNotesWithoutDuplication(t *testing.T) {
	first := mergeManagedAppAgents("Existing template guidance\n")
	if !strings.Contains(first, trustableAgentsBegin) ||
		!strings.Contains(first, "## App-local notes\n\nExisting template guidance") {
		t.Fatalf("managed AGENTS.md should prepend guard and preserve notes: %s", first)
	}

	second := mergeManagedAppAgents(first)
	if strings.Count(second, "## App-local notes") != 1 {
		t.Fatalf("managed AGENTS.md should not duplicate App-local notes: %s", second)
	}
	if strings.Count(second, trustableAgentsBegin) != 1 {
		t.Fatalf("managed AGENTS.md should replace, not duplicate, managed block: %s", second)
	}
}

// When vite.config.* contains AgentiReact(), the standard project MCP config
// includes the Vite-served endpoint as an eager HTTP server for Pi.
func TestGenerateProjectAssetsAddsAgentiReactWhenViteConfigOptsIn(t *testing.T) {
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

	if err := generateProjectAssetsForApp(app); err != nil {
		t.Fatalf("generateProjectAssetsForApp: %s", err)
	}

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
	if cAR["lifecycle"] != "eager" {
		t.Fatalf("remote MCP servers must connect when the Pi session starts: %#v", cAR)
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
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checker should pass, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "OpenServerless action contract check passed") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRejectsManagedRuntimeParameters(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "stack-status")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := `#--kind python:default
#--web true
#--param OPS_APIHOST "$OPS_APIHOST"
import stack_status
`
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte(wrapper), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "stack_status.py"), []byte("def main(args, ctx=None):\n    return {'ok': True}\n"), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should reject Trustable-managed action parameters, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "Trustable-managed runtime variables must not be bound into actions") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerIgnoresVendoredPythonDependencies(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "cache")
	if err := os.MkdirAll(filepath.Join(actionDir, "virtualenv", "lib", "python3.12", "site-packages", "redis"), 0755); err != nil {
		t.Fatalf("mkdir action dependencies: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte("from cache import main\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := "def main(args, ctx=None):\n    return {'ok': True}\n"
	if err := os.WriteFile(filepath.Join(actionDir, "cache.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	vendored := "def get(key):\n    return REDIS.get(key)\n"
	if err := os.WriteFile(filepath.Join(actionDir, "virtualenv", "lib", "python3.12", "site-packages", "redis", "client.py"), []byte(vendored), 0644); err != nil {
		t.Fatalf("write vendored module: %s", err)
	}
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checker should ignore vendored dependencies, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "OpenServerless action contract check passed") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRejectsMongoMilvusSubstitution(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "stack")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte("from stack import main\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `from pymilvus import MilvusClient

def main(args, ctx=None):
    return {"component": "MongoDB", "client": MilvusClient}
`
	if err := os.WriteFile(filepath.Join(actionDir, "stack.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should reject MongoDB implemented with Milvus, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "Do not use Milvus/vector tooling as a MongoDB substitute") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerAllowsIndependentMongoDBAndMilvusChecks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "monitor")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := `from monitor import main

def init_mongodb(args, ctx):
    ctx.MONGODB_CLIENT = object()

def init_milvus(args, ctx):
    ctx.MILVUS = object()
`
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte(wrapper), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `def check_mongodb(ctx):
    return ctx.MONGODB_CLIENT.admin.command("ping")

def check_milvus(ctx):
    return ctx.MILVUS.list_collections()

def main(args, ctx=None):
    return {"mongodb": check_mongodb(ctx), "milvus": check_milvus(ctx)}
`
	if err := os.WriteFile(filepath.Join(actionDir, "monitor.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checker should allow independent MongoDB and Milvus checks, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "OpenServerless action contract check passed") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRejectsMongoMCPRuntimeEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "stack")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte("from stack import main\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `import os

def main(args, ctx=None):
    uri = os.getenv("MDB_MCP_CONNECTION_STRING")
    return {"component": "MongoDB", "uri": uri}
`
	if err := os.WriteFile(filepath.Join(actionDir, "stack.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should reject MongoDB MCP runtime env usage, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "MDB_MCP_CONNECTION_STRING is private to the MongoDB MCP server") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRejectsGuessedMongoRuntimeEnvWithoutBinding(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "stack")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte("from stack import main\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `import os

def main(args, ctx=None):
    uri = os.getenv("MONGODB_URI") or os.getenv("MDB_CONNECTION_STRING")
    return {"component": "MongoDB", "uri": uri}
`
	if err := os.WriteFile(filepath.Join(actionDir, "stack.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should reject guessed MongoDB runtime env usage, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "MongoDB runtime environment was guessed in action code") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRejectsRedisKeysWithoutPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "stack")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := `#--kind python:default
#--web true
def init_redis(args, ctx):
    ctx.REDIS = object()
    ctx.REDIS_PREFIX = "user:"
`
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte(wrapper), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `def main(args, ctx=None):
    value = ctx.REDIS.get("stack-e2e-key")
    ctx.REDIS.set("stack-e2e-key", "stack-ok-42")
    return {"value": value}
`
	if err := os.WriteFile(filepath.Join(actionDir, "stack.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should reject Redis keys without prefix, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "Redis action code uses Redis keys without the generated ctx.REDIS_PREFIX") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerAllowsRedisKeysWithPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "stack")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := `#--kind python:default
#--web true
def init_redis(args, ctx):
    ctx.REDIS = object()
    ctx.REDIS_PREFIX = "user:"
`
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte(wrapper), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `def redis_key(ctx, name):
    return f"{getattr(ctx, 'REDIS_PREFIX', '') or ''}{name}"

def main(args, ctx=None):
    key = redis_key(ctx, "stack-e2e-key")
    ctx.REDIS.set(key, "stack-ok-42")
    return {"value": ctx.REDIS.get(key)}
`
	if err := os.WriteFile(filepath.Join(actionDir, "stack.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checker should allow prefixed Redis keys, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRejectsS3ListBuckets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "stack")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := `#--kind python:default
#--web true
def init_s3(args, ctx):
    ctx.S3_CLIENT = object()
    ctx.S3_DATA = "app-data"
`
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte(wrapper), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `def main(args, ctx=None):
    return {"buckets": ctx.S3_CLIENT.list_buckets()}
`
	if err := os.WriteFile(filepath.Join(actionDir, "stack.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should reject S3 list_buckets, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "S3 action code must never call list_buckets()") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRejectsS3ReadWriteClaimWithoutRealOperations(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "stack")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := `#--kind python:default
#--web true
def init_s3(args, ctx):
    ctx.S3_CLIENT = object()
    ctx.S3_DATA = "app-data"
`
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte(wrapper), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `def main(args, ctx=None):
    ctx.S3_CLIENT.head_bucket(Bucket=ctx.S3_DATA)
    return {"connected": True, "read_write": "OK"}
`
	if err := os.WriteFile(filepath.Join(actionDir, "stack.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should reject an unproved S3 read/write claim, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "S3 read/write status requires a real ctx.S3_DATA check") {
		t.Fatalf("unexpected checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerAllowsRealS3ReadWriteVerification(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "stack")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := `#--kind python:default
#--web true
def init_s3(args, ctx):
    ctx.S3_CLIENT = object()
    ctx.S3_DATA = "app-data"
`
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte(wrapper), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `def main(args, ctx=None):
    expected = b"trustable-s3-ok"
    key = "trustable-check/unique.txt"
    try:
        ctx.S3_CLIENT.put_object(Bucket=ctx.S3_DATA, Key=key, Body=expected)
        actual = ctx.S3_CLIENT.get_object(Bucket=ctx.S3_DATA, Key=key)["Body"].read()
        if actual != expected:
            raise RuntimeError("S3 read-back mismatch")
        return {"connected": True, "read_write": "OK"}
    finally:
        ctx.S3_CLIENT.delete_object(Bucket=ctx.S3_DATA, Key=key)
`
	if err := os.WriteFile(filepath.Join(actionDir, "stack.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checker should allow a real S3 read/write check, err=%s output=%s", err, strings.TrimSpace(string(out)))
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
	writeActionDeployArtifact(t, actionDir)

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

func TestOpenServerlessCheckerFailsManualWrapperWithoutGeneratedMarkers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "contacts")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := `import contacts

def main(args, ctx=None):
    return contacts.main(args, ctx=ctx)
`
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte(wrapper), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "contacts.py"), []byte("def main(args, ctx=None):\n    return {'ok': True}\n"), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should fail manual wrapper drift, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "lacks generated action/service markers") {
		t.Fatalf("checker should explain manual wrapper drift, got=%s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerFailsActionModuleWithoutWrapper(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "contacts")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "contacts.py"), []byte("def main(args, ctx=None):\n    return {'ok': True}\n"), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should fail module without wrapper, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "Action module exists without generated __main__.py") {
		t.Fatalf("checker should explain missing wrapper, got=%s", strings.TrimSpace(string(out)))
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
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checker should ignore deploy zips, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if strings.Contains(string(out), "zip") {
		t.Fatalf("checker should not report generated zip artifacts, got=%s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRejectsManualNestedActionZip(t *testing.T) {
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
	writeActionDeployArtifact(t, actionDir)
	if err := os.WriteFile(filepath.Join(actionDir, "hello.zip"), []byte("manual archive\n"), 0644); err != nil {
		t.Fatalf("write manual zip: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should reject a nested action zip, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "must not be created or edited inside an action source directory") {
		t.Fatalf("checker should explain manual ZIP drift, got=%s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRequiresMissingDeployArchive(t *testing.T) {
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
	if err == nil {
		t.Fatalf("checker should require a missing deploy archive, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "Deploy archive is missing") {
		t.Fatalf("checker should require ops ide deploy, got=%s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerRequiresDeployAfterActionChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "hello")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	wrapper := filepath.Join(actionDir, "__main__.py")
	module := filepath.Join(actionDir, "hello.py")
	if err := os.WriteFile(wrapper, []byte("from hello import main\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	if err := os.WriteFile(module, []byte("def main(args, ctx=None):\n    return {'ok': True}\n"), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	archive := writeActionDeployArtifact(t, actionDir)
	changedAt := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(module, changedAt, changedAt); err != nil {
		t.Fatalf("set changed source time: %s", err)
	}
	if err := os.Chtimes(archive, time.Now(), time.Now()); err != nil {
		t.Fatalf("reset deploy artifact time: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("checker should require deploy after an action change, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "Action source is newer than its deploy archive") {
		t.Fatalf("checker should require ops ide deploy, got=%s", strings.TrimSpace(string(out)))
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
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fragile route-id parsing should warn, not fail, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "route-id extraction helper") {
		t.Fatalf("checker should warn about fragile route id parsing, got=%s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerWarnsOnHTMLReturnedAsJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	actionDir := filepath.Join(dir, "packages", "v1", "invoice")
	if err := os.MkdirAll(actionDir, 0755); err != nil {
		t.Fatalf("mkdir action: %s", err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "__main__.py"), []byte("from invoice import main\n"), 0644); err != nil {
		t.Fatalf("write wrapper: %s", err)
	}
	module := `def main(args, ctx=None):
    html = "<!DOCTYPE html><html><body>Invoice</body></html>"
    return {"ok": True, "html": html}
`
	if err := os.WriteFile(filepath.Join(actionDir, "invoice.py"), []byte(module), 0644); err != nil {
		t.Fatalf("write module: %s", err)
	}
	writeActionDeployArtifact(t, actionDir)

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("HTML-as-JSON should warn, not fail, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "return it as application JSON") {
		t.Fatalf("checker should warn about HTML returned as JSON, got=%s", strings.TrimSpace(string(out)))
	}
}

func TestOpenServerlessCheckerWarnsOnDirectWindowOpenAPI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".openserverless-contract.md"), []byte("contract\n"), 0644); err != nil {
		t.Fatalf("write contract: %s", err)
	}
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	ui := "export function InvoiceButton() {\n" +
		"  return <button onClick={() => window.open(`/api/my/v1/invoice/${id}`, \"_blank\")}>Invoice</button>\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(srcDir, "InvoiceButton.tsx"), []byte(ui), 0644); err != nil {
		t.Fatalf("write ui: %s", err)
	}

	cmd := exec.Command("bash", "check_openserverless_actions.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("window.open /api/my should warn, not fail, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "opens a /api/my action URL directly") {
		t.Fatalf("checker should warn about direct window.open API target, got=%s", strings.TrimSpace(string(out)))
	}
}

func TestFrontendCheckerRejectsRootAnchorsWithHashRouter(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	app := "import { HashRouter } from 'react-router-dom';\nexport const App = () => <HashRouter><a href=\"/login\">Login</a></HashRouter>;\n"
	if err := os.WriteFile(filepath.Join(srcDir, "App.tsx"), []byte(app), 0644); err != nil {
		t.Fatalf("write app: %s", err)
	}

	cmd := exec.Command("bash", "check_trustable_frontend.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("HashRouter root anchor should fail, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "HashRouter internal navigation") {
		t.Fatalf("unexpected frontend checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestFrontendCheckerAcceptsRouterLinks(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	app := "import { HashRouter, Link } from 'react-router-dom';\nexport const App = () => <HashRouter><Link to=\"/login\">Login</Link></HashRouter>;\n"
	if err := os.WriteFile(filepath.Join(srcDir, "App.tsx"), []byte(app), 0644); err != nil {
		t.Fatalf("write app: %s", err)
	}

	cmd := exec.Command("bash", "check_trustable_frontend.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("router Link should pass, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
}

func TestFrontendCheckerRejectsHashPrefixedRouterLinks(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	app := "import { HashRouter, Link, useNavigate } from 'react-router-dom';\n" +
		"export const App = () => <HashRouter><Link to=\"#/login\">Login</Link></HashRouter>;\n" +
		"export const Button = () => { const navigate = useNavigate(); return <button onClick={() => navigate('#/register')}>Register</button>; };\n"
	if err := os.WriteFile(filepath.Join(srcDir, "App.tsx"), []byte(app), 0644); err != nil {
		t.Fatalf("write app: %s", err)
	}

	cmd := exec.Command("bash", "check_trustable_frontend.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("hash-prefixed router targets should fail, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "HashRouter APIs receive logical paths") {
		t.Fatalf("unexpected frontend checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestFrontendCheckerRejectsPasswordInQueryString(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	api := "export const login = (email, password) => fetch(`/api/auth?email=${email}&password=${password}`, { method: 'POST' });\n"
	if err := os.WriteFile(filepath.Join(srcDir, "api.ts"), []byte(api), 0644); err != nil {
		t.Fatalf("write api: %s", err)
	}

	cmd := exec.Command("bash", "check_trustable_frontend.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("password query should fail, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "Do not put passwords in URLs") {
		t.Fatalf("unexpected frontend checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestFrontendCheckerRejectsBrowserControlledIdentity(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	api := "export const hardcoded = () => fetch('/api/profile?user_id=1');\n" +
		"export const dynamic = (token, user) => fetch(`/api/profile?user_id=${user.id}`, { headers: { Authorization: `Bearer ${token}` } });\n"
	if err := os.WriteFile(filepath.Join(srcDir, "api.ts"), []byte(api), 0644); err != nil {
		t.Fatalf("write api: %s", err)
	}

	cmd := exec.Command("bash", "check_trustable_frontend.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("browser-controlled identity should fail, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "Do not hardcode browser-visible user_id") {
		t.Fatalf("hardcoded identity error missing: %s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "frontend authentication identity") {
		t.Fatalf("bearer plus browser user_id error missing: %s", strings.TrimSpace(string(out)))
	}
}

func TestFrontendCheckerRejectsAsyncIdentityRedirectBeforeLoad(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	page := "import { useEffect, useState } from 'react';\n" +
		"import { Navigate } from 'react-router-dom';\n" +
		"export const Dashboard = () => {\n" +
		"  const [currentUser, setCurrentUser] = useState<User | null>(null);\n" +
		"  useEffect(() => { fetch('/api/me').then(r => r.json()).then(setCurrentUser); }, []);\n" +
		"  if (!currentUser) return <Navigate to='/login' replace />;\n" +
		"  return <p>{currentUser.email}</p>;\n};\n"
	if err := os.WriteFile(filepath.Join(srcDir, "Dashboard.tsx"), []byte(page), 0644); err != nil {
		t.Fatalf("write dashboard: %s", err)
	}

	cmd := exec.Command("bash", "check_trustable_frontend.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("premature async identity redirect should fail, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "explicit loading state") {
		t.Fatalf("unexpected frontend checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestFrontendCheckerAcceptsAsyncIdentityWithLoadingGate(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	page := "import { useEffect, useState } from 'react';\n" +
		"import { Navigate } from 'react-router-dom';\n" +
		"export const Dashboard = () => {\n" +
		"  const [currentUser, setCurrentUser] = useState<User | null>(null);\n" +
		"  const [loading, setLoading] = useState(true);\n" +
		"  useEffect(() => { fetch('/api/me').then(r => r.json()).then(setCurrentUser).finally(() => setLoading(false)); }, []);\n" +
		"  if (loading) return <p>Loading</p>;\n" +
		"  if (!currentUser) return <Navigate to='/login' replace />;\n" +
		"  return <p>{currentUser.email}</p>;\n};\n"
	if err := os.WriteFile(filepath.Join(srcDir, "Dashboard.tsx"), []byte(page), 0644); err != nil {
		t.Fatalf("write dashboard: %s", err)
	}

	cmd := exec.Command("bash", "check_trustable_frontend.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("loading-gated async identity should pass, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
}

func TestFrontendCheckerRejectsCachedUserAsAuthoritativeSession(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	page := "import { useState } from 'react';\n" +
		"export const AuthProvider = () => {\n" +
		"  const [user, setUser] = useState(() => JSON.parse(localStorage.getItem('auth_user') || 'null'));\n" +
		"  const isAuthenticated = !!user;\n" +
		"  return <button onClick={() => logout()}>Logout</button>;\n};\n"
	if err := os.WriteFile(filepath.Join(srcDir, "AuthProvider.tsx"), []byte(page), 0644); err != nil {
		t.Fatalf("write auth provider: %s", err)
	}

	cmd := exec.Command("bash", "check_trustable_frontend.sh", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("cached browser identity should not establish a session, output=%s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "cached user/profile") {
		t.Fatalf("unexpected frontend checker output: %s", strings.TrimSpace(string(out)))
	}
}

func TestFrontendCheckerAcceptsCachedProfileAfterBackendSessionValidation(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir src: %s", err)
	}
	page := "import { useEffect, useState } from 'react';\n" +
		"export const AuthProvider = () => {\n" +
		"  const [user, setUser] = useState(() => JSON.parse(localStorage.getItem('auth_user') || 'null'));\n" +
		"  const [loading, setLoading] = useState(true);\n" +
		"  useEffect(() => { fetch('/api/my/v1/auth', { method: 'POST', body: JSON.stringify({ operation: 'me' }) }).then(r => r.json()).then(setUser).finally(() => setLoading(false)); }, []);\n" +
		"  const isAuthenticated = !loading && !!user;\n" +
		"  return <button onClick={() => logout()}>Logout</button>;\n};\n"
	if err := os.WriteFile(filepath.Join(srcDir, "AuthProvider.tsx"), []byte(page), 0644); err != nil {
		t.Fatalf("write auth provider: %s", err)
	}

	cmd := exec.Command("bash", "check_trustable_frontend.sh", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("backend-validated cached profile should pass, err=%s output=%s", err, strings.TrimSpace(string(out)))
	}
}

// Without an AgentiReact() opt-in, the standard MCP config stays limited to
// the managed service and browser servers.
func TestGenerateProjectAssetsSkipsAgentiReactWithoutOptIn(t *testing.T) {
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

	if err := generateProjectAssetsForApp(app); err != nil {
		t.Fatalf("generateProjectAssetsForApp: %s", err)
	}
	data, err := os.ReadFile(filepath.Join(appDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %s", err)
	}
	var config struct {
		Servers map[string]map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse .mcp.json: %s", err)
	}
	if _, present := config.Servers["agentireact"]; present {
		t.Fatalf("agentireact should be absent without opt-in: %#v", config.Servers)
	}
}

func TestWritePiGlobalConfigWritesNativeFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)

	cfg := &trustableConfig{
		Provider: "trustable",
		BaseURL:  "https://api.example.test/v1",
		APIKey:   "aip_secret",
		Models: map[string]*ModelLimits{
			"qwen3-coder:480b": {MaxToken: 131072, MaxOutput: 32768},
			"nomic-embed-text": {Roles: []string{"embedding"}},
		},
		Pi: &piConfig{Default: "qwen3-coder:480b"},
	}
	if err := writePiGlobalConfig(cfg); err != nil {
		t.Fatalf("writePiGlobalConfig: %s", err)
	}

	var models struct {
		Providers map[string]struct {
			BaseURL string `json:"baseUrl"`
			API     string `json:"api"`
			APIKey  string `json:"apiKey"`
			Models  []struct {
				ID            string `json:"id"`
				ContextWindow int    `json:"contextWindow"`
				MaxTokens     int    `json:"maxTokens"`
			} `json:"models"`
		} `json:"providers"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %s", err)
	}
	if err := json.Unmarshal(data, &models); err != nil {
		t.Fatalf("parse models.json: %s", err)
	}
	provider := models.Providers[piTrustableProviderName]
	if provider.BaseURL != cfg.BaseURL || provider.API != "openai-completions" || provider.APIKey != piAPIKeyRef {
		t.Fatalf("unexpected Pi provider: %#v", provider)
	}
	if len(provider.Models) != 1 || provider.Models[0].ID != "qwen3-coder:480b" {
		t.Fatalf("non-coding model was not filtered: %#v", provider.Models)
	}
	if provider.Models[0].ContextWindow != 131072 || provider.Models[0].MaxTokens != 32768 {
		t.Fatalf("unexpected model limits: %#v", provider.Models[0])
	}

	for _, name := range []string{"models.json", "auth.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %s", name, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
	var settings map[string]interface{}
	data, _ = os.ReadFile(filepath.Join(dir, "settings.json"))
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %s", err)
	}
	if settings["defaultProvider"] != piTrustableProviderName || settings["defaultModel"] != "qwen3-coder:480b" {
		t.Fatalf("unexpected Pi settings: %#v", settings)
	}
	enabledModels, ok := settings["enabledModels"].([]interface{})
	if !ok || len(enabledModels) != 1 || enabledModels[0] != piTrustableProviderName+"/*" {
		t.Fatalf("Pi model scope must contain only Trustable models: %#v", settings)
	}
	var auth map[string]map[string]string
	data, _ = os.ReadFile(filepath.Join(dir, "auth.json"))
	if err := json.Unmarshal(data, &auth); err != nil {
		t.Fatalf("parse auth.json: %s", err)
	}
	if auth[piTrustableProviderName]["key"] != cfg.APIKey {
		t.Fatalf("Pi auth key was not written")
	}
}

func TestPiProviderNameForConfigMatchesEndpointOrigin(t *testing.T) {
	tests := []struct {
		name string
		cfg  *trustableConfig
		want string
	}{
		{name: "missing config", cfg: nil, want: piLocalProviderName},
		{
			name: "trustable status catalog",
			cfg:  &trustableConfig{Provider: "trustable"},
			want: piTrustableProviderName,
		},
		{
			name: "embedded ollama status catalog",
			cfg:  &trustableConfig{Provider: "ollama", BaseURL: "http://localhost:11434/v1"},
			want: piOllamaProviderName,
		},
		{
			name: "user supplied ollama host",
			cfg:  &trustableConfig{Provider: "ollama", BaseURL: "http://192.168.1.50:11434/v1"},
			want: piLocalProviderName,
		},
		{
			name: "bestia direct endpoint",
			cfg:  &trustableConfig{Provider: "bestia", BaseURL: "http://bestia:11434/v1"},
			want: piLocalProviderName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := piProviderNameForConfig(tt.cfg); got != tt.want {
				t.Fatalf("piProviderNameForConfig() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPostConfigurationWritesPiConfigOnlyAfterSuccessfulProbe(t *testing.T) {
	piDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", piDir)
	origWorkspace := WorkspaceDir
	WorkspaceDir = t.TempDir()
	t.Cleanup(func() { WorkspaceDir = origWorkspace })

	answerOK := false
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !answerOK {
			http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"hi"}}]}`)
	}))
	t.Cleanup(stub.Close)

	post := func() *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{
			"provider": "trustable",
			"base_url": %q,
			"api_key": "aip_secret",
			"models": {"qwen3-coder:480b": {"maxToken": 131072, "maxOutput": 32768}},
			"pi": {"default": "qwen3-coder:480b"}
		}`, stub.URL)
		req := httptest.NewRequest(http.MethodPost, "/api/configuration", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handleConfiguration(rec, req)
		return rec
	}

	if rec := post(); rec.Code != http.StatusOK {
		t.Fatalf("save should succeed when the probe fails, got %d: %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(piDir, "models.json")); !os.IsNotExist(err) {
		t.Fatalf("failed probe must not write Pi config, stat err=%v", err)
	}

	answerOK = true
	if rec := post(); rec.Code != http.StatusOK {
		t.Fatalf("save failed: %d: %s", rec.Code, rec.Body)
	}
	for _, name := range []string{"models.json", "settings.json", "auth.json"} {
		if _, err := os.Stat(filepath.Join(piDir, name)); err != nil {
			t.Fatalf("%s not written after successful probe: %s", name, err)
		}
	}
}

func TestGenerateProjectAssetsForTruACP(t *testing.T) {
	projectDir := t.TempDir()
	isolateOpenServerlessCheckerInstall(t)
	t.Setenv("HOME", t.TempDir())

	if err := generateProjectAssetsInDir(projectDir, map[string]interface{}{
		"redis": map[string]interface{}{
			"type":        "local",
			"command":     []string{"redis-mcp-server", "--url", "redis://local"},
			"environment": map[string]string{"REDIS_PREFIX": "demo:"},
		},
	}); err != nil {
		t.Fatalf("generateProjectAssetsInDir: %s", err)
	}

	if _, err := os.Stat(filepath.Join(projectDir, "opencode.json")); !os.IsNotExist(err) {
		t.Fatalf("opencode.json must not be generated for TruACP, stat err=%v", err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", ".openserverless-contract.md", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(projectDir, name)); err != nil {
			t.Fatalf("%s missing: %s", name, err)
		}
	}
	agents, _ := os.ReadFile(filepath.Join(projectDir, "AGENTS.md"))
	claude, _ := os.ReadFile(filepath.Join(projectDir, "CLAUDE.md"))
	if string(agents) != string(claude) || !strings.Contains(string(agents), trustableAgentsBegin) {
		t.Fatal("managed AGENTS.md/CLAUDE.md are not aligned")
	}

	var config struct {
		Servers map[string]map[string]interface{} `json:"mcpServers"`
	}
	data, _ := os.ReadFile(filepath.Join(projectDir, ".mcp.json"))
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse .mcp.json: %s", err)
	}
	for _, name := range []string{"openserverless", "browser", "redis"} {
		if _, ok := config.Servers[name]; !ok {
			t.Fatalf("%s missing from .mcp.json: %#v", name, config.Servers)
		}
		if config.Servers[name]["lifecycle"] != "eager" {
			t.Fatalf("%s must use eager MCP lifecycle: %#v", name, config.Servers[name])
		}
	}
	if config.Servers["openserverless"]["command"] != "openserverless-mcp" ||
		config.Servers["browser"]["command"] != "trustable-browser-mcp" {
		t.Fatalf("unexpected managed MCP commands: %#v", config.Servers)
	}
}
