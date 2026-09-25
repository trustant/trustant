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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const managedGitHubHost = "github.com"

var (
	managedGitHubCommandTimeout = 20 * time.Second
	managedGitHubLoginTimeout   = 10 * time.Minute
	githubDeviceCodePattern     = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4}\b`)
	githubDeviceURLPattern      = regexp.MustCompile(`https://github\.com/login/device`)
	errGitHubLoginInProgress    = errors.New("a GitHub login is already in progress")
	managedGitHubLogin          githubLoginController
	managedGitHubCredentialsMu  sync.Mutex
)

type githubAccountStatus struct {
	Available     bool   `json:"available"`
	Authenticated bool   `json:"authenticated"`
	Login         string `json:"login,omitempty"`
	Hostname      string `json:"hostname"`
	Protocol      string `json:"protocol"`
	Error         string `json:"error,omitempty"`
}

type githubRepository struct {
	Name          string `json:"name"`
	Visibility    string `json:"visibility"`
	DefaultBranch string `json:"default_branch"`
	CloneURL      string `json:"-"`
}

type githubLoginSnapshot struct {
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

type githubLoginController struct {
	mu              sync.Mutex
	state           string
	deviceURL       string
	deviceCode      string
	messageBuffer   string
	cancel          context.CancelFunc
	cancelRequested bool
}

type githubLoginOutputWriter struct {
	controller *githubLoginController
}

func (w githubLoginOutputWriter) Write(data []byte) (int, error) {
	w.controller.consume(string(data))
	return len(data), nil
}

func managedGitHubPaths() (root, configDir, gitConfig, home string, err error) {
	if strings.TrimSpace(WorkspaceDir) == "" {
		return "", "", "", "", errors.New("workspace directory is not configured")
	}
	root = stateDir("github")
	configDir = filepath.Join(root, "gh")
	gitConfig = filepath.Join(root, "gitconfig")
	home = filepath.Join(root, "home")
	for _, dir := range []string{root, configDir, home} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return "", "", "", "", fmt.Errorf("create managed GitHub directory: %w", err)
		}
		if err = os.Chmod(dir, 0700); err != nil {
			return "", "", "", "", fmt.Errorf("protect managed GitHub directory: %w", err)
		}
	}
	file, openErr := os.OpenFile(gitConfig, os.O_CREATE|os.O_WRONLY, 0600)
	if openErr != nil {
		return "", "", "", "", fmt.Errorf("create managed Git config: %w", openErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		return "", "", "", "", fmt.Errorf("close managed Git config: %w", closeErr)
	}
	if err = os.Chmod(gitConfig, 0600); err != nil {
		return "", "", "", "", fmt.Errorf("protect managed Git config: %w", err)
	}
	return root, configDir, gitConfig, home, nil
}

func envName(entry string) string {
	if index := strings.IndexByte(entry, '='); index >= 0 {
		return entry[:index]
	}
	return entry
}

func setEnvValue(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if envName(entry) != key {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}

func managedGitHubEnvironment() ([]string, error) {
	_, configDir, gitConfig, home, err := managedGitHubPaths()
	if err != nil {
		return nil, err
	}
	blocked := map[string]bool{
		"GH_TOKEN":            true,
		"GITHUB_TOKEN":        true,
		"GH_ENTERPRISE_TOKEN": true,
		"GITHUB_ENTERPRISE_TOKEN": true,
		"GH_CONFIG_DIR":       true,
		"GIT_CONFIG_GLOBAL":   true,
		"HOME":                true,
		"GH_HOST":             true,
		"GH_BROWSER":          true,
	}
	environment := make([]string, 0, len(os.Environ())+7)
	for _, entry := range os.Environ() {
		if !blocked[envName(entry)] {
			environment = append(environment, entry)
		}
	}
	environment = append(environment,
		"GH_CONFIG_DIR="+configDir,
		"GIT_CONFIG_GLOBAL="+gitConfig,
		"HOME="+home,
		"GH_HOST="+managedGitHubHost,
		"GH_BROWSER=echo",
		"GH_PROMPT_DISABLED=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	return environment, nil
}

func managedGitHubCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return nil, errors.New("GitHub CLI is not installed")
	}
	environment, err := managedGitHubEnvironment()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = environment
	return cmd, nil
}

func getManagedGitHubStatus(ctx context.Context) githubAccountStatus {
	status := githubAccountStatus{
		Hostname: managedGitHubHost,
		Protocol: "https",
	}
	if _, err := exec.LookPath("gh"); err != nil {
		status.Error = "GitHub CLI is not available"
		return status
	}
	status.Available = true
	cmd, err := managedGitHubCommand(ctx, "api", "user")
	if err != nil {
		status.Error = "Managed GitHub configuration is unavailable"
		return status
	}
	output, err := cmd.Output()
	if err != nil {
		return status
	}
	var user struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(output, &user) != nil || strings.TrimSpace(user.Login) == "" {
		status.Error = "GitHub returned invalid account metadata"
		return status
	}
	status.Authenticated = true
	status.Login = user.Login
	return status
}

func lookupManagedGitHubRepository(ctx context.Context, repository string) (githubRepository, error) {
	if !repoPattern.MatchString(repository) {
		return githubRepository{}, errors.New("repository must use org/repo format")
	}
	cmd, err := managedGitHubCommand(ctx, "api", "repos/"+repository)
	if err != nil {
		return githubRepository{}, err
	}
	output, err := cmd.Output()
	if err != nil {
		return githubRepository{}, errors.New("repository is not accessible to the connected GitHub account")
	}
	var response struct {
		FullName      string `json:"full_name"`
		Visibility    string `json:"visibility"`
		DefaultBranch string `json:"default_branch"`
		CloneURL      string `json:"clone_url"`
	}
	if json.Unmarshal(output, &response) != nil || !repoPattern.MatchString(response.FullName) {
		return githubRepository{}, errors.New("GitHub returned invalid repository metadata")
	}
	cloneURL, parseErr := url.Parse(response.CloneURL)
	if parseErr != nil || cloneURL.Scheme != "https" || !strings.EqualFold(cloneURL.Hostname(), managedGitHubHost) {
		return githubRepository{}, errors.New("GitHub returned an unsupported clone URL")
	}
	defaultBranch := strings.TrimSpace(response.DefaultBranch)
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	return githubRepository{
		Name:          response.FullName,
		Visibility:    response.Visibility,
		DefaultBranch: defaultBranch,
		CloneURL:      response.CloneURL,
	}, nil
}

func listManagedGitHubRepositories(ctx context.Context, page, perPage int) ([]githubRepository, error) {
	query := fmt.Sprintf(
		"user/repos?affiliation=owner%%2Ccollaborator%%2Corganization_member&visibility=all&sort=updated&per_page=%d&page=%d",
		perPage,
		page,
	)
	cmd, err := managedGitHubCommand(ctx, "api", "--method", "GET", query)
	if err != nil {
		return nil, err
	}
	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("GitHub repository listing failed")
	}
	var response []struct {
		FullName      string `json:"full_name"`
		Visibility    string `json:"visibility"`
		DefaultBranch string `json:"default_branch"`
	}
	if json.Unmarshal(output, &response) != nil {
		return nil, errors.New("GitHub returned invalid repository metadata")
	}
	repositories := make([]githubRepository, 0, len(response))
	for _, item := range response {
		if !repoPattern.MatchString(item.FullName) {
			continue
		}
		defaultBranch := strings.TrimSpace(item.DefaultBranch)
		if defaultBranch == "" {
			defaultBranch = "main"
		}
		repositories = append(repositories, githubRepository{
			Name:          item.FullName,
			Visibility:    item.Visibility,
			DefaultBranch: defaultBranch,
		})
	}
	return repositories, nil
}

func managedGitHubRemoteURL(repository string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), managedGitHubCommandTimeout)
	defer cancel()
	status := getManagedGitHubStatus(ctx)
	if !status.Authenticated {
		return fmt.Sprintf("git@github.com:%s.git", repository), false, nil
	}
	if err := ensureManagedGitHubCredentials(); err != nil {
		return "", true, err
	}
	details, err := lookupManagedGitHubRepository(ctx, repository)
	if err != nil {
		return "", true, err
	}
	return details.CloneURL, true, nil
}

func setupManagedGitHubCredentials() error {
	managedGitHubCredentialsMu.Lock()
	defer managedGitHubCredentialsMu.Unlock()
	return setupManagedGitHubCredentialsLocked()
}

func setupManagedGitHubCredentialsLocked() error {
	setupSucceeded := false
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), managedGitHubCommandTimeout)
		cmd, err := managedGitHubCommand(ctx, "auth", "setup-git", "--hostname", managedGitHubHost)
		if err != nil {
			cancel()
			return err
		}
		runErr := cmd.Run()
		cancel()
		if runErr == nil {
			setupSucceeded = true
			break
		}
		if attempt == 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !setupSucceeded {
		return errors.New("GitHub authenticated, but its isolated Git credential helper could not be configured")
	}
	_, _, gitConfig, _, err := managedGitHubPaths()
	if err != nil {
		return err
	}
	if err := os.Chmod(gitConfig, 0600); err != nil {
		return err
	}
	config, err := os.ReadFile(gitConfig)
	if err != nil {
		return err
	}
	if !strings.Contains(string(config), "gh auth git-credential") {
		return errors.New("GitHub authenticated, but its isolated Git credential helper was not written")
	}
	return nil
}

func ensureManagedGitHubCredentials() error {
	managedGitHubCredentialsMu.Lock()
	defer managedGitHubCredentialsMu.Unlock()
	_, _, gitConfig, _, err := managedGitHubPaths()
	if err != nil {
		return err
	}
	config, err := os.ReadFile(gitConfig)
	if err != nil {
		return err
	}
	if strings.Contains(string(config), "gh auth git-credential") {
		return nil
	}
	return setupManagedGitHubCredentialsLocked()
}

func (c *githubLoginController) snapshot() githubLoginSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.state
	if state == "" {
		state = "idle"
	}
	return githubLoginSnapshot{
		State: state,
		URL:   c.deviceURL,
		Code:  c.deviceCode,
		Error: c.loginError(),
	}
}

func (c *githubLoginController) reconcileAuthenticated() githubLoginSnapshot {
	c.mu.Lock()
	if c.state != "connecting" {
		c.state = "connected"
		c.deviceURL = ""
		c.deviceCode = ""
		c.messageBuffer = ""
	}
	c.mu.Unlock()
	return c.snapshot()
}

func (c *githubLoginController) loginError() string {
	if c.state == "failed" {
		return "GitHub login failed or expired; retry the connection"
	}
	return ""
}

func (c *githubLoginController) consume(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messageBuffer += message
	if len(c.messageBuffer) > 16384 {
		c.messageBuffer = c.messageBuffer[len(c.messageBuffer)-16384:]
	}
	if c.deviceCode == "" {
		c.deviceCode = githubDeviceCodePattern.FindString(c.messageBuffer)
	}
	if c.deviceURL == "" {
		c.deviceURL = githubDeviceURLPattern.FindString(c.messageBuffer)
	}
}

func (c *githubLoginController) start() (githubLoginSnapshot, error) {
	c.mu.Lock()
	if c.state == "connecting" {
		c.mu.Unlock()
		return githubLoginSnapshot{}, errGitHubLoginInProgress
	}
	c.mu.Unlock()

	statusCtx, statusCancel := context.WithTimeout(context.Background(), managedGitHubCommandTimeout)
	status := getManagedGitHubStatus(statusCtx)
	statusCancel()
	if status.Authenticated {
		c.mu.Lock()
		c.state = "connected"
		c.deviceURL = ""
		c.deviceCode = ""
		c.mu.Unlock()
		return c.snapshot(), nil
	}
	if !status.Available {
		return githubLoginSnapshot{}, errors.New("GitHub CLI is not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), managedGitHubLoginTimeout)
	cmd, err := managedGitHubCommand(
		ctx,
		"auth", "login",
		"--hostname", managedGitHubHost,
		"--git-protocol", "https",
		"--web",
		"--skip-ssh-key",
	)
	if err != nil {
		cancel()
		return githubLoginSnapshot{}, err
	}

	c.mu.Lock()
	c.state = "connecting"
	c.deviceURL = ""
	c.deviceCode = ""
	c.messageBuffer = ""
	c.cancel = cancel
	c.cancelRequested = false
	c.mu.Unlock()

	writer := githubLoginOutputWriter{controller: c}
	cmd.Stdout = writer
	cmd.Stderr = writer
	go func() {
		runErr := cmd.Run()
		if runErr == nil {
			runErr = setupManagedGitHubCredentials()
		} else {
			statusCtx, statusCancel := context.WithTimeout(context.Background(), managedGitHubCommandTimeout)
			status := getManagedGitHubStatus(statusCtx)
			statusCancel()
			if status.Authenticated {
				runErr = setupManagedGitHubCredentials()
			}
		}
		c.finish(ctx.Err(), runErr)
	}()
	return c.snapshot(), nil
}

func (c *githubLoginController) finish(contextErr, runErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.cancelRequested:
		c.state = "cancelled"
	case errors.Is(contextErr, context.DeadlineExceeded):
		c.state = "failed"
	case runErr != nil:
		c.state = "failed"
	default:
		c.state = "connected"
		c.deviceURL = ""
		c.deviceCode = ""
	}
	c.cancel = nil
	c.messageBuffer = ""
}

func (c *githubLoginController) cancelLogin() bool {
	c.mu.Lock()
	if c.state != "connecting" || c.cancel == nil {
		c.mu.Unlock()
		return false
	}
	cancel := c.cancel
	c.cancelRequested = true
	c.mu.Unlock()
	cancel()
	return true
}

func (c *githubLoginController) reset() {
	c.mu.Lock()
	cancel := c.cancel
	c.state = ""
	c.deviceURL = ""
	c.deviceCode = ""
	c.messageBuffer = ""
	c.cancel = nil
	c.cancelRequested = false
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func writeGitHubJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), managedGitHubCommandTimeout)
	defer cancel()
	writeGitHubJSON(w, http.StatusOK, getManagedGitHubStatus(ctx))
}

func handleGitHubLogin(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		snapshot := managedGitHubLogin.snapshot()
		if snapshot.State != "connecting" {
			ctx, cancel := context.WithTimeout(r.Context(), managedGitHubCommandTimeout)
			status := getManagedGitHubStatus(ctx)
			cancel()
			if status.Authenticated {
				snapshot = managedGitHubLogin.reconcileAuthenticated()
			}
		}
		writeGitHubJSON(w, http.StatusOK, snapshot)
	case http.MethodPost:
		snapshot, err := managedGitHubLogin.start()
		if errors.Is(err, errGitHubLoginInProgress) {
			writeGitHubJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if err != nil {
			writeGitHubJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeGitHubJSON(w, http.StatusAccepted, snapshot)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleGitHubLoginCancel(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !managedGitHubLogin.cancelLogin() {
		writeGitHubJSON(w, http.StatusConflict, map[string]string{"error": "no GitHub login is active"})
		return
	}
	writeGitHubJSON(w, http.StatusOK, map[string]string{"state": "cancelled"})
}

func handleGitHubLogout(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	managedGitHubLogin.cancelLogin()
	ctx, cancel := context.WithTimeout(r.Context(), managedGitHubCommandTimeout)
	status := getManagedGitHubStatus(ctx)
	cancel()
	logoutFailed := false
	if status.Authenticated {
		logoutCtx, logoutCancel := context.WithTimeout(r.Context(), managedGitHubCommandTimeout)
		cmd, err := managedGitHubCommand(
			logoutCtx,
			"auth", "logout",
			"--hostname", managedGitHubHost,
			"--user", status.Login,
		)
		if err != nil || cmd.Run() != nil {
			logoutFailed = true
		}
		logoutCancel()
	}
	root := stateDir("github")
	if err := os.RemoveAll(root); err != nil {
		writeGitHubJSON(w, http.StatusInternalServerError, map[string]string{"error": "managed GitHub state could not be removed"})
		return
	}
	managedGitHubLogin.reset()
	if logoutFailed {
		writeGitHubJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "GitHub logout did not complete, but Trustable-managed credentials were removed",
		})
		return
	}
	writeGitHubJSON(w, http.StatusOK, githubAccountStatus{
		Available: true,
		Hostname:  managedGitHubHost,
		Protocol:  "https",
	})
}

func handleGitHubRepos(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page := 1
	perPage := 50
	if value := r.URL.Query().Get("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 10 {
			http.Error(w, "page must be between 1 and 10", http.StatusBadRequest)
			return
		}
		page = parsed
	}
	if value := r.URL.Query().Get("per_page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 50 {
			http.Error(w, "per_page must be between 1 and 50", http.StatusBadRequest)
			return
		}
		perPage = parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), managedGitHubCommandTimeout)
	defer cancel()
	status := getManagedGitHubStatus(ctx)
	if !status.Authenticated {
		writeGitHubJSON(w, http.StatusUnauthorized, map[string]string{"error": "Connect a GitHub account first"})
		return
	}
	repositories, err := listManagedGitHubRepositories(ctx, page, perPage)
	if err != nil {
		writeGitHubJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeGitHubJSON(w, http.StatusOK, map[string]interface{}{
		"repositories": repositories,
		"page":         page,
		"per_page":     perPage,
	})
}
