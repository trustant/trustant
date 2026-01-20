package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// OllamaConfig represents the Ollama configuration from opencode.json
type OllamaConfig struct {
	BaseURL string                       `json:"baseURL"`
	Models  map[string]map[string]interface{} `json:"models"`
}

// OpenCodeConfig represents the structure of opencode.json
type OpenCodeConfig struct {
	Provider struct {
		Ollama struct {
			Options OllamaConfig `json:"options"`
			Models  map[string]map[string]interface{} `json:"models"`
		} `json:"ollama"`
	} `json:"provider"`
}

// runPreflight executes all preflight checks before starting the server
func runPreflight() error {
	log.Println("========================================")
	log.Println("Starting preflight checks")
	log.Println("========================================")

	// Step 1: Clean up PGID file if exists
	log.Println("[1/3] Checking for leftover process groups (workgroup/pgid)...")
	if err := cleanupPgidFile(); err != nil {
		log.Printf("Warning: PGID cleanup failed: %v", err)
	} else {
		log.Println("✓ Process group cleanup complete")
	}

	// Step 2: Clean up ports
	log.Println("[2/3] Checking ports 8910, 4096, 5173...")
	if err := cleanupPorts(); err != nil {
		log.Printf("Warning: Port cleanup failed: %v", err)
	} else {
		log.Println("✓ Port cleanup complete")
	}

	// Step 3: Check Ollama health
	log.Println("[3/3] Checking Ollama health...")
	if err := checkOllamaHealth(); err != nil {
		return fmt.Errorf("Ollama health check failed: %w", err)
	}

	log.Println("========================================")
	log.Println("✓ All preflight checks passed")
	log.Println("========================================")
	return nil
}

// cleanupPgidFile reads and terminates process group from workgroup/pgid file
func cleanupPgidFile() error {
	pgidPath := "workgroup/pgid"

	// Check if file exists
	data, err := os.ReadFile(pgidPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("  - No leftover process group file found")
			return nil // File doesn't exist, nothing to do
		}
		return fmt.Errorf("failed to read pgid file: %w", err)
	}

	// Parse PGID
	pgidStr := strings.TrimSpace(string(data))
	pgid, err := strconv.Atoi(pgidStr)
	if err != nil {
		log.Printf("  - Warning: invalid PGID value in file: %s", pgidStr)
		os.Remove(pgidPath)
		return nil
	}

	log.Printf("  - Found leftover process group %d, terminating...", pgid)

	// Kill the process group
	cmd := exec.Command("kill", "--", fmt.Sprintf("-%d", pgid))
	if err := cmd.Run(); err != nil {
		log.Printf("  - Warning: failed to kill process group %d: %v", pgid, err)
	} else {
		log.Printf("  - Terminated process group %d", pgid)
	}

	// Remove the file
	if err := os.Remove(pgidPath); err != nil {
		log.Printf("  - Warning: failed to remove PGID file: %v", err)
	} else {
		log.Println("  - Removed workgroup/pgid file")
	}

	// Wait for processes to terminate
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
	// Use lsof to find processes on the port
	cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%s", port))
	output, err := cmd.Output()
	if err != nil {
		// No process found on this port (exit code 1 is normal)
		log.Printf("  - Port %s: clear", port)
		return nil
	}

	// Parse PIDs from output
	pids := strings.Fields(strings.TrimSpace(string(output)))
	if len(pids) == 0 {
		log.Printf("  - Port %s: clear", port)
		return nil
	}

	log.Printf("  - Port %s: found %d process(es), terminating...", port, len(pids))

	// Kill each process
	for _, pid := range pids {
		killCmd := exec.Command("kill", "-9", pid)
		if err := killCmd.Run(); err != nil {
			log.Printf("  - Warning: failed to kill process %s: %v", pid, err)
		} else {
			log.Printf("  - Killed process %s on port %s", pid, port)
		}
	}

	// Wait for ports to be freed
	time.Sleep(200 * time.Millisecond)

	return nil
}

// checkOllamaHealth checks the health of Ollama server and models
func checkOllamaHealth() error {
	// Read configuration
	config, err := readOpenCodeConfig()
	if err != nil {
		return fmt.Errorf("failed to read opencode.json: %w", err)
	}

	baseURL := config.Provider.Ollama.Options.BaseURL
	models := config.Provider.Ollama.Models

	// Extract base URL (remove path)
	serverURL := extractServerURL(baseURL)

	log.Printf("  - Ollama server: %s", serverURL)
	log.Printf("  - Models to check: %d", len(models))
	for modelName := range models {
		log.Printf("    • %s", modelName)
	}

	// Retry logic: try twice
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt > 1 {
			log.Println("  - Retrying Ollama health check after restart...")
		}

		// Check if Ollama is running
		log.Printf("  - [Attempt %d/2] Checking if Ollama server is running...", attempt)
		if err := checkOllamaServer(serverURL); err != nil {
			if attempt == 1 {
				log.Printf("  - Server check failed: %v", err)
				if err := restartOllama(); err != nil {
					log.Printf("  - Warning: failed to restart Ollama: %v", err)
				}
				continue
			}
			return fmt.Errorf("Ollama server is not responding: %w", err)
		}
		log.Println("  - ✓ Ollama server is running")

		// Check each model
		modelsOk := true
		for modelName := range models {
			log.Printf("  - Testing model: %s (timeout: 60s)...", modelName)
			if err := testOllamaModel(baseURL, modelName); err != nil {
				log.Printf("  - Model %s test failed: %v", modelName, err)
				modelsOk = false
				break
			}
			log.Printf("  - ✓ Model %s responded successfully", modelName)
		}

		if modelsOk {
			log.Println("  - ✓ All models are healthy")
			return nil
		}

		if attempt == 1 {
			if err := restartOllama(); err != nil {
				log.Printf("  - Warning: failed to restart Ollama: %v", err)
			}
		}
	}

	return fmt.Errorf("Ollama models are not responding after retry")
}

// readOpenCodeConfig reads and parses opencode.json
func readOpenCodeConfig() (*OpenCodeConfig, error) {
	data, err := os.ReadFile("opencode.json")
	if err != nil {
		return nil, err
	}

	var config OpenCodeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// extractServerURL removes the path from a URL, keeping only protocol://host:port
func extractServerURL(baseURL string) string {
	// Handle cases like "http://ollama:11434/v1" -> "http://ollama:11434"
	parts := strings.SplitN(baseURL, "/", 4)
	if len(parts) >= 3 {
		return strings.Join(parts[:3], "/")
	}
	return baseURL
}

// checkOllamaServer checks if Ollama server is running
func checkOllamaServer(serverURL string) error {
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(serverURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Read response body
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	// Check for "Ollama is running"
	if !strings.Contains(body, "Ollama is running") {
		return fmt.Errorf("server did not return expected response (got: %s)", body)
	}

	return nil
}

// testOllamaModel sends a simple "hello" request to test a model
func testOllamaModel(baseURL, modelName string) error {
	// Prepare the request
	reqBody := map[string]interface{}{
		"model":  modelName,
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
		},
		"stream": false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	// Create request
	url := baseURL + "/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	// Send with timeout
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check status
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("model returned status %d", resp.StatusCode)
	}

	// Try to parse response to ensure it's valid
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("invalid response from model: %w", err)
	}

	return nil
}

// restartOllama restarts the Ollama Docker container
func restartOllama() error {
	log.Println("  - Restarting Ollama Docker container...")

	cmd := exec.Command("docker", "restart", "ollama")
	if err := cmd.Run(); err != nil {
		return err
	}

	// Wait for container to start
	log.Println("  - Waiting 5 seconds for Ollama to restart...")
	time.Sleep(5 * time.Second)
	log.Println("  - ✓ Container restart command completed")

	return nil
}
