package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// handleGitStatus handles GET /api/git/status/<name>
func handleGitStatus(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/git/status/")
	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid name", http.StatusBadRequest)
		return
	}

	workbenchPath := filepath.Join(WorkbenchDir, name)
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "workbench not found"})
		return
	}

	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = workbenchPath
	output, err := cmd.Output()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	changed, added, deleted := 0, 0, 0
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if len(line) < 2 {
			continue
		}
		status := line[:2]
		if strings.Contains(status, "D") {
			deleted++
		} else if strings.Contains(status, "?") {
			added++
		} else {
			changed++
		}
	}

	clean := changed == 0 && added == 0 && deleted == 0
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"changed": changed,
		"added":   added,
		"deleted": deleted,
		"clean":   clean,
	})
}

// handleGitSave handles POST /api/git/save
func handleGitSave(w http.ResponseWriter, r *http.Request) {
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

	if req.Name == "" || !namePattern.MatchString(req.Name) {
		http.Error(w, "Invalid name", http.StatusBadRequest)
		return
	}

	workbenchPath := filepath.Join(WorkbenchDir, req.Name)
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "workbench not found"})
		return
	}

	// git add -A
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = workbenchPath
	if output, err := addCmd.CombinedOutput(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "git add failed: " + string(output)})
		return
	}

	// git status --porcelain to check if there are changes
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = workbenchPath
	statusOutput, err := statusCmd.Output()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "git status failed: " + err.Error()})
		return
	}
	if strings.TrimSpace(string(statusOutput)) == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "nothing to save"})
		return
	}

	// git commit
	commitCmd := exec.Command("git", "commit", "-m", "save from trustable")
	commitCmd.Dir = workbenchPath
	if output, err := commitCmd.CombinedOutput(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "git commit failed: " + string(output)})
		return
	}

	// git push origin
	pushCmd := exec.Command("git", "push", "origin")
	pushCmd.Dir = workbenchPath
	if output, err := pushCmd.CombinedOutput(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "git push failed: " + string(output)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "saved successfully"})
}

func handleGit(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name string `json:"name"`
		Cmd  string `json:"cmd"`
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

	// Validate command
	if req.Cmd == "" {
		http.Error(w, "Command is required", http.StatusBadRequest)
		return
	}

	// Check workspace folder exists
	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}

	// Execute git command
	args := strings.Fields(req.Cmd)
	cmd := exec.Command("git", args...)
	cmd.Dir = workspacePath

	output, err := cmd.CombinedOutput()
	if err != nil {
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
