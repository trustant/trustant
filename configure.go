package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// trustableConfig represents the structure of trustable.json
type trustableConfig struct {
	Ollama   map[string]string `json:"ollama"`
	Opencode struct {
		Default string `json:"default"`
		Small   string `json:"small"`
	} `json:"opencode"`
}

// loadTrustableConfig reads trustable.json from the workspace directory
func loadTrustableConfig() (*trustableConfig, error) {
	configPath := filepath.Join(WorkspaceDir, "trustable.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read trustable.json: %w", err)
	}
	var cfg trustableConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse trustable.json: %w", err)
	}
	return &cfg, nil
}

// parseContextSize parses a context size string like "256K" or "512"
func parseContextSize(s string) (int, error) {
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)
	if strings.HasSuffix(upper, "K") {
		val, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil {
			return 0, err
		}
		return val * 1024, nil
	}
	return strconv.Atoi(s)
}

// ollamaShowResponse is the response from /api/show
type ollamaShowResponse struct {
	Capabilities []string `json:"capabilities"`
}

// getModelCapabilities queries Ollama for a model's capabilities
func getModelCapabilities(modelName string) ([]string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	reqBody, _ := json.Marshal(map[string]string{"name": modelName})
	resp, err := client.Post(OllamaEndpoint+"/api/show", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Capabilities, nil
}

// containsCapability checks if a capability list contains a specific capability
func containsCapability(caps []string, cap string) bool {
	for _, c := range caps {
		if c == cap {
			return true
		}
	}
	return false
}

// numberWithExtPattern matches a number followed by a size suffix like "480b", "1.7b", "123b"
var numberWithExtPattern = regexp.MustCompile(`^(\d+\.?\d*[a-zA-Z]+)$`)

// modelDisplayName converts a model id to a display name
// Split on "-" and ":", capitalize each part, put numbers with extensions in parentheses
// Example: qwen3-coder:480b-cloud => Qwen3 Coder (480B) Cloud
func modelDisplayName(modelID string) string {
	parts := strings.FieldsFunc(modelID, func(r rune) bool {
		return r == '-' || r == ':'
	})
	var result []string
	for _, p := range parts {
		if numberWithExtPattern.MatchString(p) {
			result = append(result, "("+strings.ToUpper(p)+")")
		} else {
			// Capitalize first letter
			runes := []rune(p)
			if len(runes) > 0 {
				runes[0] = unicode.ToUpper(runes[0])
			}
			result = append(result, string(runes))
		}
	}
	return strings.Join(result, " ")
}

// handleConfigure handles GET /api/configure - pulls models and generates opencode config, streaming progress
func handleConfigure(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set up streaming response
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	sendMsg := func(msg string) {
		fmt.Fprintf(w, "%s\n", msg)
		flusher.Flush()
	}

	// Load trustable.json config
	cfg, err := loadTrustableConfig()
	if err != nil {
		sendMsg("ERROR: " + err.Error())
		return
	}

	// Pull each model
	client := &http.Client{Timeout: 600 * time.Second}
	for modelName := range cfg.Ollama {
		sendMsg("Pulling model " + modelName)
		log.Printf("  - Pulling %s...", modelName)

		reqBody, _ := json.Marshal(map[string]string{"name": modelName})
		resp, err := client.Post(OllamaEndpoint+"/api/pull", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			sendMsg("ERROR: Failed to pull " + modelName + ": " + err.Error())
			return
		}
		// Read through the streaming response to completion
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			sendMsg(fmt.Sprintf("ERROR: Pull %s returned status %d", modelName, resp.StatusCode))
			return
		}
		sendMsg("OK: " + modelName + " pulled")
		log.Printf("  - ✓ %s pulled", modelName)
	}

	// Generate opencode config
	sendMsg("Generating opencode configuration...")
	if err := generateOpencodeConfig(cfg); err != nil {
		sendMsg("ERROR: " + err.Error())
		return
	}
	sendMsg("OK: opencode.json generated")
	sendMsg("DONE")
}

// generateOpencodeConfig creates ~/.config/opencode/opencode.json from trustable.json config
func generateOpencodeConfig(cfg *trustableConfig) error {
	// Build model configs
	models := make(map[string]interface{})
	for modelName, ctxStr := range cfg.Ollama {
		ctxSize, err := parseContextSize(ctxStr)
		if err != nil {
			log.Printf("  - Warning: invalid context size for %s: %s", modelName, ctxStr)
			continue
		}

		caps, err := getModelCapabilities(modelName)
		if err != nil {
			log.Printf("  - Warning: could not get capabilities for %s: %v", modelName, err)
			continue
		}

		// Skip embedding-only models (no "completion" capability)
		if !containsCapability(caps, "completion") {
			log.Printf("  - Skipping %s (no completion capability)", modelName)
			continue
		}

		models[modelName] = map[string]interface{}{
			"name":        modelDisplayName(modelName),
			"tool_call":   containsCapability(caps, "tools"),
			"reasoning":   containsCapability(caps, "thinking"),
			"temperature": true,
			"limit": map[string]interface{}{
				"context": ctxSize,
				"output":  32768,
			},
			"options": map[string]interface{}{
				"maxTokens": 8192,
			},
			"variants": map[string]interface{}{
				"fast": map[string]interface{}{
					"options": map[string]interface{}{"maxTokens": 2048},
				},
				"deep": map[string]interface{}{
					"options": map[string]interface{}{"maxTokens": 16000},
				},
				"disabled_variant": map[string]interface{}{
					"disabled": true,
				},
			},
		}

		log.Printf("  - %s: tool_call=%v reasoning=%v context=%d",
			modelName,
			containsCapability(caps, "tools"),
			containsCapability(caps, "thinking"),
			ctxSize)
	}

	config := map[string]interface{}{
		"$schema":           "https://opencode.ai/config.json",
		"instructions":      []string{"opencode.md"},
		"enabled_providers": []string{"ollama"},
		"model":             cfg.Opencode.Default,
		"small_model":       cfg.Opencode.Small,
		"provider": map[string]interface{}{
			"ollama": map[string]interface{}{
				"npm": "@ai-sdk/openai-compatible",
				"options": map[string]interface{}{
					"baseURL": OpenAIBaseUrl,
					"apiKey":  OpenAIApiKey,
				},
				"models": models,
			},
		},
	}

	// Write to ~/.config/opencode/opencode.json
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, "opencode.json")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", configPath, err)
	}

	log.Printf("  - Written to %s", configPath)
	return nil
}

// handleConfiguration handles GET and POST /api/configuration
func handleConfiguration(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		handleGetConfiguration(w, r)
	case http.MethodPost:
		handlePostConfiguration(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetConfiguration returns the current trustable.json from the workspace
func handleGetConfiguration(w http.ResponseWriter, r *http.Request) {
	configPath := filepath.Join(WorkspaceDir, "trustable.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		http.Error(w, "Failed to read configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// handlePostConfiguration saves the provided configuration to trustable.json
func handlePostConfiguration(w http.ResponseWriter, r *http.Request) {
	// Read and validate JSON
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Validate it's valid JSON and matches expected structure
	var cfg trustableConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Pretty-print and write
	formatted, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		http.Error(w, "Failed to format configuration", http.StatusInternalServerError)
		return
	}

	configPath := filepath.Join(WorkspaceDir, "trustable.json")
	if err := os.WriteFile(configPath, formatted, 0644); err != nil {
		http.Error(w, "Failed to save configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}
