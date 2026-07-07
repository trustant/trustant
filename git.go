package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type gitPullResponse struct {
	Message          string `json:"message"`
	Output           string `json:"output,omitempty"`
	Updated          bool   `json:"updated"`
	WorkbenchUpdated bool   `json:"workbench_updated"`
}

type gitStatusEntry struct {
	line string
	code string
	path string
}

var gitPullGeneratedFiles = map[string]bool{
	".mcp.json":                   true,
	".openserverless-contract.md": true,
	"opencode.md":                 true,
	"opencode.json":               true,
}

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

func ensureGitIdentity(workbenchPath string) error {
	checks := map[string]string{
		"user.email": "trustable@localhost",
		"user.name":  "Trustable",
	}
	for key, fallback := range checks {
		getCmd := exec.Command("git", "config", "--get", key)
		getCmd.Dir = workbenchPath
		if output, err := getCmd.Output(); err == nil && strings.TrimSpace(string(output)) != "" {
			continue
		}

		setCmd := exec.Command("git", "config", key, fallback)
		setCmd.Dir = workbenchPath
		if output, err := setCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git config %s failed: %s", key, string(output))
		}
	}
	return nil
}

func writeGitJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	json.NewEncoder(w).Encode(payload)
}

func gitCommand(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if homeDir, err := os.UserHomeDir(); err == nil {
		sshKeyPath := filepath.Join(homeDir, ".ssh", "id_ed25519")
		sshCmd := fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=no", sshKeyPath)
		cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	}
	return cmd
}

func runGitCommand(dir string, output *bytes.Buffer, args ...string) error {
	cmd := gitCommand(dir, args...)
	data, err := cmd.CombinedOutput()
	if output != nil && len(data) > 0 {
		fmt.Fprintf(output, "$ git %s\n%s", strings.Join(args, " "), string(data))
		if !bytes.HasSuffix(data, []byte("\n")) {
			output.WriteByte('\n')
		}
	}
	return err
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := gitCommand(dir, args...)
	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}

func gitOutputRaw(dir string, args ...string) (string, error) {
	cmd := gitCommand(dir, args...)
	output, err := cmd.Output()
	return string(output), err
}

func gitRefEquals(dir, left, right string) bool {
	leftSha, leftErr := gitOutput(dir, "rev-parse", "--verify", left)
	rightSha, rightErr := gitOutput(dir, "rev-parse", "--verify", right)
	return leftErr == nil && rightErr == nil && leftSha == rightSha
}

func gitIsAncestor(dir, ancestor, descendant string) bool {
	cmd := gitCommand(dir, "merge-base", "--is-ancestor", ancestor, descendant)
	return cmd.Run() == nil
}

func parseGitStatusEntries(status string) []gitStatusEntry {
	var entries []gitStatusEntry
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		if line == "" || len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			parts := strings.Split(path, " -> ")
			path = parts[len(parts)-1]
		}
		path = strings.Trim(path, `"`)
		entries = append(entries, gitStatusEntry{
			line: line,
			code: line[:2],
			path: path,
		})
	}
	return entries
}

func splitGitPullStatus(workbenchPath, status string) (generated []gitStatusEntry, user []gitStatusEntry) {
	for _, entry := range parseGitStatusEntries(status) {
		if isGitPullGeneratedEntry(workbenchPath, entry) {
			generated = append(generated, entry)
		} else {
			user = append(user, entry)
		}
	}
	return generated, user
}

func isGitPullGeneratedEntry(workbenchPath string, entry gitStatusEntry) bool {
	if gitPullGeneratedFiles[entry.path] {
		return true
	}
	if entry.path == "AGENTS.md" {
		return agentsHasOnlyTrustableManagedBlock(filepath.Join(workbenchPath, entry.path))
	}
	return false
}

func agentsHasOnlyTrustableManagedBlock(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	start := strings.Index(content, trustableAgentsBegin)
	end := strings.Index(content, trustableAgentsEnd)
	if start < 0 || end < start {
		return false
	}
	end += len(trustableAgentsEnd)
	return strings.TrimSpace(content[:start]) == "" && strings.TrimSpace(content[end:]) == ""
}

func cleanGeneratedGitPullStatus(workbenchPath string, output *bytes.Buffer, entries []gitStatusEntry) error {
	var staged []string
	var restore []string
	var clean []string
	for _, entry := range entries {
		if entry.code[0] != '?' && entry.code[0] != ' ' {
			staged = append(staged, entry.path)
		}
		if entry.code != "??" && entry.code[0] != 'A' {
			restore = append(restore, entry.path)
		}
		if entry.code == "??" || entry.code[0] == 'A' {
			clean = append(clean, entry.path)
		}
	}
	if len(staged) > 0 {
		args := append([]string{"restore", "--staged", "--"}, staged...)
		if err := runGitCommand(workbenchPath, output, args...); err != nil {
			return err
		}
	}
	if len(restore) > 0 {
		args := append([]string{"restore", "--"}, restore...)
		if err := runGitCommand(workbenchPath, output, args...); err != nil {
			return err
		}
	}
	if len(clean) > 0 {
		args := append([]string{"clean", "-f", "--"}, clean...)
		if err := runGitCommand(workbenchPath, output, args...); err != nil {
			return err
		}
	}
	return nil
}

func configuredProductionRepo(name string) string {
	cfg, err := loadWorkspaceConfig()
	if err != nil || cfg.Apps == nil || cfg.Apps[name] == nil || cfg.Apps[name].Production == nil {
		return ""
	}
	return cfg.Apps[name].Production["OPS_REPO"]
}

func ensureProductionRemote(workspacePath, repo string, output *bytes.Buffer) error {
	repoURL := fmt.Sprintf("git@github.com:%s.git", repo)
	removeCmd := exec.Command("git", "remote", "remove", "production")
	removeCmd.Dir = workspacePath
	removeCmd.Run()
	if err := runGitCommand(workspacePath, output, "remote", "add", "production", repoURL); err != nil {
		return fmt.Errorf("failed to configure production remote: %w", err)
	}
	return nil
}

func runOpsIdeRefresh(workbenchPath string, output *bytes.Buffer) error {
	cleanCmd := exec.Command("ops", "ide", "clean")
	cleanCmd.Dir = workbenchPath
	cleanOutput, cleanErr := cleanCmd.CombinedOutput()
	if output != nil && len(cleanOutput) > 0 {
		fmt.Fprintf(output, "$ ops ide clean\n%s", string(cleanOutput))
		if !bytes.HasSuffix(cleanOutput, []byte("\n")) {
			output.WriteByte('\n')
		}
	}
	if cleanErr != nil {
		return fmt.Errorf("ops ide clean failed: %w", cleanErr)
	}

	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workbenchPath
	deployOutput, deployErr := deployCmd.CombinedOutput()
	if output != nil && len(deployOutput) > 0 {
		fmt.Fprintf(output, "$ ops ide deploy\n%s", string(deployOutput))
		if !bytes.HasSuffix(deployOutput, []byte("\n")) {
			output.WriteByte('\n')
		}
	}
	if deployErr != nil {
		return fmt.Errorf("ops ide deploy failed: %w", deployErr)
	}
	return nil
}

// handleGitPull handles POST /api/git/pull
func handleGitPull(w http.ResponseWriter, r *http.Request) {
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

	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		writeGitJSON(w, http.StatusBadRequest, map[string]string{"error": "app not found in workspace"})
		return
	}

	workbenchPath := filepath.Join(WorkbenchDir, req.Name)
	workbenchExists := false
	if info, err := os.Stat(workbenchPath); err == nil && info.IsDir() {
		workbenchExists = true
	} else if err != nil && !os.IsNotExist(err) {
		writeGitJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var output bytes.Buffer
	remote := "origin"
	if opsRepo := configuredProductionRepo(req.Name); opsRepo != "" {
		remote = "production"
		if err := ensureProductionRemote(workspacePath, opsRepo, &output); err != nil {
			writeGitJSON(w, http.StatusInternalServerError, map[string]string{
				"error":  err.Error(),
				"output": output.String(),
			})
			return
		}
	}

	if workbenchExists {
		status, err := gitOutputRaw(workbenchPath, "status", "--porcelain")
		if err != nil {
			writeGitJSON(w, http.StatusInternalServerError, map[string]string{"error": "git status failed: " + err.Error()})
			return
		}
		if status != "" {
			generatedEntries, userEntries := splitGitPullStatus(workbenchPath, status)
			if len(userEntries) > 0 {
				var userStatus strings.Builder
				for _, entry := range userEntries {
					userStatus.WriteString(entry.line)
					userStatus.WriteByte('\n')
				}
				writeGitJSON(w, http.StatusConflict, map[string]string{
					"error":  "workbench has unsaved changes; save or revert before pulling",
					"output": strings.TrimSpace(userStatus.String()),
				})
				return
			}
			if err := cleanGeneratedGitPullStatus(workbenchPath, &output, generatedEntries); err != nil {
				writeGitJSON(w, http.StatusInternalServerError, map[string]string{
					"error":  "failed to clean generated Trustable files before pull: " + err.Error(),
					"output": output.String(),
				})
				return
			}
		}
		if err := runGitCommand(workbenchPath, &output, "fetch", "origin", "main:refs/remotes/origin/main"); err != nil {
			writeGitJSON(w, http.StatusInternalServerError, map[string]string{
				"error":  "git fetch local origin failed: " + err.Error(),
				"output": output.String(),
			})
			return
		}
		if !gitIsAncestor(workbenchPath, "HEAD", "origin/main") {
			writeGitJSON(w, http.StatusConflict, map[string]string{
				"error":  "workbench has local commits or divergent history; save/push or resolve before pulling",
				"output": output.String(),
			})
			return
		}
	}

	if err := runGitCommand(workspacePath, &output, "fetch", remote, "main"); err != nil {
		writeGitJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  fmt.Sprintf("git fetch %s failed: %s", remote, err.Error()),
			"output": output.String(),
		})
		return
	}

	updated := false
	if !gitRefEquals(workspacePath, "refs/heads/main", "FETCH_HEAD") {
		if !gitIsAncestor(workspacePath, "refs/heads/main", "FETCH_HEAD") {
			writeGitJSON(w, http.StatusConflict, map[string]string{
				"error":  "remote history diverged from the local workspace; pull requires fast-forward",
				"output": output.String(),
			})
			return
		}
		if err := runGitCommand(workspacePath, &output, "update-ref", "refs/heads/main", "FETCH_HEAD"); err != nil {
			writeGitJSON(w, http.StatusInternalServerError, map[string]string{
				"error":  "git update-ref failed: " + err.Error(),
				"output": output.String(),
			})
			return
		}
		updated = true
	}

	workbenchUpdated := false
	if workbenchExists {
		before, _ := gitOutput(workbenchPath, "rev-parse", "HEAD")
		if err := runGitCommand(workbenchPath, &output, "fetch", "origin", "main:refs/remotes/origin/main"); err != nil {
			writeGitJSON(w, http.StatusInternalServerError, map[string]string{
				"error":  "git fetch updated workspace failed: " + err.Error(),
				"output": output.String(),
			})
			return
		}
		if err := runGitCommand(workbenchPath, &output, "merge", "--ff-only", "origin/main"); err != nil {
			writeGitJSON(w, http.StatusConflict, map[string]string{
				"error":  "workbench fast-forward failed: " + err.Error(),
				"output": output.String(),
			})
			return
		}
		after, _ := gitOutput(workbenchPath, "rev-parse", "HEAD")
		workbenchUpdated = before != "" && after != "" && before != after
		if workbenchUpdated {
			if err := runOpsIdeRefresh(workbenchPath, &output); err != nil {
				writeGitJSON(w, http.StatusInternalServerError, map[string]string{
					"error":  err.Error(),
					"output": output.String(),
				})
				return
			}
		}
	}

	message := "already up to date"
	if updated || workbenchUpdated {
		message = "pulled updates successfully"
	}
	writeGitJSON(w, http.StatusOK, gitPullResponse{
		Message:          message,
		Output:           strings.TrimSpace(output.String()),
		Updated:          updated,
		WorkbenchUpdated: workbenchUpdated,
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

	if err := ensureGitIdentity(workbenchPath); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
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

	// ops ide clean
	opsCleanCmd := exec.Command("ops", "ide", "clean")
	opsCleanCmd.Dir = workbenchPath
	if output, err := opsCleanCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide clean failed: %s, output: %s", err, string(output))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "ops ide clean failed: " + string(output)})
		return
	}

	// ops ide deploy
	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workbenchPath
	if output, err := deployCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide deploy failed: %s, output: %s", err, string(output))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": "ops ide deploy failed: " + string(output)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "committed and deployed successfully"})
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

	// Run git commands in the workbench directory (working tree)
	workbenchPath := filepath.Join(WorkbenchDir, req.Name)
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		http.Error(w, "Workbench not found", http.StatusNotFound)
		return
	}

	// Execute git command
	args := strings.Fields(req.Cmd)
	cmd := exec.Command("git", args...)
	cmd.Dir = workbenchPath

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

	// After checkout, also unstage and remove untracked files
	if req.Cmd == "checkout ." {
		// Unstage any staged new files first
		resetCmd := exec.Command("git", "reset", "HEAD")
		resetCmd.Dir = workbenchPath
		if resetOutput, resetErr := resetCmd.CombinedOutput(); resetErr != nil {
			log.Printf("Warning: git reset failed: %s, output: %s", resetErr, string(resetOutput))
		} else {
			output = append(output, resetOutput...)
		}

		// Re-run checkout to revert any previously staged modifications
		recheckoutCmd := exec.Command("git", "checkout", ".")
		recheckoutCmd.Dir = workbenchPath
		if recheckoutOutput, recheckoutErr := recheckoutCmd.CombinedOutput(); recheckoutErr != nil {
			log.Printf("Warning: git checkout after reset failed: %s, output: %s", recheckoutErr, string(recheckoutOutput))
		} else {
			output = append(output, recheckoutOutput...)
		}

		cleanCmd := exec.Command("git", "clean", "-fd")
		cleanCmd.Dir = workbenchPath
		if cleanOutput, cleanErr := cleanCmd.CombinedOutput(); cleanErr != nil {
			log.Printf("Warning: git clean failed: %s, output: %s", cleanErr, string(cleanOutput))
		} else {
			output = append(output, cleanOutput...)
		}

		// Run ops ide clean after revert
		opsCleanCmd := exec.Command("ops", "ide", "clean")
		opsCleanCmd.Dir = workbenchPath
		if opsCleanOutput, opsCleanErr := opsCleanCmd.CombinedOutput(); opsCleanErr != nil {
			log.Printf("Warning: ops ide clean failed: %s, output: %s", opsCleanErr, string(opsCleanOutput))
		} else {
			output = append(output, opsCleanOutput...)
		}

		// Run ops ide deploy after revert
		opsDeployCmd := exec.Command("ops", "ide", "deploy")
		opsDeployCmd.Dir = workbenchPath
		if opsDeployOutput, opsDeployErr := opsDeployCmd.CombinedOutput(); opsDeployErr != nil {
			log.Printf("Warning: ops ide deploy failed: %s, output: %s", opsDeployErr, string(opsDeployOutput))
		} else {
			output = append(output, opsDeployOutput...)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"output": string(output),
	})
}
