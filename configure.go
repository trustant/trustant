package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// opencodeConfig holds the opencode model settings
type opencodeConfig struct {
	Default string `json:"default"`
	Small   string `json:"small"`
}

// AppConfig holds per-app configuration within trustable.json
type AppConfig struct {
	Password    string            `json:"password"`
	Development map[string]string `json:"development"`
	Production  map[string]string `json:"production"`
}

// GitConfig holds git user configuration
type GitConfig struct {
	User  string `json:"user"`
	Email string `json:"email"`
}

// trustableConfig represents the structure of trustable.json
type trustableConfig struct {
	Ollama    map[string]string    `json:"ollama,omitempty"`
	TestModel string               `json:"testmodel,omitempty"`
	Opencode  *opencodeConfig      `json:"opencode,omitempty"`
	Git       *GitConfig           `json:"git,omitempty"`
	Env       map[string]string    `json:"env,omitempty"`
	Apps      map[string]*AppConfig `json:"apps,omitempty"`
	Current   string               `json:"current,omitempty"`
}

// loadBaseConfig reads the app-root trustable.json (immutable defaults)
func loadBaseConfig() (*trustableConfig, error) {
	data, err := os.ReadFile("trustable.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read base trustable.json: %w", err)
	}
	var cfg trustableConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse base trustable.json: %w", err)
	}
	return &cfg, nil
}

// loadWorkspaceConfig reads the workspace trustable.json (overrides)
func loadWorkspaceConfig() (*trustableConfig, error) {
	configPath := filepath.Join(WorkspaceDir, "trustable.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &trustableConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read workspace trustable.json: %w", err)
	}
	var cfg trustableConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse workspace trustable.json: %w", err)
	}
	return &cfg, nil
}

// mergeConfigs merges workspace overrides onto base config.
// Non-nil/non-empty workspace fields override base fields.
// Maps are merged key-by-key (workspace keys override base keys).
func mergeConfigs(base, override *trustableConfig) *trustableConfig {
	result := *base // shallow copy

	if len(override.Ollama) > 0 {
		merged := make(map[string]string)
		for k, v := range base.Ollama {
			merged[k] = v
		}
		for k, v := range override.Ollama {
			merged[k] = v
		}
		result.Ollama = merged
	}

	if override.TestModel != "" {
		result.TestModel = override.TestModel
	}

	if override.Opencode != nil {
		result.Opencode = override.Opencode
	}

	if override.Git != nil {
		result.Git = override.Git
	}

	if len(override.Env) > 0 {
		merged := make(map[string]string)
		for k, v := range base.Env {
			merged[k] = v
		}
		for k, v := range override.Env {
			merged[k] = v
		}
		result.Env = merged
	}

	if override.Apps != nil {
		result.Apps = override.Apps
	}

	return &result
}

// loadTrustableConfig loads merged config (base + workspace overrides)
func loadTrustableConfig() (*trustableConfig, error) {
	base, err := loadBaseConfig()
	if err != nil {
		return nil, err
	}
	ws, err := loadWorkspaceConfig()
	if err != nil {
		return nil, err
	}
	return mergeConfigs(base, ws), nil
}

// saveWorkspaceConfig writes only the workspace trustable.json
func saveWorkspaceConfig(cfg *trustableConfig) error {
	formatted, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to format configuration: %w", err)
	}
	configPath := filepath.Join(WorkspaceDir, "trustable.json")
	if err := os.MkdirAll(WorkspaceDir, 0755); err != nil {
		return fmt.Errorf("failed to create workspace dir: %w", err)
	}
	return os.WriteFile(configPath, formatted, 0644)
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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("model %s: %s", modelName, strings.TrimSpace(string(body)))
	}

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

	// Step 1: Check Ollama connectivity with retries
	sendMsg("Checking Ollama connection at " + OllamaEndpoint + "...")
	ollamaOK := false
	maxAttempts := 12 // 2 minutes at 10-second intervals
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(OllamaEndpoint)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.Contains(string(body), "Ollama is running") {
				sendMsg("OK: Ollama is running")
				ollamaOK = true
				break
			}
		}
		if attempt < maxAttempts {
			sendMsg(fmt.Sprintf("Attempt %d/%d: Cannot reach Ollama at %s - retrying in 10 seconds...", attempt, maxAttempts, OllamaEndpoint))
			time.Sleep(10 * time.Second)
		} else {
			sendMsg(fmt.Sprintf("ERROR: Cannot connect to Ollama at %s after 2 minutes. Please check that Ollama is running and try again.", OllamaEndpoint))
		}
	}
	if !ollamaOK {
		return
	}

	// Step 2: Load trustable.json config
	cfg, err := loadTrustableConfig()
	if err != nil {
		sendMsg("ERROR: " + err.Error())
		return
	}

	// Step 3: Configure git user
	if cfg.Git != nil {
		if cfg.Git.User != "" {
			if err := exec.Command("git", "config", "--global", "user.name", cfg.Git.User).Run(); err != nil {
				sendMsg("ERROR: failed to set git user.name: " + err.Error())
			} else {
				sendMsg("OK: git user.name set to " + cfg.Git.User)
			}
		}
		if cfg.Git.Email != "" {
			if err := exec.Command("git", "config", "--global", "user.email", cfg.Git.Email).Run(); err != nil {
				sendMsg("ERROR: failed to set git user.email: " + err.Error())
			} else {
				sendMsg("OK: git user.email set to " + cfg.Git.Email)
			}
		}
	}

	// Step 4: Pull each model
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

	// Copy embedded tools folder to ~/.config/opencode/tools
	toolsDst := filepath.Join(os.Getenv("HOME"), ".config", "opencode", "tools")
	if err := os.MkdirAll(toolsDst, 0755); err != nil {
		sendMsg("WARNING: failed to create tools directory: " + err.Error())
	} else {
		entries, err := fs.ReadDir(embeddedTools, "tools")
		if err != nil {
			sendMsg("WARNING: failed to read embedded tools: " + err.Error())
		} else {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				data, err := fs.ReadFile(embeddedTools, "tools/"+entry.Name())
				if err != nil {
					sendMsg("WARNING: failed to read embedded tool " + entry.Name() + ": " + err.Error())
					continue
				}
				dst := filepath.Join(toolsDst, entry.Name())
				if err := os.WriteFile(dst, data, 0644); err != nil {
					sendMsg("WARNING: failed to write tool " + entry.Name() + ": " + err.Error())
				}
			}
			sendMsg("OK: opencode tools installed")
		}
	}

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

	var modelDefault, modelSmall string
	if cfg.Opencode != nil {
		modelDefault = cfg.Opencode.Default
		modelSmall = cfg.Opencode.Small
	}

	config := map[string]interface{}{
		"$schema":           "https://opencode.ai/config.json",
		"instructions":      []string{filepath.Join(os.Getenv("HOME"), ".config", "opencode", "opencode.md")},
		"enabled_providers": []string{"ollama"},
		"model":             modelDefault,
		"small_model":       modelSmall,
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

	// Write opencode.md instructions file alongside the config
	mdPath := filepath.Join(filepath.Dir(configPath), "opencode.md")
	if err := os.WriteFile(mdPath, []byte(opencodeMd), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", mdPath, err)
	}
	log.Printf("  - Written to %s", mdPath)

	return nil
}

// handleTestModel handles GET /api/testmodel - tests the OpenAI API connection
func handleTestModel(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Load config to get the testmodel
	cfg, err := loadTrustableConfig()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if cfg.TestModel == "" {
		json.NewEncoder(w).Encode(map[string]string{"error": "no testmodel defined in trustable.json"})
		return
	}

	// Call /api/generate with the testmodel asking "hello"
	log.Printf("Testing model %s...", cfg.TestModel)
	client := &http.Client{Timeout: 60 * time.Second}
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":  cfg.TestModel,
		"prompt": "hello",
		"stream": false,
	})
	resp, err := client.Post(OllamaEndpoint+"/api/generate", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		log.Printf("Test model %s error: %s", cfg.TestModel, err)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	log.Printf("Test model %s response: %s", cfg.TestModel, bodyStr)

	// Check if the response starts with {"error" or doesn't contain "response"
	if strings.HasPrefix(strings.TrimSpace(bodyStr), `{"error"`) || !strings.Contains(bodyStr, `"response"`) {
		json.NewEncoder(w).Encode(map[string]string{"error": bodyStr})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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

// handleGetConfiguration returns the merged configuration (base + workspace overrides)
func handleGetConfiguration(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadTrustableConfig()
	if err != nil {
		http.Error(w, "Failed to read configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// handlePostConfiguration saves the provided configuration to workspace trustable.json
func handlePostConfiguration(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var cfg trustableConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Preserve existing apps and current from workspace config
	wsCfg, wsErr := loadWorkspaceConfig()
	if wsErr == nil {
		if wsCfg.Apps != nil && cfg.Apps == nil {
			cfg.Apps = wsCfg.Apps
		}
		if wsCfg.Current != "" && cfg.Current == "" {
			cfg.Current = wsCfg.Current
		}
	}

	if err := saveWorkspaceConfig(&cfg); err != nil {
		http.Error(w, "Failed to save configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Regenerate .env files for all apps
	regenerateAllAppEnvFiles()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

// EnvVar represents a single environment variable with dev and prod values
type EnvVar struct {
	Name      string `json:"name"`
	DevValue  string `json:"dev_value"`
	ProdValue string `json:"prod_value"`
	Readonly  bool   `json:"readonly,omitempty"`
	Fixed     bool   `json:"fixed,omitempty"`
}

// AppEnvConfig represents the full env configuration for an app
type AppEnvConfig struct {
	Vars     []EnvVar `json:"vars"`
	LocalEnv []string `json:"localenv_keys"`
}

// parseEnvFile parses a .env file into a map
func parseEnvFile(path string) map[string]string {
	result := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

// writeEnvFile writes a map of key-value pairs to a .env file
func writeEnvFile(path string, vars map[string]string, order []string) error {
	var lines []string
	written := make(map[string]bool)
	for _, k := range order {
		if v, ok := vars[k]; ok {
			lines = append(lines, fmt.Sprintf("%s=%s", k, v))
			written[k] = true
		}
	}
	for k, v := range vars {
		if !written[k] {
			lines = append(lines, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

// generateAppEnvFiles writes .env and .env.production for an app in its workbench directory
func generateAppEnvFiles(appName string) error {
	cfg, err := loadTrustableConfig()
	if err != nil {
		return err
	}

	workbenchPath := filepath.Join(WorkbenchDir, appName)

	appCfg := cfg.Apps[appName]
	if appCfg == nil {
		appCfg = &AppConfig{
			Development: make(map[string]string),
			Production:  make(map[string]string),
		}
	}

	// Build development env
	devVars := make(map[string]string)
	devVars["OPS_USER"] = appName
	devVars["OPS_PASSWORD"] = appCfg.Password
	devVars["OPS_APIHOST"] = "http://miniops.me"
	devVars["OPS_REPO"] = getAppRepo(appName)
	devVars["OPS_SKILLS"] = OpsSkills

	// Global env defaults
	for k, v := range cfg.Env {
		devVars[k] = v
	}
	// Per-app development overrides
	for k, v := range appCfg.Development {
		devVars[k] = v
	}

	// Build production env
	prodVars := make(map[string]string)
	for k, v := range appCfg.Production {
		prodVars[k] = v
	}

	order := []string{"OPS_USER", "OPS_PASSWORD", "OPS_APIHOST", "OPS_REPO", "OPS_SKILLS"}

	envPath := filepath.Join(workbenchPath, ".env")
	if err := writeEnvFile(envPath, devVars, order); err != nil {
		return fmt.Errorf("failed to write .env: %w", err)
	}

	if len(prodVars) > 0 {
		prodPath := filepath.Join(workbenchPath, ".env.production")
		if err := writeEnvFile(prodPath, prodVars, order); err != nil {
			return fmt.Errorf("failed to write .env.production: %w", err)
		}
	}

	return nil
}

// regenerateAllAppEnvFiles regenerates .env files for all apps that have a workbench
func regenerateAllAppEnvFiles() {
	cfg, err := loadTrustableConfig()
	if err != nil {
		log.Printf("Warning: failed to load config for env regeneration: %s", err)
		return
	}
	for appName := range cfg.Apps {
		workbenchPath := filepath.Join(WorkbenchDir, appName)
		if _, err := os.Stat(workbenchPath); err == nil {
			if err := generateAppEnvFiles(appName); err != nil {
				log.Printf("Warning: failed to regenerate .env for %s: %s", appName, err)
			}
		}
	}
}

// handleAppConfig handles GET and POST /api/appconfig/<name>
func handleAppConfig(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/appconfig/")
	name = strings.TrimPrefix(name, "/api/appconfig")
	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}

	workspacePath := filepath.Join(WorkspaceDir, "workspace", name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleGetAppConfig(w, r, name, workspacePath)
	case http.MethodPost:
		handlePostAppConfig(w, r, name, workspacePath)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleGetAppConfig(w http.ResponseWriter, r *http.Request, name, workspacePath string) {
	cfg, err := loadTrustableConfig()
	if err != nil {
		http.Error(w, "Failed to load config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	appCfg := cfg.Apps[name]
	if appCfg == nil {
		appCfg = &AppConfig{
			Development: make(map[string]string),
			Production:  make(map[string]string),
		}
	}
	if appCfg.Development == nil {
		appCfg.Development = make(map[string]string)
	}
	if appCfg.Production == nil {
		appCfg.Production = make(map[string]string)
	}

	// Collect global env keys
	var envKeys []string
	if cfg.Env != nil {
		for k := range cfg.Env {
			envKeys = append(envKeys, k)
		}
	}

	fixedKeys := []string{"OPS_APIHOST", "OPS_USER", "OPS_PASSWORD", "OPS_REPO", "OPS_SKILLS"}
	var vars []EnvVar

	// Fixed rows (readonly)
	vars = append(vars, EnvVar{Name: "OPS_USER", DevValue: name, ProdValue: appCfg.Production["OPS_USER"], Readonly: true})
	vars = append(vars, EnvVar{Name: "OPS_PASSWORD", DevValue: appCfg.Password, ProdValue: appCfg.Production["OPS_PASSWORD"], Readonly: true})
	vars = append(vars, EnvVar{Name: "OPS_APIHOST", DevValue: "http://miniops.me", ProdValue: appCfg.Production["OPS_APIHOST"], Readonly: true})
	vars = append(vars, EnvVar{Name: "OPS_REPO", DevValue: getAppRepo(name), ProdValue: appCfg.Production["OPS_REPO"], Readonly: true})
	vars = append(vars, EnvVar{Name: "OPS_SKILLS", DevValue: OpsSkills, ProdValue: appCfg.Production["OPS_SKILLS"], Readonly: true})

	// Env rows from global config (fixed name, editable values)
	for _, k := range envKeys {
		devVal := appCfg.Development[k]
		if devVal == "" {
			devVal = cfg.Env[k]
		}
		vars = append(vars, EnvVar{
			Name:      k,
			DevValue:  devVal,
			ProdValue: appCfg.Production[k],
			Fixed:     true,
		})
	}

	// Custom vars (in development or production but not in fixed or env keys)
	seen := make(map[string]bool)
	for _, k := range fixedKeys {
		seen[k] = true
	}
	for _, k := range envKeys {
		seen[k] = true
	}

	for k := range appCfg.Development {
		if !seen[k] {
			vars = append(vars, EnvVar{
				Name:      k,
				DevValue:  appCfg.Development[k],
				ProdValue: appCfg.Production[k],
			})
			seen[k] = true
		}
	}
	for k := range appCfg.Production {
		if !seen[k] {
			vars = append(vars, EnvVar{
				Name:      k,
				DevValue:  appCfg.Development[k],
				ProdValue: appCfg.Production[k],
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AppEnvConfig{
		Vars:     vars,
		LocalEnv: envKeys,
	})
}

func handlePostAppConfig(w http.ResponseWriter, r *http.Request, name, workspacePath string) {
	var req struct {
		Vars []EnvVar `json:"vars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		http.Error(w, "Failed to load config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if wsCfg.Apps == nil {
		wsCfg.Apps = make(map[string]*AppConfig)
	}
	if wsCfg.Apps[name] == nil {
		wsCfg.Apps[name] = &AppConfig{}
	}

	devVars := make(map[string]string)
	prodVars := make(map[string]string)
	for _, v := range req.Vars {
		if v.Name == "" {
			continue
		}
		if v.DevValue != "" {
			devVars[v.Name] = v.DevValue
		}
		if v.ProdValue != "" {
			prodVars[v.Name] = v.ProdValue
		}
	}

	wsCfg.Apps[name].Development = devVars
	wsCfg.Apps[name].Production = prodVars

	if err := saveWorkspaceConfig(wsCfg); err != nil {
		http.Error(w, "Failed to save: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Regenerate .env files from config
	if err := generateAppEnvFiles(name); err != nil {
		log.Printf("Warning: failed to regenerate .env for %s: %s", name, err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}
