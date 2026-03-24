package main

import (
	"bufio"
	"bytes"
	"fmt"
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
	WorkspaceDir   string
	OpenAIBaseUrl  string
	OpenAIApiKey   string
	OllamaEndpoint string
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
		log.Printf("  env: %s=%s", key, val)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read .env: %w", err)
	}

	// Set package-level variables from environment
	WorkspaceDir = os.Getenv("WORKSPACE_DIR")
	OpenAIBaseUrl = os.Getenv("OPENAI_BASE_URL")
	OpenAIApiKey = os.Getenv("OPENAI_API_KEY")
	OllamaEndpoint = os.Getenv("OLLAMA_ENDPOINT")

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
	log.Println("✓ Environment loaded")

	// Step 1: Clean up PGID file if exists
	log.Println("[1/4] Checking for leftover process groups...")
	if err := cleanupPgidFile(); err != nil {
		log.Printf("Warning: PGID cleanup failed: %v", err)
	} else {
		log.Println("✓ Process group cleanup complete")
	}

	// Step 2: Clean up ports
	log.Println("[2/4] Checking ports 8910, 4096, 5173...")
	if err := cleanupPorts(); err != nil {
		log.Printf("Warning: Port cleanup failed: %v", err)
	} else {
		log.Println("✓ Port cleanup complete")
	}

	// Step 3: Check Ollama health
	log.Println("[3/4] Checking Ollama health...")
	if err := checkOllamaHealth(); err != nil {
		return fmt.Errorf("Ollama health check failed: %w", err)
	}
	log.Println("✓ Ollama is healthy")

	// Step 3b: Copy default trustable.json if not present in workspace
	trustableJsonDest := filepath.Join(WorkspaceDir, "trustable.json")
	if _, err := os.Stat(trustableJsonDest); os.IsNotExist(err) {
		log.Println("  - Copying default trustable.json to workspace...")
		src, err := os.ReadFile("trustable.json")
		if err != nil {
			log.Printf("Warning: failed to read trustable.json: %v", err)
		} else {
			if err := os.MkdirAll(WorkspaceDir, 0755); err != nil {
				log.Printf("Warning: failed to create workspace dir: %v", err)
			} else if err := os.WriteFile(trustableJsonDest, src, 0644); err != nil {
				log.Printf("Warning: failed to copy trustable.json: %v", err)
			} else {
				log.Println("  - ✓ trustable.json copied to workspace")
			}
		}
	}

	// Step 4: Ensure SSH key exists
	log.Println("[4/4] Checking SSH key...")
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


// ensureSSHKey generates an ED25519 SSH key at WorkspaceDir/.ssh/id_trustable if it doesn't already exist
func ensureSSHKey() error {
	sshDir := filepath.Join(WorkspaceDir, ".ssh")
	keyPath := filepath.Join(sshDir, "id_trustable")

	// Check if key already exists
	if _, err := os.Stat(keyPath); err == nil {
		log.Printf("  - SSH key already exists at %s", keyPath)
		return nil
	}

	// Ensure .ssh directory exists
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

	pubKeyPath := filepath.Join(WorkspaceDir, ".ssh", "id_trustable.pub")
	data, err := os.ReadFile(pubKeyPath)
	if err != nil {
		http.Error(w, "SSH public key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write(data)
}
