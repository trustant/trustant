package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Environment configuration loaded from .env
var (
	WorkspaceDir       string
	OpenAIBaseUrl      string
	OpenAIApiKey       string
	OllamaEndpoint     string
	OpencodeModel      string
	OpencodeSmallModel string
)

// loadEnv reads .env from the current directory and sets the config variables,
// expanding nested environment variables.
func loadEnv() error {
	f, err := os.Open(".env")
	if err != nil {
		return fmt.Errorf("failed to open .env: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Expand environment variables in the value
		val = os.ExpandEnv(val)
		// Set in the process environment so subsequent expansions work
		os.Setenv(key, val)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read .env: %w", err)
	}

	// Set package-level variables from environment
	WorkspaceDir = os.Getenv("WORKSPACE_DIR")
	OpenAIBaseUrl = os.Getenv("OPENAI_BASE_URL")
	OpenAIApiKey = os.Getenv("OPENAI_API_KEY")
	OllamaEndpoint = os.Getenv("OLLAMA_ENDPOINT")
	OpencodeModel = os.Getenv("OPENCODE_MODEL")
	OpencodeSmallModel = os.Getenv("OPENCODE_SMALL_MODEL")

	if WorkspaceDir == "" {
		return fmt.Errorf("WORKSPACE_DIR is not set")
	}
	if OllamaEndpoint == "" {
		return fmt.Errorf("OLLAMA_ENDPOINT is not set")
	}

	return nil
}

// runPreflight executes all preflight checks before starting the server
func runPreflight() error {
	log.Println("========================================")
	log.Println("Starting preflight checks")
	log.Println("========================================")

	// Step 0: Load environment
	log.Println("[0/5] Loading environment from .env...")
	if err := loadEnv(); err != nil {
		return fmt.Errorf("failed to load environment: %w", err)
	}
	log.Printf("  WorkspaceDir:       %s", WorkspaceDir)
	log.Printf("  OllamaEndpoint:     %s", OllamaEndpoint)
	log.Printf("  OpenAIBaseUrl:      %s", OpenAIBaseUrl)
	log.Printf("  OpencodeModel:      %s", OpencodeModel)
	log.Printf("  OpencodeSmallModel: %s", OpencodeSmallModel)
	log.Println("✓ Environment loaded")

	// Step 1: Clean up PGID file if exists
	log.Println("[1/6] Checking for leftover process groups...")
	if err := cleanupPgidFile(); err != nil {
		log.Printf("Warning: PGID cleanup failed: %v", err)
	} else {
		log.Println("✓ Process group cleanup complete")
	}

	// Step 2: Clean up ports
	log.Println("[2/6] Checking ports 8910, 4096, 5173...")
	if err := cleanupPorts(); err != nil {
		log.Printf("Warning: Port cleanup failed: %v", err)
	} else {
		log.Println("✓ Port cleanup complete")
	}

	// Step 3: Check Ollama health
	log.Println("[3/6] Checking Ollama health...")
	if err := checkOllamaHealth(); err != nil {
		return fmt.Errorf("Ollama health check failed: %w", err)
	}
	log.Println("✓ Ollama is healthy")

	// Step 4: Pull models
	log.Println("[4/6] Pulling models from model.lst...")
	if err := pullModels(); err != nil {
		return fmt.Errorf("failed to pull models: %w", err)
	}
	log.Println("✓ All models pulled")

	// Step 5: Generate opencode config
	log.Println("[5/6] Generating opencode.json...")
	if err := generateOpencodeConfig(); err != nil {
		return fmt.Errorf("failed to generate opencode config: %w", err)
	}
	log.Println("✓ opencode.json generated")

	// Step 6: Ensure SSH key exists
	log.Println("[6/6] Checking SSH key...")
	if err := ensureSSHKey(); err != nil {
		return fmt.Errorf("SSH key generation failed: %w", err)
	}
	log.Println("✓ SSH key ready")

	log.Println("========================================")
	log.Println("✓ All preflight checks passed")
	log.Println("========================================")
	return nil
}

// cleanupPgidFile reads and terminates process group from WorkspaceDir/pgid file
func cleanupPgidFile() error {
	pgidPath := filepath.Join(WorkspaceDir, "pgid")

	data, err := os.ReadFile(pgidPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("  - No leftover process group file found")
			return nil
		}
		return fmt.Errorf("failed to read pgid file: %w", err)
	}

	pgidStr := strings.TrimSpace(string(data))
	pgid, err := strconv.Atoi(pgidStr)
	if err != nil {
		log.Printf("  - Warning: invalid PGID value in file: %s", pgidStr)
		os.Remove(pgidPath)
		return nil
	}

	log.Printf("  - Found leftover process group %d, terminating...", pgid)

	cmd := exec.Command("kill", "--", fmt.Sprintf("-%d", pgid))
	if err := cmd.Run(); err != nil {
		log.Printf("  - Warning: failed to kill process group %d: %v", pgid, err)
	} else {
		log.Printf("  - Terminated process group %d", pgid)
	}

	if err := os.Remove(pgidPath); err != nil {
		log.Printf("  - Warning: failed to remove PGID file: %v", err)
	} else {
		log.Println("  - Removed pgid file")
	}

	time.Sleep(500 * time.Millisecond)
	return nil
}

// cleanupPorts checks for and kills processes occupying ports 8910, 4096, and 5173
func cleanupPorts() error {
	ports := []string{"8910", "4096", "5173"}
	for _, port := range ports {
		if err := killProcessOnPort(port); err != nil {
			log.Printf("Warning: failed to clean up port %s: %v", port, err)
		}
	}
	return nil
}

// killProcessOnPort uses lsof to find and kill processes on a specific port
func killProcessOnPort(port string) error {
	cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%s", port))
	output, err := cmd.Output()
	if err != nil {
		log.Printf("  - Port %s: clear", port)
		return nil
	}

	pids := strings.Fields(strings.TrimSpace(string(output)))
	if len(pids) == 0 {
		log.Printf("  - Port %s: clear", port)
		return nil
	}

	log.Printf("  - Port %s: found %d process(es), terminating...", port, len(pids))
	for _, pid := range pids {
		killCmd := exec.Command("kill", "-9", pid)
		if err := killCmd.Run(); err != nil {
			log.Printf("  - Warning: failed to kill process %s: %v", pid, err)
		} else {
			log.Printf("  - Killed process %s on port %s", pid, port)
		}
	}

	time.Sleep(200 * time.Millisecond)
	return nil
}

// checkOllamaHealth checks that the Ollama server is responding
func checkOllamaHealth() error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(OllamaEndpoint)
	if err != nil {
		return fmt.Errorf("cannot reach Ollama at %s: %w", OllamaEndpoint, err)
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	if !strings.Contains(body, "Ollama is running") {
		return fmt.Errorf("Ollama at %s did not return expected response (got: %s)", OllamaEndpoint, body)
	}

	log.Printf("  - Ollama is running at %s", OllamaEndpoint)
	return nil
}

// modelEntry represents a line from model.lst
type modelEntry struct {
	Name        string
	ContextSize int
}

// parseModelList reads model.lst and returns the entries
func parseModelList() ([]modelEntry, error) {
	f, err := os.Open("model.lst")
	if err != nil {
		return nil, fmt.Errorf("failed to open model.lst: %w", err)
	}
	defer f.Close()

	var entries []modelEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		ctxStr := strings.TrimSpace(fields[1])
		ctx, err := parseContextSize(ctxStr)
		if err != nil {
			log.Printf("  - Warning: invalid context size for %s: %s", name, ctxStr)
			continue
		}
		entries = append(entries, modelEntry{Name: name, ContextSize: ctx})
	}
	return entries, scanner.Err()
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

// pullModels reads model.lst and pulls each model via the Ollama API
func pullModels() error {
	entries, err := parseModelList()
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 300 * time.Second}
	for _, entry := range entries {
		log.Printf("  - Pulling %s...", entry.Name)
		reqBody, _ := json.Marshal(map[string]string{"name": entry.Name})
		resp, err := client.Post(OllamaEndpoint+"/api/pull", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			return fmt.Errorf("failed to pull %s: %w", entry.Name, err)
		}
		// Read through the streaming response to completion
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("pull %s returned status %d", entry.Name, resp.StatusCode)
		}
		log.Printf("  - ✓ %s pulled", entry.Name)
	}
	return nil
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

// modelDisplayName converts a model id to a display name by splitting on "-" and ":" and uppercasing
func modelDisplayName(modelID string) string {
	// Split by both "-" and ":"
	parts := strings.FieldsFunc(modelID, func(r rune) bool {
		return r == '-' || r == ':'
	})
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
	}
	return strings.Join(parts, " ")
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

// generateOpencodeConfig creates ~/.config/opencode/opencode.json from model.lst and Ollama capabilities
func generateOpencodeConfig() error {
	entries, err := parseModelList()
	if err != nil {
		return err
	}

	// Build model configs
	models := make(map[string]interface{})
	for _, entry := range entries {
		caps, err := getModelCapabilities(entry.Name)
		if err != nil {
			log.Printf("  - Warning: could not get capabilities for %s: %v", entry.Name, err)
			continue
		}

		// Skip embedding-only models (no "completion" capability)
		if !containsCapability(caps, "completion") {
			log.Printf("  - Skipping %s (no completion capability)", entry.Name)
			continue
		}

		models[entry.Name] = map[string]interface{}{
			"name":        modelDisplayName(entry.Name),
			"tool_call":   containsCapability(caps, "tools"),
			"reasoning":   containsCapability(caps, "thinking"),
			"temperature": true,
			"limit": map[string]interface{}{
				"context": entry.ContextSize,
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
			entry.Name,
			containsCapability(caps, "tools"),
			containsCapability(caps, "thinking"),
			entry.ContextSize)
	}

	config := map[string]interface{}{
		"$schema":           "https://opencode.ai/config.json",
		"instructions":      []string{"opencode.md"},
		"enabled_providers": []string{"ollama"},
		"model":             OpencodeModel,
		"small_model":       OpencodeSmallModel,
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

// ensureSSHKey generates an ED25519 SSH key at ~/.ssh/id_trustable if it doesn't already exist
func ensureSSHKey() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	keyPath := filepath.Join(homeDir, ".ssh", "id_trustable")

	// Check if key already exists
	if _, err := os.Stat(keyPath); err == nil {
		log.Printf("  - SSH key already exists at %s", keyPath)
		return nil
	}

	// Ensure ~/.ssh directory exists
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("failed to create .ssh directory: %w", err)
	}

	log.Printf("  - Generating ED25519 SSH key at %s...", keyPath)
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "", "-C", "trustable")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ssh-keygen failed: %w\n%s", err, string(output))
	}

	log.Printf("  - ✓ SSH key generated: %s and %s.pub", keyPath, keyPath)
	return nil
}

// handleSSHKey serves the SSH public key via GET /api/sshkey
func handleSSHKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "Failed to get home directory", http.StatusInternalServerError)
		return
	}

	pubKeyPath := filepath.Join(homeDir, ".ssh", "id_trustable.pub")
	data, err := os.ReadFile(pubKeyPath)
	if err != nil {
		http.Error(w, "SSH public key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write(data)
}
