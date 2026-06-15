package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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
	// Per spec/4-launch.md the redis MCP block is just type/command/enabled/timeout
	// — no environment block and no --ssl/--cluster-mode flags.
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
	if _, ok := redis["environment"]; ok {
		t.Fatalf("redis MCP should not carry an environment block: %#v", redis)
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

// The opencode.json is a single, self-contained file generated in the app's
// workbench project folder: provider + model defaults + lsp, instructions
// pointing at the project's own opencode.md. opencode.md and tools/ are written
// alongside it. Custom providers/lsp/mcp from an existing file are preserved.
func TestGenerateOpencodeConfigInProjectDir(t *testing.T) {
	origWorkbench := WorkbenchDir
	t.Cleanup(func() { WorkbenchDir = origWorkbench })
	WorkbenchDir = t.TempDir()

	app := "demo"
	appDir := filepath.Join(WorkbenchDir, app)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir app dir: %s", err)
	}

	// Seed an existing project file with custom provider + lsp entries.
	seed := map[string]interface{}{
		"provider": map[string]interface{}{
			"custom-provider": map[string]interface{}{"options": map[string]interface{}{}},
		},
		"lsp": map[string]interface{}{
			"custom-lsp": map[string]interface{}{"command": []string{"my-lsp"}},
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
	if providers["custom-provider"] == nil {
		t.Fatalf("custom provider not preserved: %#v", providers)
	}
	lsp, ok := got["lsp"].(map[string]interface{})
	if !ok || lsp["typescript"] == nil || lsp["python"] == nil {
		t.Fatalf("generated lsp missing: %#v", got["lsp"])
	}
	if lsp["custom-lsp"] == nil {
		t.Fatalf("custom lsp not preserved: %#v", lsp)
	}

	// instructions must point at the project's own opencode.md.
	instr, ok := got["instructions"].([]interface{})
	wantMd := filepath.Join(appDir, "opencode.md")
	if !ok || len(instr) != 1 || instr[0] != wantMd {
		t.Fatalf("instructions should reference %s, got %#v", wantMd, got["instructions"])
	}

	// opencode.md must be written in the project dir, not under ~/.config.
	if _, err := os.Stat(wantMd); err != nil {
		t.Fatalf("opencode.md not written to project dir: %s", err)
	}
	// tools/ folder must exist in the project dir.
	if info, err := os.Stat(filepath.Join(appDir, "tools")); err != nil || !info.IsDir() {
		t.Fatalf("tools/ not written to project dir: %v", err)
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
