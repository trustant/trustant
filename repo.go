package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Version and expiry info parsed from _build.txt
var (
	appVersion string
	appBuild   string
	appBranch  string
	appStream  string
	expiryDate time.Time
	gitBranch  = currentGitBranch
)

func currentGitBranch() string {
	output, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// parseVersion parses the embedded _build.txt content
func parseVersion(content string) {
	appVersion = ""
	appBuild = ""
	appBranch = ""
	appStream = ""
	expiryDate = time.Time{}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version:") {
			appVersion = strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		} else if strings.HasPrefix(line, "Build:") {
			appBuild = strings.TrimSpace(strings.TrimPrefix(line, "Build:"))
		} else if strings.HasPrefix(line, "Branch:") {
			appBranch = strings.TrimSpace(strings.TrimPrefix(line, "Branch:"))
		} else if strings.HasPrefix(line, "Stream:") {
			appStream = strings.TrimSpace(strings.TrimPrefix(line, "Stream:"))
		} else if strings.HasPrefix(line, "Expiry:") {
			dateStr := strings.TrimSpace(strings.TrimPrefix(line, "Expiry:"))
			t, err := time.Parse("2006/01/02", dateStr)
			if err != nil {
				log.Printf("Warning: failed to parse expiry date %q: %v", dateStr, err)
			} else {
				expiryDate = t
			}
		}
	}
	// Development builds produced by air embed the last release metadata. When
	// Branch/Stream are absent, show the mounted repository's active branch so
	// the UI identifies the source tree actually being served. Release builds
	// always carry explicit values and never need this fallback.
	if appBranch == "" {
		appBranch = gitBranch()
	}
	if appStream == "" {
		appStream = appBranch
	}
	log.Printf("Version: %s, Build: %s, Branch: %s, Stream: %s, Expiry: %s", appVersion, appBuild, appBranch, appStream, expiryDate.Format("2006/01/02"))
}

// isExpired checks if the current date is past the expiration date
func isExpired() bool {
	if expiryDate.IsZero() {
		return false
	}
	return time.Now().After(expiryDate)
}

// handleVersion handles GET /api/version
func handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if isExpired() {
		json.NewEncoder(w).Encode(map[string]interface{}{"expired": true})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{
		"version": fmt.Sprintf("Trustable %s", appVersion),
		"build":   appBuild,
		"branch":  appBranch,
		"stream":  appStream,
		"expire":  expiryDate.Format("2006/01/02"),
	})
}

// expiredGuard returns true (and writes expired JSON response) if the app is expired
func expiredGuard(w http.ResponseWriter) bool {
	if isExpired() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"expired": true})
		return true
	}
	return false
}

// Application represents a repo/application entry
type Application struct {
	Name    string `json:"name"`
	Repo    string `json:"repo"`
	ApiHost string `json:"apihost,omitempty"`
	OpsUser string `json:"opsuser,omitempty"`
	OpsRepo string `json:"opsrepo,omitempty"`
}

func developmentApplicationURL(appName string) string {
	base, err := url.Parse(developmentAPIHost())
	if err != nil || base.Scheme == "" || base.Hostname() == "" {
		return ""
	}
	host := appName + "." + base.Hostname()
	if port := base.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	base.Host = host
	base.Path = "/"
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	return base.String()
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
		// Bare repos have config directly in the repo dir
		gitConfigPath := filepath.Join(WorkspaceDir, "workspace", name, "config")

		// Check if this is a bare git repo
		if _, err := os.Stat(gitConfigPath); os.IsNotExist(err) {
			// Also check for non-bare repos (.git/config) for backward compatibility
			gitConfigPath = filepath.Join(WorkspaceDir, "workspace", name, ".git", "config")
			if _, err := os.Stat(gitConfigPath); os.IsNotExist(err) {
				continue
			}
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

		// Read production config from workspace config
		var apihost, opsuser, opsrepo string
		cfg, cfgErr := loadTrustableConfig()
		if cfgErr == nil && cfg.Apps != nil && cfg.Apps[name] != nil {
			apihost = cfg.Apps[name].Production["OPS_APIHOST"]
			opsuser = cfg.Apps[name].Production["OPS_USER"]
			opsrepo = cfg.Apps[name].Production["OPS_REPO"]
		}

		apps = append(apps, Application{
			Name:    name,
			Repo:    repo,
			ApiHost: apihost,
			OpsUser: opsuser,
			OpsRepo: opsrepo,
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

// getAppRepo returns the repo (org/name) for a given app by reading its git remote origin
func getAppRepo(name string) string {
	// Try bare repo config first
	gitConfigPath := filepath.Join(WorkspaceDir, "workspace", name, "config")
	if _, err := os.Stat(gitConfigPath); os.IsNotExist(err) {
		// Try non-bare repo
		gitConfigPath = filepath.Join(WorkspaceDir, "workspace", name, ".git", "config")
	}
	data, err := os.ReadFile(gitConfigPath)
	if err != nil {
		return ""
	}
	return parseGitRemoteOrigin(string(data))
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

func handlePostRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Repo     string `json:"repo"`
		Password string `json:"password"`
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

	// Resolve managed GitHub metadata before creating an OpenServerless user.
	// WHY: an inaccessible private repository must not leave partially-created
	// local users, and the browser must never receive a token to perform this
	// validation itself.
	var managedRepository *githubRepository
	githubCtx, githubCancel := context.WithTimeout(r.Context(), managedGitHubCommandTimeout)
	githubStatus := getManagedGitHubStatus(githubCtx)
	if githubStatus.Authenticated {
		if err := ensureManagedGitHubCredentials(); err != nil {
			githubCancel()
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		details, err := lookupManagedGitHubRepository(githubCtx, req.Repo)
		if err != nil {
			githubCancel()
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		managedRepository = &details
	}
	githubCancel()

	// Check if workspace folder already exists
	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); err == nil {
		http.Error(w, "Workspace folder already exists", http.StatusConflict)
		return
	}

	// Create or retrieve the password
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

	// Clone the repo as bare. Managed accounts use only their isolated HTTPS
	// credential helper; disconnected installations preserve SSH/public HTTPS.
	homeDir, _ := os.UserHomeDir()
	sshKeyPath := filepath.Join(homeDir, ".ssh", "id_ed25519")
	cloned := false
	cloneOutput := ""
	defaultBranch := ""
	if managedRepository != nil {
		cloneCmd := gitCommand("", "clone", "--bare", managedRepository.CloneURL, workspacePath)
		output, err := cloneCmd.CombinedOutput()
		cloneOutput = string(output)
		if err == nil {
			cloned = true
			defaultBranch = managedRepository.DefaultBranch
		}
	} else if _, err := os.Stat(sshKeyPath); err == nil {
		repoURL := fmt.Sprintf("git@github.com:%s", req.Repo)
		cloneCmd := gitCommand("", "clone", "--bare", repoURL, workspacePath)
		if output, err := cloneCmd.CombinedOutput(); err != nil {
			log.Printf("SSH clone failed, falling back to HTTPS: %s, output: %s", err, string(output))
			cloneOutput = string(output)
			os.RemoveAll(workspacePath)
		} else {
			cloned = true
		}
	}
	if !cloned && managedRepository == nil {
		repoURL := fmt.Sprintf("https://github.com/%s", req.Repo)
		cloneCmd := gitCommand("", "clone", "--bare", repoURL, workspacePath)
		if output, err := cloneCmd.CombinedOutput(); err != nil {
			cloneOutput = string(output)
		} else {
			cloned = true
		}
	}
	if !cloned {
		if !userExisted {
			deleteUserCmd := exec.Command("ops", "admin", "deleteuser", req.Name)
			deleteUserCmd.Run()
		}
		os.RemoveAll(workspacePath)
		log.Printf("Failed to clone repo: %s", strings.TrimSpace(cloneOutput))
		http.Error(w, "Failed to clone repository: "+strings.TrimSpace(cloneOutput), http.StatusInternalServerError)
		return
	}
	if defaultBranch != "" {
		headCmd := gitCommand(workspacePath, "symbolic-ref", "HEAD", "refs/heads/"+defaultBranch)
		if output, err := headCmd.CombinedOutput(); err != nil {
			if !userExisted {
				deleteUserCmd := exec.Command("ops", "admin", "deleteuser", req.Name)
				deleteUserCmd.Run()
			}
			os.RemoveAll(workspacePath)
			log.Printf("Failed to preserve repository default branch: %s", string(output))
			http.Error(w, "Failed to preserve repository default branch", http.StatusInternalServerError)
			return
		}
	}

	// Store the password and initial config in workspace trustable.json
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		wsCfg = &trustableConfig{}
	}
	if wsCfg.Apps == nil {
		wsCfg.Apps = make(map[string]*AppConfig)
	}
	wsCfg.Apps[req.Name] = &AppConfig{
		Password:    localPassword,
		Development: make(map[string]string),
		Production:  make(map[string]string),
	}
	if err := saveWorkspaceConfig(wsCfg); err != nil {
		log.Printf("Warning: failed to save password to config: %s", err)
	}

	// A cloned repo declares its required variables in .env.dist. Seed the ones we
	// cannot populate as empty entries and report them, so the UI can open the env
	// editor immediately rather than letting the user discover them at launch.
	var missingEnv []string
	if _, err := seedMissingEnvKeys(req.Name); err != nil {
		log.Printf("Warning: failed to seed env keys from .env.dist: %s", err)
	} else if missing, err := missingAppEnvKeys(req.Name); err != nil {
		log.Printf("Warning: failed to check missing env keys: %s", err)
	} else {
		missingEnv = missing
	}

	// Return the created application with optional warning
	result := map[string]interface{}{
		"name": req.Name,
		"repo": req.Repo,
	}
	if len(missingEnv) > 0 {
		result["missing_env"] = missingEnv
	}
	if defaultBranch != "" {
		result["default_branch"] = defaultBranch
		result["github_account"] = githubStatus.Login
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

	// Terminate any running processes (opencode/opsdevel) to avoid locked files
	terminateLeftoverProcesses()

	// Delete user with ops admin deleteuser
	deleteUserCmd := exec.Command("ops", "admin", "deleteuser", req.Name)
	if output, err := deleteUserCmd.CombinedOutput(); err != nil {
		log.Printf("Warning: failed to delete user: %s, output: %s", err, string(output))
		// Continue anyway to clean up the workspace
	}

	// Remove workspace folder (retry once for leftover locked files)
	if err := os.RemoveAll(workspacePath); err != nil {
		log.Printf("Warning: first remove attempt failed: %s, retrying...", err)
		time.Sleep(1 * time.Second)
		if err := os.RemoveAll(workspacePath); err != nil {
			log.Printf("Warning: failed to remove workspace folder: %s", err)
		}
	}

	// Also remove workbench folder if it exists
	workbenchPath := filepath.Join(WorkbenchDir, req.Name)
	if err := os.RemoveAll(workbenchPath); err != nil {
		log.Printf("Warning: failed to remove workbench folder: %s", err)
	}

	// Remove app entry from workspace config
	wsCfg, err := loadWorkspaceConfig()
	if err == nil && wsCfg.Apps != nil {
		delete(wsCfg.Apps, req.Name)
		if err := saveWorkspaceConfig(wsCfg); err != nil {
			log.Printf("Warning: failed to update config after delete: %s", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleRepo(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
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
	if expiredGuard(w) {
		return
	}
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

	// Check if workbench folder exists
	workbenchPath := filepath.Join(WorkbenchDir, name)
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
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
	uploadDir := filepath.Join(workbenchPath, "upload")
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
