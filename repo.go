// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
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

// appPasswordAlphabet is deliberately alphanumeric: the value is passed to
// `ops admin adduser` on a command line and stored in trustable.json, so
// shell-significant characters would only create quoting hazards.
const appPasswordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

const appPasswordLength = 20

// generateAppPassword returns a random password for a newly created
// OpenServerless user. The browser no longer supplies one — see spec/2-repo.md.
func generateAppPassword() (string, error) {
	limit := big.NewInt(int64(len(appPasswordAlphabet)))
	out := make([]byte, appPasswordLength)
	for i := range out {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[i] = appPasswordAlphabet[n.Int64()]
	}
	return string(out), nil
}

// Version and expiry info parsed from _build.txt
var (
	appVersion string
	appBuild   string
	appBranch  string
	appStream  string
	expiryDate time.Time
	gitBranch  = currentGitBranch
)

// opsInfoEntry is one `<key>: <value>` row of `ops -info`. The Configure page
// renders these as a table, so the key is display text, not an identifier —
// the first row's key is literally "OPS & OPS_CMD".
type opsInfoEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ops CLI metadata, probed once at startup by probeOpsInfo. Both are empty when
// ops is missing or the probe failed, which the UI treats as "omit".
var (
	// opsInfo is an ordered slice, deliberately not a map: Go randomizes map
	// iteration, which would shuffle the Configure table between reloads.
	opsInfo  []opsInfoEntry
	opsTasks string // OPS_OLARIS, truncated to opsTasksShortLen
)

// opsTasksShortLen is how much of the OPS_OLARIS commit hash the footer shows.
// Six characters is enough to identify which tasks are in use at a glance.
const opsTasksShortLen = 6

// parseOpsInfo turns `ops -info` output into ordered key/value pairs.
//
// Every line is `KEY: VALUE`, but three properties of the real output shape the
// parsing: the first key contains spaces and an ampersand ("OPS & OPS_CMD"), so
// keys are not identifiers; values contain their own colons (OPS_REPO is a
// URL), so only the FIRST ": " may be split on; and OPS_BRANCH is routinely
// empty, so a blank value keeps its row rather than being dropped.
func parseOpsInfo(output string) []opsInfoEntry {
	var entries []opsInfoEntry
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		entries = append(entries, opsInfoEntry{Key: key, Value: strings.TrimSpace(value)})
	}
	return entries
}

// opsInfoValue returns the value for key, or "" when absent.
func opsInfoValue(entries []opsInfoEntry, key string) string {
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Value
		}
	}
	return ""
}

// probeOpsInfo runs `ops -info` once at startup and caches the result.
//
// It shells out, so it must never run per request. Failure is non-fatal: ops
// may simply be absent, and the server has no business refusing to start over
// a diagnostic panel. The values stay empty and the UI omits them.
func probeOpsInfo() {
	output, err := exec.Command("ops", "-info").Output()
	if err != nil {
		log.Printf("Warning: ops -info failed, CLI details unavailable: %v", err)
		return
	}
	opsInfo = parseOpsInfo(string(output))
	olaris := opsInfoValue(opsInfo, "OPS_OLARIS")
	if len(olaris) > opsTasksShortLen {
		olaris = olaris[:opsTasksShortLen]
	}
	opsTasks = olaris
	log.Printf("Ops: %s, Tasks: %s", opsInfoValue(opsInfo, "OPS_VERSION"), opsTasks)
}

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
	// The two feature flags ride along here because every page already fetches
	// /api/version on boot. See spec/0-preflight.md.
	// "tasks" is the short OPS_OLARIS hash for the footer; "opsinfo" carries the
	// full parsed `ops -info` table for the Configure page. The ops version is
	// not a separate field — it is one of the opsinfo rows.
	json.NewEncoder(w).Encode(map[string]interface{}{
		"version": fmt.Sprintf("Trustable %s", appVersion),
		"build":   appBuild,
		"branch":  appBranch,
		"stream":  appStream,
		"expire":  expiryDate.Format("2006/01/02"),
		"license": EnableLicense,
		"regolo":  EnableRegolo,
		"tasks":   opsTasks,
		"opsinfo": opsInfo,
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
		Name string `json:"name"`
		Repo string `json:"repo"`
		// Password is accepted for backwards compatibility but ignored: the
		// password is either reused from an existing user or generated here.
		Password string `json:"password"`
		// Templates is the starter's notebook/templates repository. Empty means
		// the app inherits the global notebook.repository default.
		Templates string `json:"templates"`
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

	// Validate the optional starter templates repository up front, so a bad
	// value fails before any user or clone is created.
	appTemplates := ""
	if strings.TrimSpace(req.Templates) != "" {
		normalized, err := normalizeNotebookRepository(req.Templates)
		if err != nil {
			http.Error(w, "Invalid templates repository: "+err.Error(), http.StatusBadRequest)
			return
		}
		appTemplates = normalized
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

	// Create or retrieve the password. The browser never supplies one: an
	// existing OpenServerless user keeps its password, a new one gets a
	// server-generated random password.
	localPassword := ""
	userExisted := false
	kubegetCmd := exec.Command("ops", "util", "kubeget", "whiskuser/"+req.Name, ".spec.password")
	if output, err := kubegetCmd.Output(); err == nil {
		// User exists, use the retrieved password
		localPassword = strings.TrimSpace(string(output))
		userExisted = true
		log.Printf("User %s exists, using existing password", req.Name)
	} else {
		// User doesn't exist, create the user
		generated, err := generateAppPassword()
		if err != nil {
			http.Error(w, "Failed to generate password", http.StatusInternalServerError)
			return
		}
		localPassword = generated
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
		Templates:   appTemplates,
		Development: make(map[string]string),
		Production:  make(map[string]string),
	}
	if err := saveWorkspaceConfig(wsCfg); err != nil {
		log.Printf("Warning: failed to save password to config: %s", err)
	}

	// A newly installed app may itself declare shared variables. Resolve them now
	// so they are in the pool before the missing-variable check below, and so
	// another app can consume them without waiting for this one to be launched.
	// A no-op when the app has no workbench yet, which is the common case.
	func() {
		unlock := lockRuntimeLifecycle("import shared variables " + req.Name)
		defer unlock()
		if _, err := refreshSharedForApp(req.Name); err != nil {
			log.Printf("Warning: failed to resolve shared variables for %s: %s", req.Name, err)
		}
	}()

	// A cloned repo declares what it imports in .env.dist. Report the ones the pool
	// cannot resolve, so the UI can open the Import tab immediately rather than
	// letting the user discover them at launch. Nothing is seeded into the app
	// config: an unresolved import belongs to .env.dist, and the env editor holds
	// only variables that have a value (spec/19-import.md).
	var missingEnv []string
	if pending, err := pendingImports(req.Name); err != nil {
		log.Printf("Warning: failed to resolve imports: %s", err)
	} else {
		for _, r := range pending {
			missingEnv = append(missingEnv, r.Name)
		}
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
		result["warning"] = "A user with this name already existed. Its existing local password was reused."
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

	// Remove app entry from workspace config, along with everything the app
	// shared. The pool is append-only otherwise, so without the prune the app's
	// resolved secrets would outlive it forever — and, since ownership is
	// derived from the name prefix matching an existing app, they would come
	// back as editable hand-typed entries and be inherited by any app later
	// created with the same name. Both happen in ONE save.
	//
	// Consumers of the pruned variables are already handled: resolveOneImport
	// re-pends a ${{NAME}} reference whose target has left the pool rather than
	// falling back to a different producer, so those apps ask the user at their
	// next launch instead of silently binding to someone else's database.
	wsCfg, err := loadWorkspaceConfig()
	if err == nil {
		if wsCfg.Apps != nil {
			delete(wsCfg.Apps, req.Name)
		}
		if pruned := pruneSharedPool(wsCfg, req.Name); pruned > 0 {
			log.Printf("Shared: removed %d pool variables shared by %s", pruned, req.Name)
		}
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
