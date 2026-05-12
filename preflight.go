package main

import (
	"bufio"
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
	WorkspaceDir    string
	WorkbenchDir    string
	OpenAIBaseUrl   string
	OpenAIApiKey    string
	OllamaEndpoint  string
	AIPRegisterURL  string // AIP_REGISTER_URL: registration UI base (top-up form lives at <this>/top-up)
	AIPBaseURL      string // AIP_BASE_URL:     JSON API base (status/credits/top-up endpoints sit directly under this)
	OpsSkills       string
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
	WorkbenchDir = os.Getenv("WORKBENCH_DIR")
	OpenAIBaseUrl = os.Getenv("OPENAI_BASE_URL")
	OpenAIApiKey = os.Getenv("OPENAI_API_KEY")
	OllamaEndpoint = os.Getenv("OLLAMA_ENDPOINT")
	AIPRegisterURL = strings.TrimRight(strings.TrimSpace(os.Getenv("AIP_REGISTER_URL")), "/")
	AIPBaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("AIP_BASE_URL")), "/")
	OpsSkills = os.Getenv("OPS_SKILLS")
	if OpsSkills == "" {
		OpsSkills = "trustable-ai/skills"
		os.Setenv("OPS_SKILLS", OpsSkills)
	}

	if WorkspaceDir == "" {
		return fmt.Errorf("WORKSPACE_DIR is not set")
	}
	if WorkbenchDir == "" {
		return fmt.Errorf("WORKBENCH_DIR is not set")
	}
	if OllamaEndpoint == "" {
		return fmt.Errorf("OLLAMA_ENDPOINT is not set")
	}
	if AIPRegisterURL == "" {
		return fmt.Errorf("AIP_REGISTER_URL is not set")
	}
	if AIPBaseURL == "" {
		return fmt.Errorf("AIP_BASE_URL is not set")
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
	log.Printf("  WorkbenchDir:       %s", WorkbenchDir)
	log.Printf("  OllamaEndpoint:     %s", OllamaEndpoint)
	log.Printf("  OpenAIBaseUrl:      %s", OpenAIBaseUrl)
	log.Printf("  AIPRegisterURL:     %s", AIPRegisterURL)
	log.Printf("  AIPBaseURL:         %s", AIPBaseURL)
	log.Printf("  OpsSkills:          %s", OpsSkills)
	log.Println("✓ Environment loaded")

	// Step 1: Clean up PGID file if exists
	log.Println("[1/3] Checking for leftover process groups...")
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

	// Step 2b: Migrate existing apps into workspace trustable.json
	if err := migrateToLayeredConfig(); err != nil {
		log.Printf("Warning: config migration failed: %v", err)
	}

	// Step 3: Check SSH key
	log.Println("[3/3] Checking SSH key...")
	checkSSHKey()
	if sshKeyAvailable {
		log.Println("✓ SSH key ready")
	} else {
		log.Println("⚠ SSH key missing - private repo access unavailable")
	}

	log.Println("========================================")
	log.Println("✓ All preflight checks passed")
	log.Println("========================================")
	return nil
}

// mapsEqual compares two string maps for equality
func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// migrateToLayeredConfig migrates existing apps into workspace trustable.json
func migrateToLayeredConfig() error {
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		return err
	}

	// If apps section already exists, migration is done
	if wsCfg.Apps != nil {
		return nil
	}

	wsDir := filepath.Join(WorkspaceDir, "workspace")
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		// No workspace dir yet, nothing to migrate
		return nil
	}

	log.Println("  Migrating configuration to layered format...")
	wsCfg.Apps = make(map[string]*AppConfig)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()

		app := &AppConfig{
			Development: make(map[string]string),
			Production:  make(map[string]string),
		}

		// Retrieve password from ops
		kubegetCmd := exec.Command("ops", "util", "kubeget", "whiskuser/"+name, ".spec.password")
		if output, err := kubegetCmd.Output(); err == nil {
			app.Password = strings.TrimSpace(string(output))
		} else {
			log.Printf("  Warning: could not retrieve password for %s: %v", name, err)
		}

		wsCfg.Apps[name] = app
	}

	// Strip fields from workspace config that match base (keep only overrides + apps)
	baseCfg, baseErr := loadBaseConfig()
	if baseErr == nil {
		if modelLimitsEqual(wsCfg.Models, baseCfg.Models) {
			wsCfg.Models = nil
		}
		if wsCfg.Opencode != nil && baseCfg.Opencode != nil &&
			wsCfg.Opencode.Default == baseCfg.Opencode.Default &&
			wsCfg.Opencode.Small == baseCfg.Opencode.Small {
			wsCfg.Opencode = nil
		}
	}

	log.Printf("  Migrated %d app(s) to layered config", len(wsCfg.Apps))
	return saveWorkspaceConfig(wsCfg)
}

// cleanupPgidFile reads and terminates process group from WorkbenchDir/pgid file
func cleanupPgidFile() error {
	pgidPath := filepath.Join(WorkbenchDir, "pgid")

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

// sshKeyAvailable indicates whether the SSH key was found during preflight
var sshKeyAvailable bool

// checkSSHKey ensures ~/.ssh/id_ed25519 exists, generating one if missing,
// and sets sshKeyAvailable accordingly. The key is used for publishing git pushes.
func checkSSHKey() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("  - Warning: cannot determine home directory: %v", err)
		sshKeyAvailable = false
		return
	}
	sshDir := filepath.Join(homeDir, ".ssh")
	keyPath := filepath.Join(sshDir, "id_ed25519")
	pubPath := keyPath + ".pub"

	if _, err := os.Stat(keyPath); err == nil {
		if _, err := os.Stat(pubPath); err != nil {
			if out, perr := exec.Command("ssh-keygen", "-y", "-f", keyPath).Output(); perr == nil {
				_ = os.WriteFile(pubPath, out, 0o600)
			}
		}
		log.Printf("  - SSH key found at %s", keyPath)
		sshKeyAvailable = true
		return
	}

	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		log.Printf("  - Warning: cannot create %s: %v", sshDir, err)
		sshKeyAvailable = false
		return
	}
	log.Printf("  - SSH key not found, generating new ed25519 keypair at %s", keyPath)
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "trustable", "-f", keyPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("  - Warning: ssh-keygen failed: %v: %s", err, strings.TrimSpace(string(out)))
		sshKeyAvailable = false
		return
	}
	_ = os.Chmod(keyPath, 0o600)
	_ = os.Chmod(pubPath, 0o600)
	log.Printf("  - SSH key generated at %s", keyPath)
	sshKeyAvailable = true
}

// handleSSHKey serves the SSH public key via GET /api/sshkey
func handleSSHKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !sshKeyAvailable {
		http.Error(w, "SSH public key not found", http.StatusNotFound)
		return
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "SSH public key not found", http.StatusNotFound)
		return
	}

	pubKeyPath := filepath.Join(homeDir, ".ssh", "id_ed25519.pub")
	data, err := os.ReadFile(pubKeyPath)
	if err != nil {
		http.Error(w, "SSH public key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write(data)
}
