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
	"bytes"
	"encoding/json"
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

// Publish progress stage counts. Streamed to the UI as `total` so the progress
// bar can size itself. See spec/6-publish.md.
const (
	publishPushProgressTotal   = 3
	publishRemoteProgressTotal = 6
)

// preparePublishResponseWriter upgrades the response to SSE when the client
// asks for it. Requests without `Accept: text/event-stream` — notably the
// needs_config probe — keep the plain JSON behaviour.
func preparePublishResponseWriter(w http.ResponseWriter, r *http.Request, total int) (http.ResponseWriter, bool) {
	writer, ok := prepareProgressResponseWriter(w, r, total, "publish progress streaming is not supported")
	if !ok {
		return nil, false
	}
	if _, streaming := writer.(*progressSSEResponseWriter); !streaming {
		writer.Header().Set("Content-Type", "application/json")
	}
	return writer, true
}

// writePublishJSON writes the terminal payload. On the streaming path the
// status code is not written: the SSE response is already committed as 200 and
// the outcome travels in the `done`/`error` event.
func writePublishJSON(w http.ResponseWriter, status int, payload interface{}) {
	if _, streaming := w.(*progressSSEResponseWriter); !streaming {
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
	}
	json.NewEncoder(w).Encode(payload)
}

// writePublishError reports a request-validation failure. It replaces
// http.Error on the publish paths: once the response has been upgraded to SSE,
// http.Error's plain-text body carries no `data:` line, so the client would see
// the stream end with no terminal event and no reason for the failure.
func writePublishError(w http.ResponseWriter, message string, status int) {
	if _, streaming := w.(*progressSSEResponseWriter); streaming {
		writePublishJSON(w, status, map[string]string{"error": message})
		return
	}
	http.Error(w, message, status)
}

// handlePublishPush handles POST /api/publish/push
// Pushes code to a production GitHub repository. Requires a valid license; the
// license `hosts` list is not consulted for push. See spec/14-license.md.
func handlePublishPush(w http.ResponseWriter, r *http.Request) {
	if !requireValidLicense(w) {
		return
	}
	// The body must be decoded before the SSE upgrade: upgrading flushes the
	// response, after which Go's server no longer serves the unread body.
	var req struct {
		Name string `json:"name"`
		Repo string `json:"repo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var ok bool
	if w, ok = preparePublishResponseWriter(w, r, publishPushProgressTotal); !ok {
		return
	}
	reportProgress(w, 1, "Checking license and repository configuration...")

	if !namePattern.MatchString(req.Name) {
		writePublishError(w, "Invalid name format", http.StatusBadRequest)
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
			writePublishError(w, "Repo must be in format org/repo", http.StatusBadRequest)
			return
		}
		wsCfg.Apps[req.Name].Production["OPS_REPO"] = req.Repo
		if err := saveWorkspaceConfig(wsCfg); err != nil {
			writePublishError(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Check if OPS_REPO is configured
	opsRepo := wsCfg.Apps[req.Name].Production["OPS_REPO"]
	if opsRepo == "" {
		writePublishJSON(w, http.StatusOK, map[string]interface{}{"needs_config": true})
		return
	}

	// Use workspace bare repo to push
	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		writePublishJSON(w, http.StatusBadRequest, map[string]string{
			"error": "App not found in workspace",
		})
		return
	}

	reportProgress(w, 2, "Configuring production remote...")
	var output bytes.Buffer
	branch, err := gitDefaultBranch(workspacePath)
	if err != nil {
		writePublishJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err := ensureProductionRemoteStreaming(w, workspacePath, opsRepo, &output); err != nil {
		writePublishJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "output": output.String()})
		return
	}
	reportProgress(w, 3, "Pushing to GitHub...")
	if err := runGitCommandStreaming(w, workspacePath, &output, "push", "production", branch); err != nil {
		log.Printf("Git push to production failed: %s", err)
		writePublishJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  err.Error(),
			"output": output.String(),
		})
		return
	}

	writePublishJSON(w, http.StatusOK, map[string]string{
		"output": output.String(),
	})
}

// handlePublishForcePush handles POST /api/publish/force-push
// Force pushes code to the production GitHub repository. Requires a valid
// license; the license `hosts` list is not consulted for push.
func handlePublishForcePush(w http.ResponseWriter, r *http.Request) {
	if !requireValidLicense(w) {
		return
	}
	// Decode before upgrading; see handlePublishPush.
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var ok bool
	if w, ok = preparePublishResponseWriter(w, r, publishPushProgressTotal); !ok {
		return
	}
	reportProgress(w, 1, "Checking license and repository configuration...")

	if !namePattern.MatchString(req.Name) {
		writePublishError(w, "Invalid name format", http.StatusBadRequest)
		return
	}

	// Load config to get OPS_REPO
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		writePublishJSON(w, http.StatusOK, map[string]string{"error": "Failed to load config: " + err.Error()})
		return
	}
	if wsCfg.Apps == nil || wsCfg.Apps[req.Name] == nil || wsCfg.Apps[req.Name].Production == nil {
		writePublishJSON(w, http.StatusOK, map[string]string{"error": "No production config found"})
		return
	}

	opsRepo := wsCfg.Apps[req.Name].Production["OPS_REPO"]
	if opsRepo == "" {
		writePublishJSON(w, http.StatusOK, map[string]string{"error": "No production repository configured"})
		return
	}

	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		writePublishJSON(w, http.StatusOK, map[string]string{"error": "App not found in workspace"})
		return
	}

	reportProgress(w, 2, "Configuring production remote...")
	var output bytes.Buffer
	branch, err := gitDefaultBranch(workspacePath)
	if err != nil {
		writePublishJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err := ensureProductionRemoteStreaming(w, workspacePath, opsRepo, &output); err != nil {
		writePublishJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "output": output.String()})
		return
	}
	reportProgress(w, 3, "Pushing to GitHub...")
	if err := runGitCommandStreaming(w, workspacePath, &output, "push", "-f", "production", branch); err != nil {
		log.Printf("Git force push to production failed: %s", err)
		writePublishJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  err.Error(),
			"output": output.String(),
		})
		return
	}

	writePublishJSON(w, http.StatusOK, map[string]string{
		"output": output.String(),
	})
}

// handlePublishRemote handles POST /api/publish/remote
// Deploys to a production OpenServerless environment
func handlePublishRemote(w http.ResponseWriter, r *http.Request) {
	if !requireValidLicense(w) {
		return
	}
	// Decode before upgrading; see handlePublishPush.
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

	var ok bool
	if w, ok = preparePublishResponseWriter(w, r, publishRemoteProgressTotal); !ok {
		return
	}
	// The production password must never reach the stream, whether it arrives
	// with this request or was already stored in the config below.
	redactSecrets(w, req.Password)
	reportProgress(w, 1, "Checking license and production configuration...")

	if !namePattern.MatchString(req.Name) {
		writePublishError(w, "Invalid name format", http.StatusBadRequest)
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
			writePublishError(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Check all required production values are set
	prod := wsCfg.Apps[req.Name].Production
	redactSecrets(w, prod["OPS_PASSWORD"])
	if prod["OPS_APIHOST"] == "" || prod["OPS_USER"] == "" || prod["OPS_PASSWORD"] == "" {
		writePublishJSON(w, http.StatusOK, map[string]interface{}{"needs_config": true})
		return
	}

	// Shared variables another app produces must already have a value for THIS
	// host before anything touches the cluster. Unlike the development gate this
	// blocks: a launch with a missing value costs a broken dev server, a publish
	// with one deploys an app pointed at nothing. Two ways out, both offered by
	// the frontend — publish the producing app to this host, or type the value
	// in by hand for it.
	// The MERGED config: the production pool can come from either layer, and the
	// gate must not block on a value the base layer already supplies.
	mergedCfg, err := loadTrustableConfig()
	if err != nil {
		mergedCfg = wsCfg
	}
	if missing := missingProductionShared(req.Name, prod["OPS_APIHOST"], mergedCfg); len(missing) > 0 {
		log.Printf("Publish of %s blocked, unresolved shared values on %s: %+v", req.Name, prod["OPS_APIHOST"], missing)
		writePublishJSON(w, http.StatusOK, map[string]interface{}{
			"needs_config":   true,
			"missing_shared": missing,
		})
		return
	}

	// The target apihost must be covered by the license before anything
	// touches the cluster. Local apihosts are always allowed.
	if !requireLicensedHost(w, prod["OPS_APIHOST"]) {
		return
	}

	// Ensure workbench exists
	workbenchPath := filepath.Join(WorkbenchDir, req.Name)
	workspacePath := filepath.Join(WorkspaceDir, "workspace", req.Name)

	// Accumulates everything the streamed commands produced, so the terminal
	// payload carries the full output on success as well as on failure.
	var output bytes.Buffer

	reportProgress(w, 2, "Preparing workbench...")
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		// Clone from workspace
		log.Printf("Cloning workbench for %s...", req.Name)
		cloneCmd := exec.Command("git", "clone", workspacePath, workbenchPath)
		if err := runStreamingCommand(w, &output, cloneCmd); err != nil {
			log.Printf("Failed to clone workbench: %s, output: %s", err, output.String())
			writePublishJSON(w, http.StatusInternalServerError, map[string]string{
				"error":  "Failed to clone workbench: " + err.Error(),
				"output": strings.TrimSpace(output.String()),
			})
			return
		}

		// Run npm install if package.json exists
		pkgPath := filepath.Join(workbenchPath, "package.json")
		if _, err := os.Stat(pkgPath); err == nil {
			reportProgress(w, 3, "Installing dependencies...")
			npmCmd := exec.Command("npm", "install")
			npmCmd.Dir = workbenchPath
			if err := runStreamingCommand(w, &output, npmCmd); err != nil {
				log.Printf("npm install failed: %s, output: %s", err, output.String())
			}
		} else {
			reportProgress(w, 3, "Installing dependencies (skipped, no package.json)...")
		}
	} else {
		reportProgress(w, 3, "Installing dependencies (skipped, workbench present)...")
	}

	// Generate env files (always, to ensure .env.production is current)
	reportProgress(w, 4, "Generating environment files...")
	if err := generateAppEnvFiles(req.Name); err != nil {
		log.Printf("Failed to generate env files: %s", err)
		writePublishJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  "Failed to generate environment files: " + err.Error(),
			"output": strings.TrimSpace(output.String()),
		})
		return
	}

	// Run ops ide login --mode=production
	reportProgress(w, 5, "Connecting to OpenServerless...")
	log.Printf("Running ops ide login --mode=production for %s...", req.Name)
	removeOpsConfig()
	loginCmd := exec.Command("ops", "ide", "login", "--mode=production")
	loginCmd.Dir = workbenchPath
	if err := runStreamingCommand(w, &output, loginCmd); err != nil {
		log.Printf("ops ide login --mode=production failed: %s, output: %s", err, output.String())
		writePublishJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  "Login failed: " + err.Error(),
			"output": strings.TrimSpace(output.String()),
		})
		return
	}

	// The production login just wrote this cluster's service bindings. Resolve
	// THIS app's own .env.shared against them and store the values under this
	// host, so an app that consumes them can be published next. Only this app is
	// resolved: a production login is a real operation against a real cluster,
	// and sweeping every producer would log into clusters the user never asked
	// to touch. Non-fatal — the deploy of this app does not depend on it.
	func() {
		unlock := lockRuntimeLifecycle("resolve production shared " + req.Name)
		defer unlock()
		if _, err := resolveProductionShared(req.Name, prod["OPS_APIHOST"]); err != nil {
			log.Printf("Warning: failed to resolve production shared values for %s: %s", req.Name, err)
		}
	}()

	// Run ops ide deploy
	reportProgress(w, 6, "Deploying application...")
	log.Printf("Running ops ide deploy for %s...", req.Name)
	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workbenchPath
	if err := runStreamingCommand(w, &output, deployCmd); err != nil {
		log.Printf("ops ide deploy failed: %s, output: %s", err, output.String())
		writePublishJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  "Deploy failed: " + err.Error(),
			"output": strings.TrimSpace(output.String()),
		})
		return
	}

	writePublishJSON(w, http.StatusOK, map[string]string{
		"message": "Published successfully",
		"output":  strings.TrimSpace(output.String()),
	})
}
