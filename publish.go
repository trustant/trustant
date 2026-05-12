package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// handlePublish routes publish requests by URL path
func handlePublish(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch r.URL.Path {
	case "/api/publish/push":
		handlePublishPush(w, r)
	case "/api/publish/force-push":
		handlePublishForcePush(w, r)
	case "/api/publish/remote":
		handlePublishRemote(w, r)
	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

// handlePublishPush handles POST /api/publish/push
// Pushes code to a production GitHub repository. Git push is always allowed —
// publishing-auth gating applies only to /api/publish/remote (OpenServerless deploy).
func handlePublishPush(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Repo string `json:"repo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if !namePattern.MatchString(req.Name) {
		http.Error(w, "Invalid name format", http.StatusBadRequest)
		return
	}

	// Load config and optionally save repo
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		wsCfg = &trustableConfig{}
	}
	if wsCfg.Apps == nil {
		wsCfg.Apps = make(map[string]*AppConfig)
	}
	if wsCfg.Apps[req.Name] == nil {
		wsCfg.Apps[req.Name] = &AppConfig{
			Development: make(map[string]string),
			Production:  make(map[string]string),
		}
	}
	if wsCfg.Apps[req.Name].Production == nil {
		wsCfg.Apps[req.Name].Production = make(map[string]string)
	}

	// Save repo if provided
	if req.Repo != "" {
		if !repoPattern.MatchString(req.Repo) {
			http.Error(w, "Repo must be in format org/repo", http.StatusBadRequest)
			return
		}
		wsCfg.Apps[req.Name].Production["OPS_REPO"] = req.Repo
		if err := saveWorkspaceConfig(wsCfg); err != nil {
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Check if OPS_REPO is configured
	opsRepo := wsCfg.Apps[req.Name].Production["OPS_REPO"]
	if opsRepo == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"needs_config": true})
		return
	}

	// Use workspace bare repo to push
	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "App not found in workspace",
		})
		return
	}

	// Setup production remote on the bare repo
	repoURL := fmt.Sprintf("git@github.com:%s.git", opsRepo)

	// Remove existing production remote (ignore errors)
	removeCmd := exec.Command("git", "remote", "remove", "production")
	removeCmd.Dir = workspacePath
	removeCmd.Run()

	// Add production remote
	addCmd := exec.Command("git", "remote", "add", "production", repoURL)
	addCmd.Dir = workspacePath
	if output, err := addCmd.CombinedOutput(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":  "Failed to add production remote: " + err.Error(),
			"output": string(output),
		})
		return
	}

	// Push to production with SSH key
	homeDir, _ := os.UserHomeDir()
	sshKeyPath := filepath.Join(homeDir, ".ssh", "id_ed25519")
	sshCmd := fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=no", sshKeyPath)

	pushCmd := exec.Command("git", "push", "production", "main")
	pushCmd.Dir = workspacePath
	pushCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)

	output, err := pushCmd.CombinedOutput()
	if err != nil {
		log.Printf("Git push to production failed: %s, output: %s", err, string(output))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":  err.Error(),
			"output": string(output),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"output": string(output),
	})
}

// handlePublishForcePush handles POST /api/publish/force-push
// Force pushes code to the production GitHub repository. Git push is always allowed —
// publishing-auth gating applies only to /api/publish/remote (OpenServerless deploy).
func handlePublishForcePush(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if !namePattern.MatchString(req.Name) {
		http.Error(w, "Invalid name format", http.StatusBadRequest)
		return
	}

	// Load config to get OPS_REPO
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load config: " + err.Error()})
		return
	}
	if wsCfg.Apps == nil || wsCfg.Apps[req.Name] == nil || wsCfg.Apps[req.Name].Production == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "No production config found"})
		return
	}

	opsRepo := wsCfg.Apps[req.Name].Production["OPS_REPO"]
	if opsRepo == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "No production repository configured"})
		return
	}

	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "App not found in workspace"})
		return
	}

	// Setup production remote
	repoURL := fmt.Sprintf("git@github.com:%s.git", opsRepo)

	removeCmd := exec.Command("git", "remote", "remove", "production")
	removeCmd.Dir = workspacePath
	removeCmd.Run()

	addCmd := exec.Command("git", "remote", "add", "production", repoURL)
	addCmd.Dir = workspacePath
	if output, err := addCmd.CombinedOutput(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"error":  "Failed to add production remote: " + err.Error(),
			"output": string(output),
		})
		return
	}

	// Force push to production with SSH key
	homeDir, _ := os.UserHomeDir()
	sshKeyPath := filepath.Join(homeDir, ".ssh", "id_ed25519")
	sshCmd := fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=no", sshKeyPath)

	pushCmd := exec.Command("git", "push", "-f", "production", "main")
	pushCmd.Dir = workspacePath
	pushCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)

	output, err := pushCmd.CombinedOutput()
	if err != nil {
		log.Printf("Git force push to production failed: %s, output: %s", err, string(output))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":  err.Error(),
			"output": string(output),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"output": string(output),
	})
}

// handlePublishRemote handles POST /api/publish/remote
// Deploys to a production OpenServerless environment
func handlePublishRemote(w http.ResponseWriter, r *http.Request) {
	if !requirePublishingAuth(w) {
		return
	}
	var req struct {
		Name     string `json:"name"`
		ApiHost  string `json:"apihost"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if !namePattern.MatchString(req.Name) {
		http.Error(w, "Invalid name format", http.StatusBadRequest)
		return
	}

	// Load config and optionally save production values
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		wsCfg = &trustableConfig{}
	}
	if wsCfg.Apps == nil {
		wsCfg.Apps = make(map[string]*AppConfig)
	}
	if wsCfg.Apps[req.Name] == nil {
		wsCfg.Apps[req.Name] = &AppConfig{
			Development: make(map[string]string),
			Production:  make(map[string]string),
		}
	}
	if wsCfg.Apps[req.Name].Production == nil {
		wsCfg.Apps[req.Name].Production = make(map[string]string)
	}

	// Save config if provided
	configChanged := false
	if req.ApiHost != "" {
		wsCfg.Apps[req.Name].Production["OPS_APIHOST"] = req.ApiHost
		configChanged = true
	}
	if req.User != "" {
		wsCfg.Apps[req.Name].Production["OPS_USER"] = req.User
		configChanged = true
	}
	if req.Password != "" {
		wsCfg.Apps[req.Name].Production["OPS_PASSWORD"] = req.Password
		configChanged = true
	}
	if configChanged {
		if err := saveWorkspaceConfig(wsCfg); err != nil {
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Check all required production values are set
	prod := wsCfg.Apps[req.Name].Production
	if prod["OPS_APIHOST"] == "" || prod["OPS_USER"] == "" || prod["OPS_PASSWORD"] == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"needs_config": true})
		return
	}

	// Ensure workbench exists
	workbenchPath := filepath.Join(WorkbenchDir, req.Name)
	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)

	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		// Clone from workspace
		log.Printf("Cloning workbench for %s...", req.Name)
		cloneCmd := exec.Command("git", "clone", workspacePath, workbenchPath)
		if output, err := cloneCmd.CombinedOutput(); err != nil {
			log.Printf("Failed to clone workbench: %s, output: %s", err, string(output))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "Failed to clone workbench: " + string(output),
			})
			return
		}

		// Run npm install if package.json exists
		pkgPath := filepath.Join(workbenchPath, "package.json")
		if _, err := os.Stat(pkgPath); err == nil {
			npmCmd := exec.Command("npm", "install")
			npmCmd.Dir = workbenchPath
			if output, err := npmCmd.CombinedOutput(); err != nil {
				log.Printf("npm install failed: %s, output: %s", err, string(output))
			}
		}
	}

	// Generate env files (always, to ensure .env.production is current)
	if err := generateAppEnvFiles(req.Name); err != nil {
		log.Printf("Failed to generate env files: %s", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Failed to generate environment files: " + err.Error(),
		})
		return
	}

	// Run ops ide login --mode=production
	log.Printf("Running ops ide login --mode=production for %s...", req.Name)
	loginCmd := exec.Command("ops", "ide", "login", "--mode=production")
	loginCmd.Dir = workbenchPath
	if output, err := loginCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide login --mode=production failed: %s, output: %s", err, string(output))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":  "Login failed: " + err.Error(),
			"output": strings.TrimSpace(string(output)),
		})
		return
	}

	// Run ops ide deploy
	log.Printf("Running ops ide deploy for %s...", req.Name)
	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workbenchPath
	if output, err := deployCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide deploy failed: %s, output: %s", err, string(output))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":  "Deploy failed: " + err.Error(),
			"output": strings.TrimSpace(string(output)),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"output": "Published successfully",
	})
}
