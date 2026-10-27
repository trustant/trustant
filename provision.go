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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// "Create App on <license host>": provisions the app's OpenServerless user on
// the license host through the Admin API user-provisioning endpoint, waits for
// the app to answer, then stores the production credentials. See
// spec/6-publish.md and spec/user_provisioning.md.

const publishProvisionProgressTotal = 3

// Tunable from tests.
var (
	provisionReadyTimeout   = 2 * time.Minute
	provisionPollInterval   = 3 * time.Second
	provisionRetryAttempts  = 5
	provisionMaxRetryDelay  = 5 * time.Second
	provisionHTTPClient     = &http.Client{Timeout: 20 * time.Second}
	provisionUsersPath      = "/system/api/v1/users"
	provisionDefaultService = "all"
)

// provisionedAppURL is the literal "<user>.<host>" for the license host,
// keeping its scheme and port.
func provisionedAppURL(host, user string) (string, error) {
	normalized, err := normalizeAPIHost(host)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	u.Host = user + "." + u.Host
	return u.String(), nil
}

// createProvisionedUser POSTs the user to the Admin API. It returns exists=true
// on 409 (the user is already there) and retries 503 with backoff, as the API
// asks. Any other non-202 status is an error carrying the server message.
func createProvisionedUser(ctx context.Context, host, token, user, password, email string) (bool, error) {
	normalized, err := normalizeAPIHost(host)
	if err != nil {
		return false, err
	}
	body, err := json.Marshal(map[string]string{
		"user": user, "password": password, "email": email, "services": provisionDefaultService,
	})
	if err != nil {
		return false, err
	}
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, normalized+provisionUsersPath, bytes.NewReader(body))
		if err != nil {
			return false, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := provisionHTTPClient.Do(req)
		if err != nil {
			return false, fmt.Errorf("cannot reach %s: %w", normalized, err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		var envelope struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &envelope)
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = strings.TrimSpace(string(raw))
		}
		switch resp.StatusCode {
		case http.StatusAccepted, http.StatusOK, http.StatusCreated:
			return false, nil
		case http.StatusConflict:
			return true, nil
		case http.StatusUnauthorized:
			return false, fmt.Errorf("invalid provisioning token for %s (change it in Configuration)", normalized)
		case http.StatusServiceUnavailable:
			if attempt < provisionRetryAttempts && !strings.Contains(message, "route setup failed") {
				delay := time.Duration(attempt) * time.Second
				if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
					delay = time.Duration(s) * time.Second
				}
				if delay > provisionMaxRetryDelay {
					delay = provisionMaxRetryDelay
				}
				select {
				case <-ctx.Done():
					return false, ctx.Err()
				case <-time.After(delay):
				}
				continue
			}
		}
		if message == "" {
			message = resp.Status
		}
		return false, fmt.Errorf("user provisioning failed (HTTP %d): %s", resp.StatusCode, message)
	}
}

// waitForProvisionedApp polls appURL until it answers 200 or the timeout
// expires. A 404 (and any transport error) means "not yet".
func waitForProvisionedApp(ctx context.Context, appURL string) error {
	deadline := time.Now().Add(provisionReadyTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, appURL, nil)
		if err != nil {
			return err
		}
		if resp, err := provisionHTTPClient.Do(req); err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().Add(provisionPollInterval).After(deadline) {
			return fmt.Errorf("%s was not ready after %s", appURL, provisionReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(provisionPollInterval):
		}
	}
}

// handlePublishProvision handles POST /api/publish/provision {name, token?}.
// A token in the body is stored as the provisioning token first; with none
// stored the answer is {needs_token, host} and nothing is called.
func handlePublishProvision(w http.ResponseWriter, r *http.Request) {
	if !requireValidLicense(w) {
		return
	}
	// Decode before upgrading; see handlePublishPush.
	var req struct {
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePublishJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	var ok bool
	if w, ok = preparePublishResponseWriter(w, r, publishProvisionProgressTotal); !ok {
		return
	}
	redactSecrets(w, req.Token)

	if !namePattern.MatchString(req.Name) {
		writePublishJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid name format"})
		return
	}
	cfg, err := loadWorkspaceConfig()
	if err != nil {
		writePublishJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to load config: " + err.Error()})
		return
	}
	host := strings.TrimSpace(cfg.LicenseHost)
	if host == "" {
		writePublishJSON(w, http.StatusBadRequest, map[string]string{"error": "No license host configured: set it in Configuration"})
		return
	}
	if !requireLicensedHost(w, host) {
		return
	}
	if token := strings.TrimSpace(req.Token); token != "" {
		cfg.ProvisioningToken = token
		if err := saveWorkspaceConfig(cfg); err != nil {
			writePublishJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save config: " + err.Error()})
			return
		}
	}
	token := strings.TrimSpace(cfg.ProvisioningToken)
	if token == "" {
		writePublishJSON(w, http.StatusOK, map[string]interface{}{"needs_token": true, "host": host})
		return
	}
	email := strings.TrimSpace(cfg.LicenseEmail)
	if email == "" {
		writePublishJSON(w, http.StatusBadRequest, map[string]string{"error": "No license email configured: set it in Configuration"})
		return
	}
	appCfg := cfg.Apps[req.Name]
	if appCfg == nil || appCfg.Password == "" {
		writePublishJSON(w, http.StatusBadRequest, map[string]string{"error": "Application " + req.Name + " has no local password"})
		return
	}
	// The Admin API only accepts lowercase user names.
	user := strings.ToLower(req.Name)
	password := appCfg.Password
	redactSecrets(w, token, password)
	appURL, err := provisionedAppURL(host, user)
	if err != nil {
		writePublishJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid license host: " + err.Error()})
		return
	}

	reportProgress(w, 1, "Creating app "+user+" on "+host+"...")
	exists, err := createProvisionedUser(r.Context(), host, token, user, password, email)
	if err != nil {
		log.Printf("Provisioning %s on %s failed: %v", user, host, err)
		writePublishJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if exists {
		reportOutput(w, "User "+user+" already exists on "+host)
	}

	reportProgress(w, 2, "Waiting for app to be provisioned...")
	if err := waitForProvisionedApp(r.Context(), appURL); err != nil {
		writePublishJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "Waiting for app to be provisioned failed: " + err.Error()})
		return
	}

	reportProgress(w, 3, "Saving production configuration...")
	cfg, err = loadWorkspaceConfig()
	if err != nil {
		writePublishJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to load config: " + err.Error()})
		return
	}
	if cfg.Apps == nil {
		cfg.Apps = make(map[string]*AppConfig)
	}
	if cfg.Apps[req.Name] == nil {
		cfg.Apps[req.Name] = &AppConfig{Password: password, Development: map[string]string{}}
	}
	if cfg.Apps[req.Name].Production == nil {
		cfg.Apps[req.Name].Production = make(map[string]string)
	}
	prod := cfg.Apps[req.Name].Production
	prod["OPS_APIHOST"] = host
	prod["OPS_USER"] = user
	prod["OPS_PASSWORD"] = password
	if err := saveWorkspaceConfig(cfg); err != nil {
		writePublishJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save config: " + err.Error()})
		return
	}

	message := "App " + user + " created on " + host
	if exists {
		message = "User " + user + " already exists on " + host + ". Publishing will override the existing app."
	}
	writePublishJSON(w, http.StatusOK, map[string]interface{}{
		"message":  message,
		"apihost":  host,
		"user":     user,
		"password": password,
		"exists":   exists,
		"url":      appURL,
	})
}
