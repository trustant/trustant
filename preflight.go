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
	WorkspaceDir string
	WorkbenchDir string
	// OPENAI_BASE_URL / OPENAI_API_KEY are deliberately not mirrored into globals:
	// the provider base URL comes from cfg.BaseURL in the layered trustable.json,
	// and the real key is resolved by Pi through auth.json from the literal
	// "$OPENAI_API_KEY" reference. Both still reach subprocesses via the process
	// environment that loadEnv sets, and are stripped from the user-visible shell
	// by terminalEnvironment.
	OllamaEndpoint string
	AIPRegisterURL string // AIP_REGISTER_URL: registration UI base (top-up form lives at <this>/top-up)
	AIPBaseURL     string // AIP_BASE_URL:     JSON API base (status/credits/top-up endpoints sit directly under this)
	OpsSkills      string
	// Feature flags. Both default to OFF: an empty or unset variable disables
	// the feature, so a plain .env (see .env.dist) runs without the license
	// gate and without the Sovereign AI provider. The distribution image
	// enables them explicitly in image/env.
	EnableLicense bool // ENABLE_LICENSE: gate git push / publishing on a valid license
	EnableRegolo  bool // ENABLE_REGOLO:  offer the Sovereign AI (Regolo.AI) provider
)

// envFlag reports whether an environment variable is set to a non-empty,
// non-false value. Anything unset, empty, "0", "false", "no" or "off" is off.
func envFlag(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

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
		log.Printf("  env: %s=%s", key, safeEnvLogValue(key, val))
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read .env: %w", err)
	}

	// Set package-level variables from environment
	WorkspaceDir = os.Getenv("WORKSPACE_DIR")
	WorkbenchDir = os.Getenv("WORKBENCH_DIR")
	OllamaEndpoint = os.Getenv("OLLAMA_ENDPOINT")
	AIPRegisterURL = strings.TrimRight(strings.TrimSpace(os.Getenv("AIP_REGISTER_URL")), "/")
	AIPBaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("AIP_BASE_URL")), "/")
	OpsSkills = os.Getenv("OPS_SKILLS")
	EnableLicense = envFlag("ENABLE_LICENSE")
	EnableRegolo = envFlag("ENABLE_REGOLO")
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

// safeEnvLogValue prevents server-side credentials from being copied into the
// Trustable/Air/pod log. NOTEBOOK_GITHUB_TOKEN is covered by TOKEN; the broader
// rule also fixes the same exposure for provider keys and service passwords.
func safeEnvLogValue(key, value string) string {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "API_KEY"} {
		if strings.Contains(upper, marker) {
			if value == "" {
				return ""
			}
			return "<redacted>"
		}
	}
	return value
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
	log.Printf("  AIPRegisterURL:     %s", AIPRegisterURL)
	log.Printf("  AIPBaseURL:         %s", AIPBaseURL)
	log.Printf("  OpsSkills:          %s", OpsSkills)
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

	// Step 2b: Migrate existing apps into workspace trustable.json
	if err := migrateToLayeredConfig(); err != nil {
		log.Printf("Warning: config migration failed: %v", err)
	}

	// Restore Pi's managed JSON before an application can launch. WHY: the
	// workspace volume survives a pod replacement, while ~/.pi/agent itself is
	// rebuilt from the image so the pinned extension packages can be upgraded.
	if err := restorePiGlobalConfigAtStartup(); err != nil {
		log.Printf("Warning: Pi configuration restore failed: %v", err)
	} else {
		log.Println("✓ Pi configuration restore complete")
	}

	// Step 3: Seed the predefined-env palette. Optional and non-fatal: a
	// malformed .env.default must not stop the server from starting. Runs after
	// migrateToLayeredConfig, which is what guarantees a workspace
	// trustable.json exists to write into.
	log.Println("[3/4] Importing predefined environment variables...")
	if err := importPredefinedEnvDefaults(); err != nil {
		log.Printf("Warning: predefined env import failed: %v", err)
	} else {
		log.Println("✓ Predefined environment variables up to date")
	}

	// Step 4: Check SSH key
	log.Println("[4/4] Checking SSH key...")
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
		// Do not collapse pi.default during legacy layered-config migration.
		// Issue #51 has no OpenCode-to-Pi migration; the explicit Pi selection
		// must remain visible in the workspace layer that Configure owns.
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

func deriveSSHPublicKey(keyPath, pubPath string) error {
	if _, err := os.Stat(pubPath); err == nil {
		return nil
	}
	out, err := exec.Command("ssh-keygen", "-y", "-f", keyPath).Output()
	if err != nil {
		return err
	}
	return os.WriteFile(pubPath, out, 0o600)
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func ensureSSHKeyLink(linkPath, targetPath string, mode os.FileMode) error {
	if currentTarget, err := os.Readlink(linkPath); err == nil && currentTarget == targetPath {
		return nil
	}
	if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(targetPath, linkPath); err == nil {
		return nil
	}
	return copyFile(targetPath, linkPath, mode)
}

// checkSSHKey ensures ~/.ssh/id_ed25519 exists, backed by the persistent
// workspace copy, and sets sshKeyAvailable accordingly. The key is used for
// publishing git pushes.
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
	persistentDir := filepath.Join(WorkspaceDir, ".trustable", "ssh")
	persistentKeyPath := filepath.Join(persistentDir, "id_ed25519")
	persistentPubPath := persistentKeyPath + ".pub"

	if err := os.MkdirAll(persistentDir, 0o700); err != nil {
		log.Printf("  - Warning: cannot create %s: %v", persistentDir, err)
		sshKeyAvailable = false
		return
	}
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		log.Printf("  - Warning: cannot create %s: %v", sshDir, err)
		sshKeyAvailable = false
		return
	}

	if _, err := os.Stat(persistentKeyPath); os.IsNotExist(err) {
		if _, homeErr := os.Stat(keyPath); homeErr == nil {
			log.Printf("  - Migrating SSH key from %s to %s", keyPath, persistentKeyPath)
			if err := copyFile(keyPath, persistentKeyPath, 0o600); err != nil {
				log.Printf("  - Warning: failed to persist existing SSH key: %v", err)
				sshKeyAvailable = false
				return
			}
		} else {
			log.Printf("  - SSH key not found, generating persistent ed25519 keypair at %s", persistentKeyPath)
			cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "trustable", "-f", persistentKeyPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("  - Warning: ssh-keygen failed: %v: %s", err, strings.TrimSpace(string(out)))
				sshKeyAvailable = false
				return
			}
		}
	} else if err != nil {
		log.Printf("  - Warning: cannot inspect persistent SSH key: %v", err)
		sshKeyAvailable = false
		return
	}

	if err := os.Chmod(persistentKeyPath, 0o600); err != nil {
		log.Printf("  - Warning: cannot chmod persistent SSH key: %v", err)
		sshKeyAvailable = false
		return
	}
	if err := deriveSSHPublicKey(persistentKeyPath, persistentPubPath); err != nil {
		log.Printf("  - Warning: cannot derive SSH public key: %v", err)
		sshKeyAvailable = false
		return
	}
	_ = os.Chmod(persistentPubPath, 0o600)

	if err := ensureSSHKeyLink(keyPath, persistentKeyPath, 0o600); err != nil {
		log.Printf("  - Warning: cannot link SSH key into %s: %v", keyPath, err)
		sshKeyAvailable = false
		return
	}
	if err := ensureSSHKeyLink(pubPath, persistentPubPath, 0o600); err != nil {
		log.Printf("  - Warning: cannot link SSH public key into %s: %v", pubPath, err)
		sshKeyAvailable = false
		return
	}
	log.Printf("  - SSH key ready at %s (persistent: %s)", keyPath, persistentKeyPath)
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
