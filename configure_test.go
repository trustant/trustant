package main

import "testing"

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

func TestDefaultOpenCodeMCPConfigUsesAppEnv(t *testing.T) {
	mcp := defaultOpenCodeMCPConfig(map[string]string{
		"POSTGRES_URL":  "postgres://user:pass@postgres/db",
		"REDIS_HOST":    "redis",
		"MILVUS_HOST":   "milvus",
		"S3_ACCESS_KEY": "key",
		"S3_SECRET_KEY": "secret",
		"S3_HOST":       "minio",
		"S3_PORT":       "9000",
	})

	postgres := mcp["postgres"].(map[string]interface{})
	if postgres["enabled"] != true {
		t.Fatalf("postgres MCP should be enabled: %#v", postgres)
	}
	postgresEnv := postgres["environment"].(map[string]string)
	if postgresEnv["DATABASE_URI"] != "postgres://user:pass@postgres/db" {
		t.Fatalf("postgres MCP should normalize POSTGRES_URL to DATABASE_URI: %#v", postgresEnv)
	}

	for _, name := range []string{"redis", "milvus", "s3"} {
		server := mcp[name].(map[string]interface{})
		if server["enabled"] != true {
			t.Fatalf("%s MCP should be enabled: %#v", name, server)
		}
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
