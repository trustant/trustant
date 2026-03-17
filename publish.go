package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

func handlePublish(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate name
	if req.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}
	if !namePattern.MatchString(req.Name) {
		http.Error(w, "Invalid name format", http.StatusBadRequest)
		return
	}

	// Check workspace folder exists
	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}

	// Check if .env.<name> exists
	envFile := filepath.Join(workspacePath, ".env."+req.Name)
	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "not available for publishing"})
		return
	}

	// Set WSK_CONFIG_FILE path
	propsFile := fmt.Sprintf("/tmp/%s.props", req.Name)

	// Execute ops ide login
	loginCmd := exec.Command("ops", "ide", "login")
	loginCmd.Dir = workspacePath
	loginCmd.Env = append(os.Environ(), "WSK_CONFIG_FILE="+propsFile)

	if output, err := loginCmd.CombinedOutput(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":  "ops ide login failed: " + err.Error(),
			"output": string(output),
		})
		return
	}

	// Check if props file was created
	if _, err := os.Stat(propsFile); os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "login did not create config file"})
		return
	}

	// Execute ops ide deploy
	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workspacePath
	deployCmd.Env = append(os.Environ(), "WSK_CONFIG_FILE="+propsFile)

	output, err := deployCmd.CombinedOutput()

	// Clean up props file
	os.Remove(propsFile)

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":  "ops ide deploy failed: " + err.Error(),
			"output": string(output),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "publishing ok"})
}
