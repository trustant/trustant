package main

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed web
var embeddedWeb embed.FS

type Application struct {
	Name string `json:"name"`
	Repo string `json:"repo"`
}

// Validation patterns
var (
	namePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]{5,19}$`)
	repoPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)
)

func generatePassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b), nil
}

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
	entries, err := os.ReadDir("workspace")
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
		gitConfigPath := filepath.Join("workspace", name, ".git", "config")

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

		apps = append(apps, Application{
			Name: name,
			Repo: repo,
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

func handlePostRepo(w http.ResponseWriter, r *http.Request) {
	var app Application
	if err := json.NewDecoder(r.Body).Decode(&app); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate name: alphanumeric, starts with letter, 6-20 characters
	if !namePattern.MatchString(app.Name) {
		http.Error(w, "Name must be alphanumeric, start with a letter, and be 6-20 characters long", http.StatusBadRequest)
		return
	}

	// Validate repo format: user/path
	if !repoPattern.MatchString(app.Repo) {
		http.Error(w, "Repo must be in format user/path", http.StatusBadRequest)
		return
	}

	// Check if user already exists
	existingUsers, err := getExistingUsers()
	if err != nil {
		log.Printf("Warning: could not list existing users: %v", err)
		// Continue anyway - the adduser command will fail if user exists
	} else if existingUsers[app.Name] {
		http.Error(w, "User already exists", http.StatusConflict)
		return
	}

	// Check if workspace folder already exists
	workspacePath := filepath.Join("workspace", app.Name)
	if _, err := os.Stat(workspacePath); err == nil {
		http.Error(w, "Workspace folder already exists", http.StatusConflict)
		return
	}

	// Generate random password
	password, err := generatePassword(8)
	if err != nil {
		http.Error(w, "Failed to generate password", http.StatusInternalServerError)
		return
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

	// Store password
	passwordFile := filepath.Join(opsDir, app.Name+".password")
	if err := os.WriteFile(passwordFile, []byte(password), 0600); err != nil {
		http.Error(w, "Failed to store password", http.StatusInternalServerError)
		return
	}

	// Create user with ops admin adduser
	email := app.Name + "@n7s.co"
	addUserCmd := exec.Command("ops", "admin", "adduser", app.Name, email, password)
	if output, err := addUserCmd.CombinedOutput(); err != nil {
		// Clean up password file
		os.Remove(passwordFile)
		log.Printf("Failed to add user: %s, output: %s", err, string(output))
		http.Error(w, fmt.Sprintf("Failed to create user: %s", string(output)), http.StatusInternalServerError)
		return
	}

	// Clone the repo
	repoURL := fmt.Sprintf("git@github.com:%s", app.Repo)
	cloneCmd := exec.Command("git", "clone", repoURL, workspacePath)
	if output, err := cloneCmd.CombinedOutput(); err != nil {
		// Clean up: delete user and password file
		deleteUserCmd := exec.Command("ops", "admin", "delete", app.Name)
		deleteUserCmd.Run()
		os.Remove(passwordFile)
		log.Printf("Failed to clone repo: %s, output: %s", err, string(output))
		http.Error(w, fmt.Sprintf("Failed to clone repository: %s", string(output)), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(app)
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
	workspacePath := filepath.Join("workspace", req.Name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		http.Error(w, "Application not found", http.StatusNotFound)
		return
	}

	// Delete user with ops admin delete
	deleteUserCmd := exec.Command("ops", "admin", "delete", req.Name)
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

func main() {
	// Ensure workspace directory exists
	if err := os.MkdirAll("workspace", 0755); err != nil {
		log.Printf("Warning: failed to create workspace directory: %s", err)
	}

	// API routes
	http.HandleFunc("/api/repo", handleRepo)

	// Static file serving
	if _, err := os.Stat("web"); err == nil {
		// Serve from disk (development mode)
		log.Println("Serving from disk: ./web")
		http.Handle("/", http.FileServer(http.Dir("web")))
	} else {
		// Serve from embedded filesystem
		log.Println("Serving from embedded filesystem")
		webFS, err := fs.Sub(embeddedWeb, "web")
		if err != nil {
			log.Fatal(err)
		}
		http.Handle("/", http.FileServer(http.FS(webFS)))
	}

	log.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}
