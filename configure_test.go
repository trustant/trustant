package main

import (
	"encoding/json"
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
	origPluginPath := guardrailPluginInstallPathOverride
	root := t.TempDir()
	checkerInstallPath := filepath.Join(root, "bin", "check_openserverless_actions.sh")
	openServerlessCheckerInstallPathOverride = checkerInstallPath
	frontendCheckerInstallPathOverride = filepath.Join(root, "bin", "check_trustable_frontend.sh")
	appCheckerInstallPathOverride = filepath.Join(root, "bin", "check_trustable_app.sh")
	guardrailPluginInstallPathOverride = filepath.Join(root, "config", "opencode", "plugins", "trustable-guardrails.js")
	t.Cleanup(func() {
		openServerlessCheckerInstallPathOverride = origCheckerPath
		frontendCheckerInstallPathOverride = origFrontendPath
		appCheckerInstallPathOverride = origAppPath
		guardrailPluginInstallPathOverride = origPluginPath
	})
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

func TestModelAllowedForOpenCodeBlocksNonAgentModels(t *testing.T) {
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
		if ok, reason := modelAllowedForOpenCode("bestia", model, nil); ok || reason == "" {
			t.Fatalf("%s should be blocked for OpenCode, ok=%v reason=%q", model, ok, reason)
		}
	}

	allowed := []string{
		"bestia/coding:30b",
		"qwen3.6:35b",
		"qwen3.6-27b",
		"gpt-oss-20b",
	}
	for _, model := range allowed {
		if ok, reason := modelAllowedForOpenCode("bestia", model, nil); !ok {
			t.Fatalf("%s should be allowed for OpenCode, reason=%q", model, reason)
		}
	}
}

func TestModelAllowedForOpenCodeHonorsCatalogMetadata(t *testing.T) {
	disabled := false
	if ok, reason := modelAllowedForOpenCode("trustable", "qwen3.6-27b", &ModelLimits{
		Enabled: &disabled,
		Reason:  "temporarily unavailable",
	}); ok || reason != "temporarily unavailable" {
		t.Fatalf("disabled catalog model should be blocked with reason, ok=%v reason=%q", ok, reason)
	}

	if ok, reason := modelAllowedForOpenCode("trustable", "custom-safe-model", &ModelLimits{Roles: []string{"coding"}}); !ok {
		t.Fatalf("coding role should allow model, reason=%q", reason)
	}
	if ok, reason := modelAllowedForOpenCode("trustable", "custom-vector-model", &ModelLimits{Roles: []string{"embedding"}}); ok || reason == "" {
		t.Fatalf("embedding role should block model, ok=%v reason=%q", ok, reason)
	}
}

func TestValidateOpenCodeModelSelectionRejectsDisallowedSelectedModel(t *testing.T) {
	cfg := &trustableConfig{
		Provider: "bestia",
		Models: map[string]*ModelLimits{
			"bestia/embedding:952mb": {MaxInput: 8192},
			"qwen3.6:35b":            {MaxInput: 131072},
		},
		Opencode: &opencodeConfig{Default: "bestia/embedding:952mb", Small: "qwen3.6:35b"},
	}
	err := validateOpenCodeModelSelection(cfg)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected disallowed model validation error, got %v", err)
	}
}

func TestValidateOpenCodeModelSelectionAllowsDeferredDiscovery(t *testing.T) {
	cfg := &trustableConfig{
		Provider: "bestia",
		Models:   map[string]*ModelLimits{},
		Opencode: &opencodeConfig{Default: "", Small: ""},
	}
	if err := validateOpenCodeModelSelection(cfg); err != nil {
		t.Fatalf("empty provider-choice config should be allowed before discovery: %s", err)
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

func TestMongoDBURIStaysOutOfGeneratedAppEnvFiles(t *testing.T) {
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

// The opencode.json is a single, self-contained file fully regenerated in the
// app's workbench project folder on every launch: provider + model defaults +
// lsp, instructions pointing at the project's own contract/opencode.md, the
// AGENTS.md guard that shadows CLAUDE.md, the checker script, and the
// openserverless MCP server wired in. There is no merge — any pre-existing
// content in the file (custom providers/lsp/mcp, stale managed entries) is
// discarded so the result always reflects the current config.
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
	permission, ok := got["permission"].(map[string]interface{})
	if !ok {
		t.Fatalf("generated permission config missing: %#v", got["permission"])
	}
	editPerm, ok := permission["edit"].(map[string]interface{})
	if !ok || editPerm["packages/**/__main__.py"] != "deny" || editPerm["packages/**/*.zip"] != "deny" {
		t.Fatalf("generated edit guardrails missing: %#v", permission["edit"])
	}
	bashPerm, ok := permission["bash"].(map[string]interface{})
	if !ok || bashPerm["ops action"] != "deny" || bashPerm["ops action *"] != "deny" {
		t.Fatalf("generated bash guardrails missing: %#v", permission["bash"])
	}

	// instructions must point first at the project's own OpenServerless
	// contract, then at opencode.md.
	instr, ok := got["instructions"].([]interface{})
	wantContract := filepath.Join(appDir, ".openserverless-contract.md")
	wantMd := filepath.Join(appDir, "opencode.md")
	if !ok || len(instr) != 2 || instr[0] != wantContract || instr[1] != wantMd {
		t.Fatalf("instructions should reference %s then %s, got %#v", wantContract, wantMd, got["instructions"])
	}

	// AGENTS.md, the contract, and opencode.md must be written in the project dir,
	// not under ~/.config. The checker is installed once in the configured bin
	// path and is executable; it is not duplicated into every app repo.
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
	for _, path := range []string{frontendCheckerInstallPathOverride, appCheckerInstallPathOverride} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Trustable checker not installed at %s: %s", path, err)
		}
		if info.Mode()&0111 == 0 {
			t.Fatalf("Trustable checker should be executable at %s, mode=%s", path, info.Mode())
		}
	}
	pluginData, err := os.ReadFile(guardrailPluginInstallPathOverride)
	if err != nil {
		t.Fatalf("OpenCode guardrail plugin not installed: %s", err)
	}
	if !strings.Contains(string(pluginData), "session.compacted") ||
		!strings.Contains(string(pluginData), "trustable_completion_check") {
		t.Fatalf("OpenCode guardrail plugin missing enforcement hooks")
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
	browser, ok := mcp["browser"].(map[string]interface{})
	if !ok {
		t.Fatalf("browser MCP server missing: %#v", mcp)
	}
	if cmd, ok := browser["command"].([]interface{}); !ok || len(cmd) != 1 || cmd[0] != "trustable-browser-mcp" {
		t.Fatalf("unexpected browser command: %#v", browser["command"])
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
	cBrowser, ok := claude.MCPServers["browser"]
	if !ok || cBrowser["type"] != "stdio" || cBrowser["command"] != "trustable-browser-mcp" {
		t.Fatalf("unexpected .mcp.json browser entry: %#v", cBrowser)
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
