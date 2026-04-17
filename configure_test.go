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
