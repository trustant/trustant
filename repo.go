package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Application represents a repo/application entry
type Application struct {
	Name    string `json:"name"`
	Repo    string `json:"repo"`
	APIHost string `json:"apihost,omitempty"`
}

// Validation patterns
var (
	namePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]{5,19}$`)
	repoPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)
)

func getExistingUsers() (map[string]bool, error) {
	cmd := exec.Command("sh", "-c", "ops admin listuser | awk 'NR>1{print $1}'")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	users := make(map[string]bool)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			users[line] = true
		}
	}
	return users, nil
}

func validateAPIHost(apihost string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://%s/api/info", apihost)

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to connect to apihost: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("apihost returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read apihost response: %w", err)
	}

	var info struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return fmt.Errorf("apihost returned invalid JSON: %w", err)
	}

	if info.Description != "OpenWhisk" {
		return fmt.Errorf("apihost is not an OpenWhisk instance (description: %s)", info.Description)
	}

	return nil
}

func handleGetRepo(w http.ResponseWriter, r *http.Request) {
	apps := []Application{}

	// List folders in workspace
	entries, err := os.ReadDir(filepath.Join(WorkspaceDir, "workspace"))
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(apps)
			return
		}
		http.Error(w, "Failed to read workspace directory", http.StatusInternalServerError)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		gitConfigPath := filepath.Join(WorkspaceDir, "workspace", name, ".git", "config")

		// Check if .git exists
		if _, err := os.Stat(gitConfigPath); os.IsNotExist(err) {
			continue
		}

		// Read git config to get remote origin
		configData, err := os.ReadFile(gitConfigPath)
		if err != nil {
			continue
		}

		// Parse remote origin URL from git config
		repo := parseGitRemoteOrigin(string(configData))
		if repo == "" {
			continue
		}

		// Read apihost from .env.<name> if it exists
		var apihost string
		envNamePath := filepath.Join(WorkspaceDir, "workspace", name, ".env."+name)
		if envData, err := os.ReadFile(envNamePath); err == nil {
			apihost = parseEnvAPIHost(string(envData))
		}

		apps = append(apps, Application{
			Name:    name,
			Repo:    repo,
			APIHost: apihost,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apps)
}

func parseGitRemoteOrigin(configContent string) string {
	lines := strings.Split(configContent, "\n")
	inRemoteOrigin := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "[remote \"origin\"]" {
			inRemoteOrigin = true
			continue
		}

		if inRemoteOrigin {
			if strings.HasPrefix(line, "[") {
				break
			}

			if strings.HasPrefix(line, "url = ") {
				url := strings.TrimPrefix(line, "url = ")
				return extractRepoFromURL(url)
			}
		}
	}
	return ""
}

func extractRepoFromURL(url string) string {
	// Handle git@github.com:user/repo.git format
	if strings.HasPrefix(url, "git@") {
		parts := strings.SplitN(url, ":", 2)
		if len(parts) == 2 {
			url = parts[1]
		}
	}

	// Handle https://github.com/user/repo.git format
	url = strings.TrimPrefix(url, "https://github.com/")
	url = strings.TrimPrefix(url, "http://github.com/")

	// Remove .git suffix
	url = strings.TrimSuffix(url, ".git")

	// Extract last two parts (user/repo)
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return url
}

func parseEnvAPIHost(envContent string) string {
	lines := strings.Split(envContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "OPS_APIHOST=") {
			value := strings.TrimPrefix(line, "OPS_APIHOST=")
			// Remove https:// prefix as per spec
			value = strings.TrimPrefix(value, "https://")
			// Also remove http:// prefix for consistency
			value = strings.TrimPrefix(value, "http://")
			return value
		}
	}
	return ""
}

// calculateStreamerURL adds "stream." to the domain of the apihost URL
// Example: nuvolaris.org -> http://stream.nuvolaris.org
func calculateStreamerURL(apihost string) string {
	// Remove any protocol prefix if present
	host := strings.TrimPrefix(apihost, "https://")
	host = strings.TrimPrefix(host, "http://")

	// Add stream. prefix to the host
	return fmt.Sprintf("http://stream.%s", host)
}

func handlePostRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Repo     string `json:"repo"`
		Password string `json:"password"`
		APIHost  string `json:"apihost,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate name: alphanumeric, starts with letter, 6-20 characters
	if !namePattern.MatchString(req.Name) {
		http.Error(w, "Name must be alphanumeric, start with a letter, and be 6-20 characters long", http.StatusBadRequest)
		return
	}

	// Validate repo format: user/path
	if !repoPattern.MatchString(req.Repo) {
		http.Error(w, "Repo must be in format user/path", http.StatusBadRequest)
		return
	}

	// Validate password: required
	if req.Password == "" {
		http.Error(w, "Password is required", http.StatusBadRequest)
		return
	}

	// Check if workspace folder already exists
	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); err == nil {
		http.Error(w, "Workspace folder already exists", http.StatusConflict)
		return
	}

	// Normalize apihost: strip protocol prefix if present
	req.APIHost = strings.TrimPrefix(req.APIHost, "https://")
	req.APIHost = strings.TrimPrefix(req.APIHost, "http://")

	// Validate apihost if provided
	if req.APIHost != "" {
		if err := validateAPIHost(req.APIHost); err != nil {
			http.Error(w, fmt.Sprintf("Invalid apihost: %s", err), http.StatusBadRequest)
			return
		}
	}

	// Create ~/.ops directory if it doesn't exist
	homeDir, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "Failed to get home directory", http.StatusInternalServerError)
		return
	}
	opsDir := filepath.Join(homeDir, ".ops")
	if err := os.MkdirAll(opsDir, 0700); err != nil {
		http.Error(w, "Failed to create .ops directory", http.StatusInternalServerError)
		return
	}

	// Try to retrieve existing password with ops util kubeget
	localPassword := req.Password
	userExisted := false
	kubegetCmd := exec.Command("ops", "util", "kubeget", "whiskuser/"+req.Name, ".spec.password")
	if output, err := kubegetCmd.Output(); err == nil {
		// User exists, use the retrieved password
		localPassword = strings.TrimSpace(string(output))
		userExisted = true
		log.Printf("User %s exists, using existing password", req.Name)
	} else {
		// User doesn't exist, create the user
		email := req.Name + "@n7s.co"
		addUserCmd := exec.Command("ops", "admin", "adduser", req.Name, email, localPassword, "--all")
		if output, err := addUserCmd.CombinedOutput(); err != nil {
			log.Printf("Failed to add user: %s, output: %s", err, string(output))
			http.Error(w, fmt.Sprintf("Failed to create user: %s", string(output)), http.StatusInternalServerError)
			return
		}
	}

	// Store password
	passwordFile := filepath.Join(opsDir, req.Name+".password")
	if err := os.WriteFile(passwordFile, []byte(localPassword), 0600); err != nil {
		http.Error(w, "Failed to store password", http.StatusInternalServerError)
		return
	}

	// Clone the repo using the trustable SSH key
	repoURL := fmt.Sprintf("git@github.com:%s", req.Repo)
	homeDir2, _ := os.UserHomeDir()
	sshKeyPath := filepath.Join(homeDir2, ".ssh", "id_trustable")
	sshCmd := fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=no", sshKeyPath)
	cloneCmd := exec.Command("git", "clone", repoURL, workspacePath)
	cloneCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	if output, err := cloneCmd.CombinedOutput(); err != nil {
		// Clean up: delete user and password file
		deleteUserCmd := exec.Command("ops", "admin", "deleteuser", req.Name)
		deleteUserCmd.Run()
		os.Remove(passwordFile)
		log.Printf("Failed to clone repo: %s, output: %s", err, string(output))
		http.Error(w, fmt.Sprintf("Failed to clone repository: %s", string(output)), http.StatusInternalServerError)
		return
	}

	// Create .env file with default apihost and required environment variables
	envContent := fmt.Sprintf(`OPS_USER=%s
OPS_PASSWORD=%s
OPS_APIHOST=http://miniops.me
OLLAMA_HOST=ollama
OLLAMA_PROTO=http
OLLAMA_TOKEN=dummy
OPENAI_BASE_URL=http://ollama:11434/v1
OPENAI_API_KEY=dummy
OLLAMA_MODEL=gpt-oss:20b
VITE_STREAM=http://stream.miniops.me
`, req.Name, localPassword)
	envPath := filepath.Join(workspacePath, ".env")
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		log.Printf("Warning: failed to create .env file: %s", err)
	}

	// Create .env.<name> file if apihost is provided
	if req.APIHost != "" {
		envNameContent := fmt.Sprintf("OPS_USER=%s\nOPS_PASSWORD=%s\nOPS_APIHOST=https://%s\n", req.Name, localPassword, req.APIHost)

		// Read .env from current directory and append it
		if envData, err := os.ReadFile(".env"); err == nil {
			envNameContent += string(envData)
		} else {
			log.Printf("Warning: failed to read .env: %s", err)
		}

		envNamePath := filepath.Join(workspacePath, ".env."+req.Name)
		if err := os.WriteFile(envNamePath, []byte(envNameContent), 0600); err != nil {
			log.Printf("Warning: failed to create .env.%s file: %s", req.Name, err)
		}

		// Calculate <streamer> by adding "stream." to the domain of apihost
		streamer := calculateStreamerURL(req.APIHost)

		// Create .env.production with VITE_STREAM
		prodEnvContent := fmt.Sprintf("VITE_STREAM=%s\n", streamer)
		destProdPath := filepath.Join(workspacePath, ".env.production")
		if err := os.WriteFile(destProdPath, []byte(prodEnvContent), 0600); err != nil {
			log.Printf("Warning: failed to create .env.production: %s", err)
		}
	}

	// Run npm install if package.json exists
	packageJSONPath := filepath.Join(workspacePath, "package.json")
	if _, err := os.Stat(packageJSONPath); err == nil {
		npmCmd := exec.Command("npm", "install")
		npmCmd.Dir = workspacePath
		if output, err := npmCmd.CombinedOutput(); err != nil {
			log.Printf("Warning: npm install failed: %s, output: %s", err, string(output))
			// Don't fail the request, just log the warning
		}
	}

	// Return the created application with optional warning
	result := map[string]interface{}{
		"name":    req.Name,
		"repo":    req.Repo,
		"apihost": req.APIHost,
	}
	if userExisted {
		result["warning"] = "The provided password was ignored because the user already existed. The existing local password was reused."
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(result)
}

func handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	// Validate name format to prevent path traversal
	if !namePattern.MatchString(req.Name) {
		http.Error(w, "Invalid name format", http.StatusBadRequest)
		return
	}

	// Check if workspace folder exists
	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		http.Error(w, "Application not found", http.StatusNotFound)
		return
	}

	// Delete user with ops admin deleteuser
	deleteUserCmd := exec.Command("ops", "admin", "deleteuser", req.Name)
	if output, err := deleteUserCmd.CombinedOutput(); err != nil {
		log.Printf("Warning: failed to delete user: %s, output: %s", err, string(output))
		// Continue anyway to clean up the workspace
	}

	// Remove workspace folder
	if err := os.RemoveAll(workspacePath); err != nil {
		log.Printf("Warning: failed to remove workspace folder: %s", err)
	}

	// Remove password file
	homeDir, err := os.UserHomeDir()
	if err == nil {
		passwordFile := filepath.Join(homeDir, ".ops", req.Name+".password")
		if err := os.Remove(passwordFile); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to remove password file: %s", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleRepo(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		handleGetRepo(w, r)
	case http.MethodPost:
		handlePostRepo(w, r)
	case http.MethodDelete:
		handleDeleteRepo(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (max 32MB)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Failed to parse multipart form", http.StatusInternalServerError)
		return
	}

	// Get the name field
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "Name field is required", http.StatusBadRequest)
		return
	}

	// Validate name format to prevent path traversal
	if !namePattern.MatchString(name) {
		http.Error(w, "Invalid name format", http.StatusBadRequest)
		return
	}

	// Check if workspace folder exists
	workspacePath := filepath.Join(WorkspaceDir, "workspace", name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		http.Error(w, "Application not found", http.StatusNotFound)
		return
	}

	// Get the file from the form
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File field is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Extract filename and remove any path components
	filename := filepath.Base(header.Filename)
	if filename == "" || filename == "." || filename == ".." {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	// Create upload directory if necessary
	uploadDir := filepath.Join(workspacePath, "upload")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		http.Error(w, "Failed to create upload directory", http.StatusInternalServerError)
		return
	}

	// Create the destination file
	destPath := filepath.Join(uploadDir, filename)
	destFile, err := os.Create(destPath)
	if err != nil {
		http.Error(w, "Failed to create destination file", http.StatusInternalServerError)
		return
	}
	defer destFile.Close()

	// Copy the uploaded file to the destination
	if _, err := io.Copy(destFile, file); err != nil {
		http.Error(w, "Failed to save uploaded file", http.StatusInternalServerError)
		return
	}

	// Return the absolute path of the uploaded file
	absPath, err := filepath.Abs(destPath)
	if err != nil {
		http.Error(w, "Failed to get absolute path", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(absPath))
}
