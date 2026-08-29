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
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"
)

const (
	// Preserve the historical public port split: TruACP replaces OpenCode on the
	// left pane without changing ingress/proxy contracts; Vite remains on 5173.
	truacpPort        = 4096
	opsdevelPort      = 5173
	localLoopbackHost = "127.0.0.1"
	// WHY: ~/.local/bin contains the generated wrapper itself, so resolving the
	// upstream Milvus CLI through PATH can recurse or follow a stale uv symlink.
	globalMilvusCLIPath = "/usr/local/bin/milvus_cli"
)

var runtimeLifecycleMu sync.Mutex

// lockRuntimeLifecycle serializes launch, stop, and redeploy operations because
// all applications share TruACP :4096, Vite :5173, and one process-group marker.
// Browser tabs can submit overlapping requests; allowing them to proceed in
// parallel lets a late request mistake the runtime just started by an earlier
// request for an orphan and either kill it or report a false port conflict.
func lockRuntimeLifecycle(operation string) func() {
	log.Printf("Runtime lifecycle: waiting for %s", operation)
	runtimeLifecycleMu.Lock()
	log.Printf("Runtime lifecycle: acquired for %s", operation)
	return func() {
		log.Printf("Runtime lifecycle: released for %s", operation)
		runtimeLifecycleMu.Unlock()
	}
}

// opsConfig mirrors the service blocks of ~/.ops/config.json that drive MCP
// server generation and CLI tooling at launch time (see spec/4-launch.md).
type opsConfig struct {
	S3 struct {
		Host   string `json:"host"`
		Port   int    `json:"port"`
		Bucket struct {
			Data   string `json:"data"`
			Static string `json:"static"`
		} `json:"bucket"`
		Access struct {
			Key string `json:"key"`
		} `json:"access"`
		Secret struct {
			Key string `json:"key"`
		} `json:"secret"`
	} `json:"s3"`
	Postgres struct {
		Database string `json:"database"`
		Username string `json:"username"`
		Password string `json:"password"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		URL      string `json:"url"`
	} `json:"postgres"`
	Redis struct {
		Port     int    `json:"port"`
		Password string `json:"password"`
		URL      string `json:"url"`
		Service  string `json:"service"`
		Prefix   string `json:"prefix"`
	} `json:"redis"`
	Milvus struct {
		Host  string `json:"host"`
		Port  int    `json:"port"`
		Token string `json:"token"`
		DB    struct {
			Name string `json:"name"`
		} `json:"db"`
	} `json:"milvus"`
	MongoDB struct {
		URI              string `json:"uri"`
		URL              string `json:"url"`
		ConnectionString string `json:"connection_string"`
		Host             string `json:"host"`
		Port             int    `json:"port"`
		Database         string `json:"database"`
		Username         string `json:"username"`
		Password         string `json:"password"`
		AuthSource       string `json:"auth_source"`
	} `json:"mongodb"`
	MongoDBURI string `json:"MONGODB_URI"`
}

// opsConfigPath returns the path to ~/.ops/config.json.
func opsConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ops", "config.json")
}

// loadOpsConfig reads and parses ~/.ops/config.json. A missing file is not an
// error — it returns a zero-value config so the caller emits no MCP servers.
func loadOpsConfig() (*opsConfig, error) {
	path := opsConfigPath()
	if path == "" {
		return &opsConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &opsConfig{}, nil
		}
		return nil, err
	}
	var cfg opsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// buildLaunchMCPConfig builds the OpenCode `mcp` section from ~/.ops/config.json
// per spec/4-launch.md. Each server is added only when its backing config block
// is present. Returns nil when no services are configured so the caller emits no
// mcp section. Reading the config is best-effort: failures are logged and yield
// an empty section rather than blocking a launch.
func buildLaunchMCPConfig() map[string]interface{} {
	cfg, err := loadOpsConfig()
	if err != nil {
		log.Printf("Warning: failed to load ~/.ops/config.json for MCP generation: %s", err)
		return nil
	}
	return buildMCPFromOpsConfig(cfg)
}

// buildMCPFromOpsConfig is the pure builder behind buildLaunchMCPConfig: it maps
// a parsed opsConfig to the OpenCode mcp section. Each server is added only when
// its backing config block is present; returns nil when nothing is configured.
func buildMCPFromOpsConfig(cfg *opsConfig) map[string]interface{} {
	mcp := make(map[string]interface{})

	if cfg.S3.Host != "" {
		mcp["s3"] = map[string]interface{}{
			"type":    "local",
			"command": []string{"mcp-s3"},
			"environment": map[string]string{
				"S3_ENDPOINT":           fmt.Sprintf("http://%s:%d", cfg.S3.Host, cfg.S3.Port),
				"AWS_ACCESS_KEY_ID":     cfg.S3.Access.Key,
				"AWS_SECRET_ACCESS_KEY": cfg.S3.Secret.Key,
				"S3_CONNECTION_NAME":    "default",
				"S3_USE_PATH_STYLE":     "true",
			},
			"enabled": true,
			"timeout": 30000,
		}
	}

	if cfg.Postgres.Database != "" {
		mcp["postgres"] = map[string]interface{}{
			"type":    "local",
			"command": []string{"postgres-mcp", "--access-mode=unrestricted"},
			"environment": map[string]string{
				"DATABASE_URI": cfg.Postgres.URL,
			},
			"enabled": true,
			"timeout": 30000,
		}
	}

	if cfg.Redis.URL != "" || cfg.Redis.Port != 0 {
		mcp["redis"] = map[string]interface{}{
			"type": "local",
			"command": []string{
				"trustable-redis-mcp",
				"--host", cfg.Redis.Service,
				"--port", fmt.Sprintf("%d", cfg.Redis.Port),
				"--username", redisUsername(cfg),
				"--password", cfg.Redis.Password,
			},
			"environment": map[string]string{
				"REDIS_USERNAME": redisUsername(cfg),
				"REDIS_HOST":     cfg.Redis.Service,
				"REDIS_PORT":     fmt.Sprintf("%d", cfg.Redis.Port),
				"REDIS_PWD":      cfg.Redis.Password,
				"TRUSTABLE_REDIS_PREFIX": cfg.Redis.Prefix,
			},
			"enabled": true,
			"timeout": 30000,
		}
	}

	if cfg.Milvus.Host != "" {
		mcp["milvus"] = map[string]interface{}{
			"type": "local",
			"command": []string{
				"mcp-server-milvus",
				"--milvus-token", cfg.Milvus.Token,
				"--milvus-db", cfg.Milvus.DB.Name,
				"--milvus-uri", fmt.Sprintf("http://%s:%d", cfg.Milvus.Host, cfg.Milvus.Port),
			},
			"environment": map[string]string{
				"MILVUS_URI": fmt.Sprintf("http://%s:%d", cfg.Milvus.Host, cfg.Milvus.Port),
			},
			"enabled": true,
			"timeout": 30000,
		}
	}

	if uri := mongodbConnectionString(cfg); uri != "" {
		mcp["mongodb"] = map[string]interface{}{
			"type":    "local",
			"command": []string{"mongodb-mcp-server"},
			"environment": map[string]string{
				"MDB_MCP_CONNECTION_STRING": uri,
			},
			"enabled": true,
			"timeout": 30000,
		}
	}

	if len(mcp) == 0 {
		return nil
	}
	return mcp
}

// isTrustableManagedMCPServer reports whether an mcp server name is one this app
// generates from ~/.ops/config.json. These belong in the LOCAL workbench
// opencode.json, never the global one (see spec/4-launch.md); the global writer
// uses this to drop any that leaked into a global config written by older code.
func isTrustableManagedMCPServer(name string) bool {
	switch name {
	case "s3", "postgres", "redis", "milvus", "mongodb", "openserverless", "react":
		return true
	}
	return false
}

// logOpsServiceBlocks reports which service blocks are present in
// ~/.ops/config.json after `ops ide login`. The MCP servers and CLI wrappers are
// generated only for present blocks (see buildMCPFromOpsConfig), so a missing
// block here is the root cause of a skipped/empty MCP server. Purely diagnostic.
func logOpsServiceBlocks(app string) {
	cfg, err := loadOpsConfig()
	if err != nil {
		log.Printf("Warning: could not read ~/.ops/config.json for %s after login: %s", app, err)
		return
	}
	for _, s := range []struct {
		name    string
		present bool
	}{
		{"s3", cfg.S3.Host != ""},
		{"postgres", cfg.Postgres.Database != ""},
		{"redis", cfg.Redis.URL != "" || cfg.Redis.Port != 0},
		{"milvus", cfg.Milvus.Host != ""},
		{"mongodb", mongodbConnectionString(cfg) != ""},
	} {
		if s.present {
			log.Printf("  - service %q configured for %s; MCP server will be generated", s.name, app)
		} else {
			log.Printf("  - service %q absent from ~/.ops/config.json for %s; MCP server will be skipped", s.name, app)
		}
	}
}

func appServiceRuntimeEnv(base []string) []string {
	cfg, err := loadOpsConfig()
	if err != nil {
		log.Printf("Warning: failed to load ~/.ops/config.json for service runtime env: %s", err)
		return base
	}
	if uri := mongodbConnectionString(cfg); uri != "" {
		base = append(base, "MONGODB_URI="+uri)
	}
	return base
}

// appendEnvironmentOverrides removes inherited/application values for the
// supplied keys before appending the host-owned values. This keeps managed
// notebook settings authoritative even if a stale process or app env carries
// variables with the same names.
func appendEnvironmentOverrides(base []string, overrides ...string) []string {
	keys := make(map[string]struct{}, len(overrides))
	for _, entry := range overrides {
		if key, _, ok := strings.Cut(entry, "="); ok {
			keys[key] = struct{}{}
		}
	}
	filtered := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replaced := keys[key]; replaced {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, overrides...)
}

const trustablePiRuntimeManifestVersion = 2

var trustablePiRuntimeManifestPathOverride string
var trustablePiExtensionPathOverride string

// trustablePiRuntimeWorkbench is one credential-free, host-owned application
// contract. Workspace deliberately names the active checkout, not the durable
// bare repository under WORKSPACE_DIR.
type trustablePiRuntimeWorkbench struct {
	App                string   `json:"app"`
	Workspace          string   `json:"workspace"`
	DevelopmentURL     string   `json:"developmentUrl"`
	BrowserURL         string   `json:"browserUrl"`
	RequiredMCPServers []string `json:"requiredMcpServers"`
	MCPConfig          string   `json:"mcpConfig"`
	WatcherLog         string   `json:"watcherLog"`
}

// trustablePiRuntimeManifest retains the versioned workbenches envelope. WHY:
// every consumer must validate one host contract instead of interpreting
// incompatible files carried in the same TRUSTABLE_RUNTIME_CONFIG variable.
type trustablePiRuntimeManifest struct {
	Version     int                           `json:"version"`
	Workbenches []trustablePiRuntimeWorkbench `json:"workbenches"`
}

// trustablePiRuntimeManifestPath keeps the mutable host contract outside the
// application checkout. WHY: generated project files are model-editable input,
// while the selected workbench boundary must remain host-owned.
func trustablePiRuntimeManifestPath() (string, error) {
	if trustablePiRuntimeManifestPathOverride != "" {
		return trustablePiRuntimeManifestPathOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home for Trustable Pi runtime manifest: %w", err)
	}
	return filepath.Join(home, ".config", "trustable", "pi-runtime.json"), nil
}

// trustablePiExtensionPath resolves the extension installed by trustable-acp's
// setup.sh. WHY: managed mode must fail before starting a session when the
// deterministic policy artifact is absent instead of silently running plain Pi.
func trustablePiExtensionPath() (string, error) {
	if trustablePiExtensionPathOverride != "" {
		return trustablePiExtensionPathOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home for Trustable Pi extension: %w", err)
	}
	path := filepath.Join(home, ".local", "lib", "truacp", "extensions", "trustable-runtime.ts")
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("Trustable Pi extension is not installed at %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Trustable Pi extension is not a regular file: %s", path)
	}
	return path, nil
}

// browserVisibleDevelopmentURL derives Vite's public origin from the browser's
// Trustable request. WHY: localhost and the configured OpenServerless API host
// describe different network surfaces and cannot be substituted for the URL
// the user can actually open.
func browserVisibleDevelopmentURL(r *http.Request) (string, error) {
	scheme := "http"
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded != "" {
		scheme = forwarded
	} else if r.TLS != nil {
		scheme = "https"
	}
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported browser-visible protocol %q", scheme)
	}

	requestURL, err := url.Parse(scheme + "://" + r.Host)
	if err != nil || requestURL.Hostname() == "" {
		return "", fmt.Errorf("invalid Trustable request host %q", r.Host)
	}
	const trustablePrefix = "trustable."
	if !strings.HasPrefix(requestURL.Hostname(), trustablePrefix) {
		return "", fmt.Errorf("invalid Trustable request host %q: expected trustable.<domain>", r.Host)
	}
	viteHost := "vite." + strings.TrimPrefix(requestURL.Hostname(), trustablePrefix)
	if port := requestURL.Port(); port != "" {
		viteHost = net.JoinHostPort(viteHost, port)
	}
	return (&url.URL{Scheme: scheme, Host: viteHost}).String(), nil
}

// writeTrustablePiRuntimeManifest publishes an atomic, private manifest after
// project MCP generation. WHY: the required server list must describe the exact
// .mcp.json that Pi will consume, not a pre-login or inferred service set.
func writeTrustablePiRuntimeManifest(app, projectDir, browserURL, watcherLog string) (string, error) {
	canonicalProjectDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve Trustable workbench %s: %w", projectDir, err)
	}
	canonicalProjectDir, err = filepath.Abs(canonicalProjectDir)
	if err != nil {
		return "", fmt.Errorf("failed to make Trustable workbench absolute: %w", err)
	}
	parsedBrowserURL, err := url.Parse(browserURL)
	if err != nil || parsedBrowserURL.Host == "" ||
		(parsedBrowserURL.Scheme != "http" && parsedBrowserURL.Scheme != "https") {
		return "", fmt.Errorf("invalid browser-visible application URL %q", browserURL)
	}
	canonicalWatcherLog, err := filepath.Abs(watcherLog)
	if err != nil {
		return "", fmt.Errorf("failed to make watcher log absolute: %w", err)
	}
	watcherInfo, err := os.Stat(canonicalWatcherLog)
	if err != nil {
		return "", fmt.Errorf("failed to inspect watcher log %s: %w", canonicalWatcherLog, err)
	}
	if !watcherInfo.Mode().IsRegular() || watcherInfo.Mode().Perm() != 0600 {
		return "", fmt.Errorf("watcher log must be a private regular file: %s", canonicalWatcherLog)
	}
	if rel, err := filepath.Rel(canonicalProjectDir, canonicalWatcherLog); err != nil ||
		(rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", fmt.Errorf("watcher log must remain outside the workbench: %s", canonicalWatcherLog)
	}

	data, err := os.ReadFile(filepath.Join(canonicalProjectDir, ".mcp.json"))
	if err != nil {
		return "", fmt.Errorf("failed to read generated MCP config: %w", err)
	}
	var config struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("failed to parse generated MCP config: %w", err)
	}
	if len(config.MCPServers) == 0 {
		return "", errors.New("generated MCP config declares no servers")
	}
	servers := make([]string, 0, len(config.MCPServers))
	for name := range config.MCPServers {
		if strings.TrimSpace(name) == "" {
			return "", errors.New("generated MCP config contains an empty server name")
		}
		servers = append(servers, name)
	}
	sort.Strings(servers)
	privateMCPConfig, err := managedMCPConfigPath(canonicalProjectDir)
	if err != nil {
		return "", err
	}
	privateMCPConfig, err = filepath.Abs(privateMCPConfig)
	if err != nil {
		return "", fmt.Errorf("failed to make private MCP config absolute: %w", err)
	}
	privateMCPInfo, err := os.Stat(privateMCPConfig)
	if err != nil {
		return "", fmt.Errorf("failed to inspect private MCP config: %w", err)
	}
	if !privateMCPInfo.Mode().IsRegular() || privateMCPInfo.Mode().Perm() != 0600 {
		return "", errors.New("private MCP config must be a mode-0600 regular file")
	}
	if rel, err := filepath.Rel(canonicalProjectDir, privateMCPConfig); err != nil ||
		(rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", errors.New("private MCP config must remain outside the workbench")
	}
	privateData, err := os.ReadFile(privateMCPConfig)
	if err != nil {
		return "", fmt.Errorf("failed to read private MCP config: %w", err)
	}
	var privateConfig struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(privateData, &privateConfig); err != nil {
		return "", fmt.Errorf("failed to parse private MCP config: %w", err)
	}
	for _, name := range servers {
		if _, ok := privateConfig.MCPServers[name]; !ok {
			return "", fmt.Errorf("private MCP config is missing server %q", name)
		}
	}
	if len(privateConfig.MCPServers) != len(servers) {
		return "", errors.New("private MCP config server set does not match the credential-free config")
	}

	manifest := trustablePiRuntimeManifest{
		Version: trustablePiRuntimeManifestVersion,
		Workbenches: []trustablePiRuntimeWorkbench{{
			App:       app,
			Workspace: canonicalProjectDir,
			// WHY: verification runs beside the managed Vite process and must use
			// the shared local port only after matching this exact workbench.
			DevelopmentURL:     "http://localhost:5173",
			BrowserURL:         browserURL,
			RequiredMCPServers: servers,
			MCPConfig:          privateMCPConfig,
			WatcherLog:         canonicalWatcherLog,
		}},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to encode Trustable Pi runtime manifest: %w", err)
	}
	manifestPath, err := trustablePiRuntimeManifestPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		return "", fmt.Errorf("failed to create Trustable Pi runtime config directory: %w", err)
	}
	temporaryPath := manifestPath + ".tmp"
	defer os.Remove(temporaryPath)
	if err := os.WriteFile(temporaryPath, encoded, 0600); err != nil {
		return "", fmt.Errorf("failed to write Trustable Pi runtime manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, manifestPath); err != nil {
		return "", fmt.Errorf("failed to publish Trustable Pi runtime manifest: %w", err)
	}
	return manifestPath, nil
}

// mongodbConnectionString returns a MongoDB URI only from the official
// post-login config surface. Arbitrary workbench env vars do not enable MongoDB.
func mongodbConnectionString(cfg *opsConfig) string {
	for _, direct := range []string{
		cfg.MongoDB.URI,
		cfg.MongoDB.URL,
		cfg.MongoDB.ConnectionString,
		cfg.MongoDBURI,
	} {
		if direct = strings.TrimSpace(direct); direct != "" {
			return direct
		}
	}

	host := strings.TrimSpace(cfg.MongoDB.Host)
	database := strings.Trim(strings.TrimSpace(cfg.MongoDB.Database), "/")
	if host == "" || database == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		return host
	}

	u := url.URL{
		Scheme: "mongodb",
		Host:   host,
		Path:   "/" + database,
	}
	if cfg.MongoDB.Port != 0 && !strings.Contains(host, ":") {
		u.Host = net.JoinHostPort(host, fmt.Sprintf("%d", cfg.MongoDB.Port))
	}
	if cfg.MongoDB.Username != "" {
		if cfg.MongoDB.Password != "" {
			u.User = url.UserPassword(cfg.MongoDB.Username, cfg.MongoDB.Password)
		} else {
			u.User = url.User(cfg.MongoDB.Username)
		}
	}
	if cfg.MongoDB.AuthSource != "" {
		q := u.Query()
		q.Set("authSource", cfg.MongoDB.AuthSource)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// redisUsername derives the Redis user from the config prefix: the prefix with
// its trailing char (the ":") removed, e.g. "trureact:" -> "trureact". This is
// the "current user" used both for the redis MCP server's REDIS_USERNAME and as
// the redis-cli --user argument (see spec/4-launch.md).
func redisUsername(cfg *opsConfig) string {
	user := cfg.Redis.Prefix
	if user != "" {
		user = user[:len(user)-1]
	}
	return user
}

// localBinPrefix is the PATH the ~/.local/bin wrapper scripts set so they can
// invoke the real CLI binaries by bare name without re-entering the wrappers
// themselves: /usr/bin on Linux, /opt/homebrew/bin/ on Mac (see
// spec/4-launch.md line 4).
func localBinPrefix() string {
	if runtime.GOOS == "darwin" {
		return "/opt/homebrew/bin/"
	}
	return "/usr/bin"
}

// setupServiceToolingFromConfig renders every companion CLI from the same
// post-login snapshot used for MCP generation. WHY: reading config separately
// can produce a Redis wrapper and MCP entry with different endpoints if login
// refreshes the file between the two operations. Every wrapper sets PATH to
// localBinPrefix() and invokes the real binary by bare name so it cannot
// re-enter ~/.local/bin; Milvus instead derives its interpreter from the exact
// global entry point.
func setupServiceToolingFromConfig(cfg *opsConfig) error {
	return setupServiceToolingFromConfigWithMilvusEntryPoint(cfg, globalMilvusCLIPath)
}

// setupServiceToolingFromConfigWithMilvusEntryPoint keeps the global
// implementation path explicit for regression fixtures while the production
// call fixes it outside ~/.local/bin. No caller may rediscover it through PATH.
func setupServiceToolingFromConfigWithMilvusEntryPoint(cfg *opsConfig, globalClientPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home for service tooling: %w", err)
	}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("create service wrapper directory: %w", err)
	}
	prefix := localBinPrefix()
	var wrapperErrors []error

	if cfg.S3.Host != "" {
		if err := writeServiceWrapper(binDir, "rclone", fmt.Sprintf(
			"#!/bin/bash\n"+
				"export PATH=%s\n"+
				"export RCLONE_CONFIG_S3_TYPE=s3\n"+
				"export RCLONE_CONFIG_S3_PROVIDER=SeaweedFS\n"+
				"export RCLONE_CONFIG_S3_ACCESS_KEY_ID='%s'\n"+
				"export RCLONE_CONFIG_S3_SECRET_ACCESS_KEY='%s'\n"+
				"export RCLONE_CONFIG_S3_ENDPOINT='http://%s:%d'\n"+
				"export RCLONE_CONFIG_S3_REGION=us-east-1\n"+
				"export RCLONE_CONFIG_WEB_TYPE=alias\n"+
				"export RCLONE_CONFIG_WEB_REMOTE=s3:%s\n"+
				"export RCLONE_CONFIG_DATA_TYPE=alias\n"+
				"export RCLONE_CONFIG_DATA_REMOTE=s3:%s\n"+
				"exec rclone \"$@\"\n",
			prefix, cfg.S3.Access.Key, cfg.S3.Secret.Key, cfg.S3.Host, cfg.S3.Port,
			cfg.S3.Bucket.Static, cfg.S3.Bucket.Data)); err != nil {
			wrapperErrors = append(wrapperErrors, err)
		}
	}
	if cfg.Postgres.Database != "" {
		if err := writeServiceWrapper(binDir, "psql", fmt.Sprintf(
			"#!/bin/bash\nexport PATH=%s\nexec psql \"%s\" \"$@\"\n",
			prefix, cfg.Postgres.URL)); err != nil {
			wrapperErrors = append(wrapperErrors, err)
		}
	}
	if cfg.Redis.URL != "" || cfg.Redis.Port != 0 {
		user := redisUsername(cfg)
		if err := writeServiceWrapper(binDir, "redis-cli", fmt.Sprintf(
			"#!/bin/bash\n"+
				"export PATH=%s\n"+
				"export REDISCLI_AUTH='%s'\n"+
				"exec redis-cli -h '%s' --user '%s' -p '%d' \"$@\"\n",
			prefix, cfg.Redis.Password, cfg.Redis.Service, user, cfg.Redis.Port)); err != nil {
			wrapperErrors = append(wrapperErrors, err)
		}
	}
	if cfg.Milvus.Host != "" {
		// WHY: the global path is outside ~/.local/bin, so the configured
		// milvus_cli wrapper cannot rediscover itself through PATH.
		if body, err := renderMilvusCliWrapper(globalClientPath, cfg); err != nil {
			wrapperErrors = append(wrapperErrors, fmt.Errorf("render milvus_cli wrapper: %w", err))
		} else {
			if err := writeServiceWrapper(binDir, "milvus_cli", body); err != nil {
				wrapperErrors = append(wrapperErrors, err)
			}
		}
	}
	return errors.Join(wrapperErrors...)
}

// renderMilvusCliWrapper renders the embedded milvus_cli.tmpl with the milvus
// config and the interpreter resolved from the exact globally installed
// `milvus_cli` entry point.
func renderMilvusCliWrapper(globalClientPath string, cfg *opsConfig) (string, error) {
	pythonVenv, err := milvusPythonInterpreter(globalClientPath)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("milvus_cli").Parse(milvusCliTemplate)
	if err != nil {
		return "", fmt.Errorf("parse milvus_cli template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct {
		PythonVenv string
		Host       string
		Port       int
		Token      string
		DbName     string
	}{
		PythonVenv: pythonVenv,
		Host:       cfg.Milvus.Host,
		Port:       cfg.Milvus.Port,
		Token:      cfg.Milvus.Token,
		DbName:     cfg.Milvus.DB.Name,
	}); err != nil {
		return "", fmt.Errorf("render milvus_cli template: %w", err)
	}
	return buf.String(), nil
}

// milvusPythonInterpreter returns the interpreter from the global
// milvus_cli shebang. WHY: scanning PATH is unsafe after the generated
// ~/.local/bin wrapper has taken precedence and can select the wrapper again.
func milvusPythonInterpreter(globalClientPath string) (string, error) {
	data, err := os.ReadFile(globalClientPath)
	if err != nil {
		return "", fmt.Errorf("read global milvus_cli at %s: %w", globalClientPath, err)
	}
	firstLine := string(data)
	if idx := strings.IndexByte(firstLine, '\n'); idx != -1 {
		firstLine = firstLine[:idx]
	}
	interpreter := strings.TrimSpace(strings.TrimPrefix(firstLine, "#!"))
	if interpreter == "" || interpreter == firstLine {
		return "", fmt.Errorf("global milvus_cli at %s has no interpreter shebang", globalClientPath)
	}
	if !filepath.IsAbs(interpreter) {
		return "", fmt.Errorf("global milvus_cli at %s uses non-absolute interpreter", globalClientPath)
	}
	return interpreter, nil
}

// writeServiceWrapper atomically installs one regular executable into binDir.
// WHY: os.WriteFile follows an existing symlink, which can overwrite a
// package-managed uv target when replacing the old milvus_cli installation.
func writeServiceWrapper(binDir, name, body string) (err error) {
	path := filepath.Join(binDir, name)
	temp, err := os.CreateTemp(binDir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary %s wrapper: %w", name, err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if _, err := temp.WriteString(body); err != nil {
		return fmt.Errorf("write temporary %s wrapper: %w", name, err)
	}
	if err := temp.Chmod(0755); err != nil {
		return fmt.Errorf("chmod temporary %s wrapper: %w", name, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary %s wrapper: %w", name, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("install %s wrapper: %w", name, err)
	}
	log.Printf("Configured %s wrapper at %s", name, path)
	return nil
}

// getPgidFile returns the path to the pgid file inside WorkbenchDir
func getPgidFile() string {
	return filepath.Join(WorkbenchDir, "pgid")
}

// getCurrentFile returns the path to the current app name file inside WorkbenchDir
func getCurrentFile() string {
	return filepath.Join(WorkbenchDir, "current")
}

// writeCurrentApp writes the current app name to the current file and workspace config
func writeCurrentApp(name string) error {
	if err := os.WriteFile(getCurrentFile(), []byte(name), 0644); err != nil {
		return err
	}
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		return err
	}
	wsCfg.Current = name
	return saveWorkspaceConfig(wsCfg)
}

// readCurrentApp reads the current app name from the current file
func readCurrentApp() (string, error) {
	data, err := os.ReadFile(getCurrentFile())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func processGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// managedRuntimeHealthyForApp identifies only a Trustable-owned runtime: the
// durable current/pgid markers must agree with the requested app, the process
// group must still exist, and both shared listeners must answer. Port checks
// alone are deliberately insufficient because an unrelated listener must be
// reclaimed instead of being reused as a successful launch.
func managedRuntimeHealthyForApp(app string, leftPort, rightPort int) bool {
	current, err := readCurrentApp()
	if err != nil || current != app {
		return false
	}
	pgid, err := readPgid()
	if err != nil || !processGroupAlive(pgid) {
		return false
	}
	return isPortListening(leftPort) && isPortListening(rightPort)
}

// canonicalWorkbenchPath returns the path OpenCode uses to store project and
// session state. In the pod /home/trustable/workbench may be a symlink into the
// persistent workspace volume, so resolve the parent even before the app
// checkout exists.
func canonicalWorkbenchPath(app string) (string, error) {
	workbenchPath, err := filepath.Abs(filepath.Join(WorkbenchDir, app))
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(workbenchPath); err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(workbenchPath)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolvedParent, filepath.Base(workbenchPath)), nil
	}
	return workbenchPath, nil
}

// removeCurrentFile removes the current app name file and clears it from workspace config
func removeCurrentFile() {
	os.Remove(getCurrentFile())
	wsCfg, err := loadWorkspaceConfig()
	if err == nil {
		wsCfg.Current = ""
		saveWorkspaceConfig(wsCfg)
	}
}

// isPortFree checks if a port is available for use
func isPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// reclaimPort forcefully frees a TCP port by killing whatever is listening on
// it. This is the fallback for orphaned launches: when a prior opencode/devel
// process still holds 4096/5173 but its pgid file is gone (so
// terminateLeftoverProcesses found nothing to kill), the port-free check would
// otherwise wedge the launcher. Best-effort; returns true if the port ended up
// free. Uses `lsof` to find the listeners by port, independent of any pgid file.
func reclaimPort(port int) bool {
	out, err := exec.Command("lsof", "-nP", "-tiTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			pid, convErr := strconv.Atoi(strings.TrimSpace(line))
			if convErr != nil || pid <= 0 {
				continue
			}
			log.Printf("reclaimPort: killing orphaned pid %d holding port %d", pid, port)
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	// Give the OS a moment to release the socket, then confirm.
	for i := 0; i < 8; i++ {
		if isPortFree(port) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return isPortFree(port)
}

// isPortListening checks if a port is accepting connections
func isPortListening(port int) bool {
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("%s:%d", localLoopbackHost, port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// waitForPort waits for a port to start accepting connections
func waitForPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isPortListening(port) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("port %d not listening after %v", port, timeout)
}

// readPgid reads the process group ID from the pgid file
func readPgid() (int, error) {
	data, err := os.ReadFile(getPgidFile())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// writePgid writes the process group ID to the pgid file
func writePgid(pgid int) error {
	return os.WriteFile(getPgidFile(), []byte(strconv.Itoa(pgid)), 0644)
}

// removePgidFile removes the pgid file
func removePgidFile() {
	os.Remove(getPgidFile())
}

// killPgid forcefully terminates a process group by its pgid
func killPgid(pgid int) error {
	// Kill the entire process group (negative pgid)
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	if err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

// terminateLeftoverProcesses checks for and kills any leftover process group.
// It handles two cases of stale state from a previous launch:
//  1. A pgid file pointing at a still-running process group — kill it by pgid.
//  2. No pgid file, but an orphaned truacp/devel still holding 4096/5173 (the
//     pgid file was removed while the process kept running) — reclaim the ports
//     directly so the upcoming port-free check doesn't wedge the launch.
//
// A `current` file lingering without a pgid file is itself stale, so it is
// cleared too.
func terminateLeftoverProcesses() {
	pgid, err := readPgid()
	if err != nil {
		// No pgid file: nothing to kill by group, but a prior process may still
		// be holding the ports. Reclaim them and clear the stale current marker.
		if isPortListening(truacpPort) || isPortListening(opsdevelPort) {
			log.Printf("No pgid file but ports busy; reclaiming orphaned listeners")
			reclaimPort(truacpPort)
			reclaimPort(opsdevelPort)
		}
		removeCurrentFile()
		return
	}

	log.Printf("Found leftover pgid file with pgid %d, terminating...", pgid)

	if err := killPgid(pgid); err != nil {
		log.Printf("Warning: failed to kill process group %d: %s", pgid, err)
	}

	removePgidFile()
	removeCurrentFile()

	// Wait a bit for ports to be freed
	time.Sleep(500 * time.Millisecond)
}

// waitForProcessStart waits for a process to either exit (error) or stay running for the specified duration
// Returns nil if process stays running, error if it exits prematurely
func waitForProcessStart(cmd *exec.Cmd, duration time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		// Process exited within the duration - this is an error
		if err != nil {
			return fmt.Errorf("process exited with error: %w", err)
		}
		return fmt.Errorf("process exited unexpectedly")
	case <-time.After(duration):
		// Process is still running after the duration - success
		return nil
	}
}

// ensureRequiredWorkbenchFolders scaffolds the folders every app must have
// (spec/4-launch.md "ensure required folders"). It creates packages/.gitkeep
// when packages/ is missing, and seeds web/ (index.html from the embedded
// template plus favicon.ico and trustable-head.png) when web/ does not already
// exist — an app shipping its own web/ is left untouched. Any created files are
// staged and committed with a fixed message. Best-effort: failures are logged,
// never fatal to a launch.
func ensureRequiredWorkbenchFolders(workbenchPath string) {
	var created []string

	// packages/.gitkeep
	packagesDir := filepath.Join(workbenchPath, "packages")
	if _, err := os.Stat(packagesDir); os.IsNotExist(err) {
		if err := os.MkdirAll(packagesDir, 0755); err != nil {
			log.Printf("ensureRequiredWorkbenchFolders: failed to create packages/: %s", err)
		} else if err := os.WriteFile(filepath.Join(packagesDir, ".gitkeep"), []byte{}, 0644); err != nil {
			log.Printf("ensureRequiredWorkbenchFolders: failed to write packages/.gitkeep: %s", err)
		} else {
			created = append(created, "packages/.gitkeep")
		}
	}

	// web/ — only seed when the folder does not already exist.
	webDir := filepath.Join(workbenchPath, "web")
	if _, err := os.Stat(webDir); os.IsNotExist(err) {
		if err := os.MkdirAll(webDir, 0755); err != nil {
			log.Printf("ensureRequiredWorkbenchFolders: failed to create web/: %s", err)
		} else {
			// index.html from the embedded template, plus the assets it references.
			seeds := map[string]string{
				"index.html":         "web/template.html",
				"favicon.ico":        "web/favicon.ico",
				"trustable-head.png": "web/trustable-head.png",
			}
			for dest, src := range seeds {
				data, err := embeddedWeb.ReadFile(src)
				if err != nil {
					log.Printf("ensureRequiredWorkbenchFolders: failed to read embedded %s: %s", src, err)
					continue
				}
				if err := os.WriteFile(filepath.Join(webDir, dest), data, 0644); err != nil {
					log.Printf("ensureRequiredWorkbenchFolders: failed to write web/%s: %s", dest, err)
					continue
				}
				created = append(created, "web/"+dest)
			}
		}
	}

	if len(created) == 0 {
		return
	}

	if err := ensureGitIdentity(workbenchPath); err != nil {
		log.Printf("ensureRequiredWorkbenchFolders: %s", err)
		return
	}
	addArgs := append([]string{"add"}, created...)
	addCmd := exec.Command("git", addArgs...)
	addCmd.Dir = workbenchPath
	if output, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("ensureRequiredWorkbenchFolders: git add failed: %s", string(output))
		return
	}
	commitCmd := exec.Command("git", "commit", "-m", "adding required web and packages")
	commitCmd.Dir = workbenchPath
	if output, err := commitCmd.CombinedOutput(); err != nil {
		log.Printf("ensureRequiredWorkbenchFolders: git commit failed: %s", string(output))
		return
	}
	log.Printf("ensureRequiredWorkbenchFolders: scaffolded and committed %v", created)
}

func workspaceRepoExists(workspacePath string) bool {
	if _, err := os.Stat(filepath.Join(workspacePath, "config")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(workspacePath, ".git", "config")); err == nil {
		return true
	}
	return false
}

func ensureNodeDependencies(workbenchPath, app string) {
	if _, err := os.Stat(filepath.Join(workbenchPath, "package.json")); err != nil {
		return
	}
	if _, err := os.Stat(filepath.Join(workbenchPath, "node_modules")); err == nil {
		return
	}
	log.Printf("Running npm install in workbench/%s...", app)
	npmCmd := exec.Command("npm", "install")
	npmCmd.Dir = workbenchPath
	if output, err := npmCmd.CombinedOutput(); err != nil {
		log.Printf("Warning: npm install failed: %s, output: %s", err, string(output))
	}
}

func restoreMissingWorkbenchCheckouts() {
	workspaceRoot := filepath.Join(WorkspaceDir, "workspace")
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: failed to read workspace repos for workbench restore: %s", err)
		}
		return
	}
	if err := os.MkdirAll(WorkbenchDir, 0755); err != nil {
		log.Printf("Warning: failed to create workbench dir for restore: %s", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		app := entry.Name()
		if !namePattern.MatchString(app) {
			continue
		}
		workspacePath := filepath.Join(workspaceRoot, app)
		if !workspaceRepoExists(workspacePath) {
			continue
		}
		workbenchPath, _ := filepath.Abs(filepath.Join(WorkbenchDir, app))
		if _, err := os.Stat(workbenchPath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			log.Printf("Warning: failed to inspect workbench/%s: %s", app, err)
			continue
		}
		log.Printf("Restoring missing workbench/%s from workspace/%s...", app, app)
		cloneCmd := exec.Command("git", "clone", workspacePath, workbenchPath)
		if output, err := cloneCmd.CombinedOutput(); err != nil {
			log.Printf("Warning: failed to restore workbench/%s: %s, output: %s", app, err, string(output))
			continue
		}
		// The restored clone predates the managed block whenever the workspace
		// repo does, so ignore the generated files before writing .env.
		ensureWorkbenchGitignore(workbenchPath)
		if err := generateAppEnvFiles(app); err != nil {
			log.Printf("Warning: failed to generate restored workbench .env for %s: %s", app, err)
		}
	}
}

// handleLaunchGet handles GET /api/launch/<app>
const launchProgressTotal = 8

func prepareLaunchResponseWriter(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, bool) {
	return prepareProgressResponseWriter(w, r, launchProgressTotal, "launch progress streaming is not supported")
}

func reportLaunchProgress(w http.ResponseWriter, stage int, message string) {
	reportProgress(w, stage, message)
}

func handleLaunchGet(w http.ResponseWriter, r *http.Request, app string) {
	var ok bool
	w, ok = prepareLaunchResponseWriter(w, r)
	if !ok {
		return
	}
	if _, streaming := w.(*progressSSEResponseWriter); !streaming {
		w.Header().Set("Content-Type", "application/json")
	}

	reportLaunchProgress(w, 1, "Preparing workspace...")

	// Validate app name format
	if !namePattern.MatchString(app) {
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid app name format"})
		return
	}

	// Check if workspace folder exists
	workspacePath := filepath.Join(WorkspaceDir, "workspace", app)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("App folder not found: %s/workspace/%s", WorkspaceDir, app)})
		return
	}

	// A second tab can reach this point only after the first launch releases the
	// lifecycle lock. Reuse its healthy process group instead of redeploying the
	// same app and treating the legitimate :4096 listener as a port collision.
	if managedRuntimeHealthyForApp(app, truacpPort, opsdevelPort) {
		log.Printf("Runtime for %s is already healthy; reusing ports %d and %d", app, truacpPort, opsdevelPort)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"left":         truacpPort,
			"right":        opsdevelPort,
			"skills_added": false,
		})
		return
	}

	// Terminate leftover processes
	terminateLeftoverProcesses()

	// Clone to workbench if not already present
	// Use absolute path so child processes (Vite) resolve watches correctly
	workbenchPath, err := canonicalWorkbenchPath(app)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to resolve workbench path: %s", err)})
		return
	}
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		log.Printf("Cloning workspace/%s to workbench/%s...", app, app)
		if err := os.MkdirAll(WorkbenchDir, 0755); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to create workbench dir: %s", err)})
			return
		}
		cloneCmd := exec.Command("git", "clone", workspacePath, workbenchPath)
		if output, err := cloneCmd.CombinedOutput(); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to clone to workbench: %s", string(output))})
			return
		}

		// A repo cloned from anywhere declares its variables in .env.dist. Seed the
		// ones this installation has no value for as empty config entries, so they
		// surface as blank rows in the env editor instead of being invisible.
		if seeded, err := seedMissingEnvKeys(app); err != nil {
			log.Printf("Warning: failed to seed env keys from .env.dist: %s", err)
		} else if len(seeded) > 0 {
			log.Printf("Seeded %d env keys from .env.dist for %s: %v", len(seeded), app, seeded)
		}

		// Generate .env and .env.production from config
		if err := generateAppEnvFiles(app); err != nil {
			log.Printf("Warning: failed to generate workbench .env: %s", err)
		}

		ensureNodeDependencies(workbenchPath, app)

		log.Printf("Workbench for %s set up successfully", app)
	} else {
		log.Printf("Workbench for %s already exists, reusing", app)
		// On the reuse path, regenerate the .env files to keep them in sync with
		// the current config (spec/4-launch.md). A restored checkout may not yet
		// have node_modules, so install dependencies when package.json exists and
		// node_modules is absent.
		if err := generateAppEnvFiles(app); err != nil {
			log.Printf("Warning: failed to regenerate workbench .env: %s", err)
		}
		ensureNodeDependencies(workbenchPath, app)
	}

	// Ensure the required folders exist (packages/, web/) after checkout.
	ensureRequiredWorkbenchFolders(workbenchPath)

	// Before any generator runs: .mcp.json and friends must already be ignored
	// when they are written, otherwise they land as untracked files that a
	// later commit picks up and that revert's `git clean -fd` deletes.
	ensureWorkbenchGitignore(workbenchPath)

	// Refresh the shared pool before the gate below: a variable that a producing
	// app's .env.shared can satisfy must not block the launch. Service
	// credentials are regenerated on every ops ide login, so resolving here —
	// rather than only when the picker saves — is what stops a consumer being
	// handed a stale secret. Logs back in as this app afterwards. Non-fatal.
	refreshSharedPool(app)

	// Gate: a variable the repo declares in .env.dist but that has no development
	// value cannot be supplied later — the app would deploy and fail at runtime.
	// Abort before ops ide login so nothing is touched on the cluster, and hand
	// the frontend the key list so it can open the env editor on them.
	// Development values only: production is a publish-time concern.
	if missing, err := missingAppEnvKeys(app); err != nil {
		log.Printf("Warning: failed to check missing env keys for %s: %s", app, err)
	} else if len(missing) > 0 {
		log.Printf("Launch of %s blocked, missing env values: %v", app, missing)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":       "Missing required environment variables",
			"missing_env": missing,
		})
		return
	}

	// Skills remain project-local rather than being baked into Pi's global state,
	// so each generated app carries the capabilities appropriate to its repo.
	skillsAdded := ensureSkills(app)

	reportLaunchProgress(w, 2, "Checking application account...")
	// Ensure the OpenWhisk user exists and password is in sync
	log.Printf("Checking OpenWhisk user for %s...", app)
	cfg, err := loadTrustableConfig()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to load config: %s", err)})
		return
	}
	// Configure owns Pi's global provider/model state. Edit intentionally avoids
	// repairing it here. Keep an API-side guard as well as the applist redirect
	// so a stale/direct browser tab cannot launch an unconfigured Pi runtime.
	if piDefaultModel(cfg) == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":          "Pi model is not configured",
			"setup_required": true,
		})
		return
	}
	storedPassword := ""
	if cfg.Apps != nil && cfg.Apps[app] != nil {
		storedPassword = cfg.Apps[app].Password
	}

	kubegetCmd := exec.Command("ops", "util", "kubeget", "whiskuser/"+app, ".spec.password")
	kubegetOutput, kubegetErr := kubegetCmd.Output()
	if kubegetErr != nil {
		// User doesn't exist: recreate it using the password from the workbench
		// .env (OPS_PASSWORD), which was (re)generated above. Fall back to the
		// stored config password only if .env has none.
		recreatePassword := parseEnvFile(filepath.Join(workbenchPath, ".env"))["OPS_PASSWORD"]
		if recreatePassword == "" {
			recreatePassword = storedPassword
		}
		if recreatePassword == "" {
			json.NewEncoder(w).Encode(map[string]string{"error": "No password in .env or config for user " + app + ", cannot recreate"})
			return
		}
		log.Printf("User %s not found, creating with password from .env...", app)
		email := app + "@n7s.co"
		addUserCmd := exec.Command("ops", "admin", "adduser", app, email, recreatePassword, "--all")
		if output, err := addUserCmd.CombinedOutput(); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to create user: %s", string(output))})
			return
		}
		log.Printf("User %s created successfully", app)
	} else {
		// User exists, check if password matches
		remotePassword := strings.TrimSpace(string(kubegetOutput))
		if remotePassword != storedPassword && remotePassword != "" {
			log.Printf("Password mismatch for %s, updating stored password", app)
			wsCfg, err := loadWorkspaceConfig()
			if err == nil {
				if wsCfg.Apps == nil {
					wsCfg.Apps = make(map[string]*AppConfig)
				}
				if wsCfg.Apps[app] == nil {
					wsCfg.Apps[app] = &AppConfig{
						Development: make(map[string]string),
						Production:  make(map[string]string),
					}
				}
				wsCfg.Apps[app].Password = remotePassword
				if err := saveWorkspaceConfig(wsCfg); err != nil {
					log.Printf("Warning: failed to update stored password: %s", err)
				}
				// Regenerate .env with updated password
				if err := generateAppEnvFiles(app); err != nil {
					log.Printf("Warning: failed to regenerate .env after password update: %s", err)
				}
			}
		}
	}

	reportLaunchProgress(w, 3, "Connecting to OpenServerless...")
	// Run ops ide login (always, even when reusing workbench)
	log.Printf("Running ops ide login for %s...", app)
	// ops ide login merges into the single global ~/.ops/config.json, so a
	// previous app's service blocks would survive and be indistinguishable from
	// this app's — a wrong service binding, not noise. See shared.go.
	removeOpsConfig()
	loginCmd := exec.Command("ops", "ide", "login")
	loginCmd.Dir = workbenchPath
	if output, err := loginCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide login for %s failed: %s, output: %s", app, err, string(output))
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide login failed: %s", string(output))})
		return
	}
	log.Printf("ops ide login for %s completed successfully", app)

	// ops ide login (re)writes ~/.ops/config.json with the app user's service
	// bindings. The project MCP generation below reads that file, so log
	// which service blocks landed — a missing block here is exactly why an MCP
	// server would be skipped or misconfigured (spec/4-launch.md).
	logOpsServiceBlocks(app)
	if err := generateAppEnvFiles(app); err != nil {
		log.Printf("Warning: failed to regenerate .env after ops ide login: %s", err)
	}

	reportLaunchProgress(w, 4, "Cleaning previous build...")
	// Run ops ide clean
	log.Printf("Running ops ide clean for %s...", app)
	cleanCmd := exec.Command("ops", "ide", "clean")
	cleanCmd.Dir = workbenchPath
	if output, err := cleanCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide clean for %s failed: %s, output: %s", app, err, string(output))
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide clean failed: %s", string(output))})
		return
	}
	log.Printf("ops ide clean for %s completed successfully", app)

	reportLaunchProgress(w, 5, "Deploying application...")
	// Run ops ide deploy
	log.Printf("Running ops ide deploy for %s...", app)
	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workbenchPath
	deployCmd.Env = appServiceRuntimeEnv(os.Environ())
	if output, err := deployCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide deploy for %s failed: %s, output: %s", app, err, string(output))
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide deploy failed: %s", string(output))})
		return
	}
	log.Printf("ops ide deploy for %s completed successfully", app)

	// Detect ports
	leftPort := truacpPort
	rightPort := opsdevelPort

	// Check if ports are free. If a port is held — typically by an orphaned
	// truacp/devel from a prior launch whose pgid file is gone, so
	// terminateLeftoverProcesses couldn't clean it up — reclaim it by killing
	// the listener directly before giving up.
	if !isPortFree(leftPort) && !reclaimPort(leftPort) {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Port %d (truacp) is not available", leftPort)})
		return
	}
	if !isPortFree(rightPort) && !reclaimPort(rightPort) {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Port %d (opsdevel) is not available", rightPort)})
		return
	}

	// Generate MCP entries and companion CLIs from one post-login snapshot.
	// WHY: service endpoints are an atomic platform contract; two reads can
	// drift when ops refreshes ~/.ops/config.json during launch.
	serviceConfig, err := loadOpsConfig()
	if err != nil {
		reportLaunchProgress(w, 6, "Preparing agent tools...")
		log.Printf("Failed to load post-login service configuration: %s", err)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to load post-login service configuration: %s", err),
		})
		return
	}

	// Generate the project assets consumed by Pi: standard .mcp.json,
	// AGENTS.md, the OpenServerless contract, and local checkers.
	// Provider/model configuration is global under ~/.pi/agent.
	if err := generateProjectAssetsInDir(workbenchPath, buildMCPFromOpsConfig(serviceConfig)); err != nil {
		log.Printf("Failed to generate project assets: %s", err)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to generate project assets: %s", err),
		})
		return
	}
	// Link CLAUDE.md -> AGENTS.md and .claude -> .agents so every agent shares
	// one configuration. Runs here because AGENTS.md must exist first, and
	// because creating .agents before ensureSkills would suppress skills setup.
	ensureAgentConfigLinks(workbenchPath)
	// Commit the launch-owned content that belongs in the repo: the lock file
	// npm install produced earlier and the freshly written AGENTS.md. Last,
	// because only now are both in their final state.
	commitLaunchProjectFiles(workbenchPath)
	// Configure the CLI tooling (rclone, psql, redis-cli) that accompanies the
	// MCP servers generated above from ~/.ops/config.json. WHY: these wrappers
	// are one launch contract with the MCP entries; continuing after a required
	// wrapper fails would expose tooling Pi cannot actually use.
	if err := setupServiceToolingFromConfig(serviceConfig); err != nil {
		log.Printf("Service tooling configuration failed: %s", err)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("service tooling configuration failed: %s", err),
		})
		return
	}
	browserURL, err := browserVisibleDevelopmentURL(r)
	if err != nil {
		log.Printf("Trustable Pi development URL resolution failed: %s", err)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to resolve browser-visible development URL: %s", err),
		})
		return
	}
	extensionPath, err := trustablePiExtensionPath()
	if err != nil {
		log.Printf("Trustable Pi extension validation failed: %s", err)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to validate Trustable Pi extension: %s", err),
		})
		return
	}
	watcherLogPath, err := opsDevelLogPath(app)
	if err != nil {
		log.Printf("Trustable watcher log path resolution failed: %s", err)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to resolve ops ide devel log: %s", err),
		})
		return
	}
	initialWatcherLog, err := openRotatingRuntimeLog(watcherLogPath)
	if err != nil {
		log.Printf("Trustable watcher log initialization failed: %s", err)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to initialize ops ide devel log: %s", err),
		})
		return
	}
	// WHY: create and protect the host-owned log before Pi validates the
	// manifest, so managed mode never starts with a declared but absent source.
	fmt.Fprintf(initialWatcherLog, "\n[trustable] preparing ops ide devel for %s at %s\n", app, time.Now().Format(time.RFC3339))
	initialWatcherLog.Close()
	runtimeManifestPath, err := writeTrustablePiRuntimeManifest(app, workbenchPath, browserURL, watcherLogPath)
	if err != nil {
		log.Printf("Trustable Pi runtime manifest generation failed: %s", err)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to generate Trustable Pi runtime manifest: %s", err),
		})
		return
	}
	notebookEnv, err := notebookRuntimeEnvironment(app)
	if err != nil {
		log.Printf("Trustable notebook configuration failed: %s", err)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to prepare notebook configuration: %s", err),
		})
		return
	}

	reportLaunchProgress(w, 7, "Starting coding services...")
	// Start truacp. It serves its React UI on :4096 and owns the ACP session,
	// spawning pi-acp (and therefore Pi) in the selected workbench directory.
	// Trustable does not bootstrap or rewrite an agent session URL.
	log.Printf("Starting truacp for %s on port %d...", app, leftPort)
	truacpCmd := exec.Command("truacp", "--port", strconv.Itoa(leftPort), "--dir", workbenchPath)
	truacpCmd.Dir = workbenchPath
	truacpCmd.Stdout = os.Stdout
	truacpCmd.Stderr = os.Stderr
	truacpCmd.Env = appServiceRuntimeEnv(os.Environ())
	appEnv := parseEnvFile(filepath.Join(workbenchPath, ".env"))
	for key, value := range appEnv {
		// Service bindings are reconstructed from the post-login ops config by
		// appServiceRuntimeEnv; accepting duplicates from .env could select stale
		// credentials after an app switch.
		if isServiceRuntimeEnvKey(key) {
			continue
		}
		truacpCmd.Env = append(truacpCmd.Env, key+"="+value)
	}
	// TruACP also supports standalone use, where its local endpoint form and Pi
	// update notice are appropriate. Append these managed-runtime controls last
	// so neither the inherited environment nor an application .env can override
	// Trustable's ownership of configuration and dependency updates.
	managedEnv := []string{
		"TRUSTABLE_MANAGED_RUNTIME=1",
		"TRUSTABLE_RUNTIME_CONFIG="+runtimeManifestPath,
		"TRUSTABLE_PI_EXTENSION_PATH="+extensionPath,
		"PI_SKIP_VERSION_CHECK=1",
	}
	managedEnv = append(managedEnv, notebookEnv...)
	truacpCmd.Env = appendEnvironmentOverrides(truacpCmd.Env, managedEnv...)
	// Keep truacp, pi-acp, Pi and ops ide devel in one process group.
	truacpCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Log exactly what we are about to run so failures are visible in the air console.
	if bin, lookErr := exec.LookPath("truacp"); lookErr != nil {
		log.Printf("truacp: WARNING `truacp` not found in PATH: %s", lookErr)
	} else {
		log.Printf("truacp: binary=%s", bin)
	}
	log.Printf("truacp: cmd=`%s`", strings.Join(truacpCmd.Args, " "))
	log.Printf(
		"truacp: dir=%s mcp=%s runtime=%s development=%s appEnvCount=%d",
		workbenchPath,
		filepath.Join(workbenchPath, ".mcp.json"),
		runtimeManifestPath,
		browserURL,
		len(appEnv),
	)

	if err := truacpCmd.Start(); err != nil {
		log.Printf("truacp: FAILED to start: %s", err)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to start truacp: %s", err)})
		return
	}

	log.Printf("truacp: started pid=%d in directory: %s", truacpCmd.Process.Pid, workbenchPath)

	// Get the process group ID
	pgid, err := syscall.Getpgid(truacpCmd.Process.Pid)
	if err != nil {
		truacpCmd.Process.Kill()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to get process group: %s", err)})
		return
	}

	// Check truacp doesn't terminate within 0.5 seconds.
	truacpExited := make(chan error, 1)
	go func() {
		truacpExited <- truacpCmd.Wait()
	}()

	select {
	case err := <-truacpExited:
		// Process exited within 0.5 seconds - this is an error
		errMsg := "truacp exited unexpectedly"
		if err != nil {
			errMsg = fmt.Sprintf("truacp exited with error: %s", err)
		}
		log.Printf("truacp: %s (see truacp stdout/stderr above for the cause)", errMsg)
		json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	case <-time.After(500 * time.Millisecond):
		// Process is still running - continue
		log.Printf("truacp: pid=%d still alive after 500ms, serving on port %d", truacpCmd.Process.Pid, leftPort)
	}

	// Write pgid and current app name to files
	if err := writePgid(pgid); err != nil {
		killPgid(pgid)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to write pgid file: %s", err)})
		return
	}
	if err := writeCurrentApp(app); err != nil {
		log.Printf("Warning: failed to write current app file: %s", err)
	}

	// Start ops ide devel in the same process group
	log.Printf("Starting ops ide devel for %s on port %d...", app, rightPort)
	develLog, err := openRotatingRuntimeLog(watcherLogPath)
	if err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to open ops ide devel log: %s", err)})
		return
	}
	fmt.Fprintf(develLog, "[trustable] starting managed watcher at %s\n", time.Now().Format(time.RFC3339))
	develCmd := exec.Command("sh", "-c", fmt.Sprintf("cd %q && ops ide devel", workbenchPath))
	develOutput := io.MultiWriter(os.Stdout, develLog)
	develCmd.Stdout = develOutput
	develCmd.Stderr = develOutput
	develCmd.Env = appServiceRuntimeEnv(os.Environ())
	// Join the same process group as truacp.
	develCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}

	if err := develCmd.Start(); err != nil {
		fmt.Fprintf(develLog, "[trustable] watcher failed to start: %s\n", err)
		develLog.Close()
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to start ops ide devel: %s", err)})
		return
	}

	// Check ops ide devel doesn't terminate within 0.5 seconds
	develExited := make(chan error, 1)
	go func() {
		waitErr := develCmd.Wait()
		fmt.Fprintf(develLog, "[trustable] watcher exited at %s: %v\n", time.Now().Format(time.RFC3339), waitErr)
		develLog.Close()
		develExited <- waitErr
	}()

	select {
	case err := <-develExited:
		// Process exited within 0.5 seconds - this is an error
		errMsg := "ops ide devel exited unexpectedly"
		if err != nil {
			errMsg = fmt.Sprintf("ops ide devel exited with error: %s", err)
		}
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	case <-time.After(500 * time.Millisecond):
		// Process is still running - continue
	}

	reportLaunchProgress(w, 8, "Waiting for services to become ready...")
	// Wait for both ports to be listening
	log.Printf("Waiting for ports %d and %d to be listening...", leftPort, rightPort)
	if err := waitForPort(leftPort, 30*time.Second); err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("truacp failed to start listening: %s", err)})
		return
	}
	if err := waitForPort(rightPort, 30*time.Second); err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide devel failed to start listening: %s", err)})
		return
	}

	log.Printf("Services for %s started - truacp on port %d, opsdevel on port %d", app, leftPort, rightPort)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"left":         leftPort,
		"right":        rightPort,
		"skills_added": skillsAdded,
	})
}

// handleLaunchDelete handles DELETE /api/launch
func handleLaunchDelete(w http.ResponseWriter, r *http.Request) {
	pgid, err := readPgid()
	if err != nil {
		// No pgid file - nothing running
		w.WriteHeader(http.StatusNoContent)
		return
	}

	log.Printf("Terminating process group %d...", pgid)
	if err := killPgid(pgid); err != nil {
		log.Printf("Warning: failed to kill process group %d: %s", pgid, err)
	}

	removePgidFile()
	removeCurrentFile()
	log.Printf("Process group %d terminated and pgid file removed", pgid)

	w.WriteHeader(http.StatusNoContent)
}

// findDevelPids finds PIDs in the process group that are ops ide devel or vite related
func findDevelPids(pgid int) ([]int, error) {
	// List all PIDs in the process group
	pgrepCmd := exec.Command("pgrep", "-g", strconv.Itoa(pgid))
	output, err := pgrepCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pgrep failed: %w", err)
	}

	var develPids []int
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			continue
		}

		// Check command line for this PID
		psCmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=")
		psOutput, err := psCmd.Output()
		if err != nil {
			continue
		}
		cmdLine := string(psOutput)

		// Match ops ide devel, vite, or node processes on port 5173
		if strings.Contains(cmdLine, "ops ide devel") ||
			strings.Contains(cmdLine, "vite") ||
			strings.Contains(cmdLine, "5173") {
			develPids = append(develPids, pid)
		}
	}
	return develPids, nil
}

// waitForHTTP waits for an HTTP server to respond to HEAD requests
func waitForHTTP(port int, timeout time.Duration) error {
	client := &http.Client{Timeout: 1 * time.Second}
	url := fmt.Sprintf("http://%s:%d/", localLoopbackHost, port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Head(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("http://%s:%d not responding after %v", localLoopbackHost, port, timeout)
}

// waitForPortFree waits for a port to stop accepting connections
func waitForPortFree(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isPortListening(port) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("port %d still listening after %v", port, timeout)
}

// handleRedeploy handles GET /api/redeploy?name=<app> - restarts ops ide devel, streaming progress via SSE
func handleRedeploy(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}

	unlock := lockRuntimeLifecycle("redeploy " + name)
	defer unlock()

	req := struct{ Name string }{Name: name}

	workbenchPath, err := canonicalWorkbenchPath(req.Name)
	if err != nil {
		http.Error(w, "Failed to resolve workbench path", http.StatusInternalServerError)
		return
	}
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		http.Error(w, "Workbench not found", http.StatusNotFound)
		return
	}

	pgid, err := readPgid()
	if err != nil {
		http.Error(w, "No running session found", http.StatusBadRequest)
		return
	}

	// Stream progress as text/event-stream
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func(event, data string) {
		fmt.Fprintf(w, "event: %s\n", event)
		for _, line := range strings.Split(data, "\n") {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprintf(w, "\n")
		flusher.Flush()
	}

	// Step 1: Terminate ops ide devel
	send("status", "Terminating ops ide devel...")
	log.Printf("Redeploy: finding devel PIDs in process group %d...", pgid)
	develPids, _ := findDevelPids(pgid)

	if len(develPids) > 0 {
		for _, pid := range develPids {
			syscall.Kill(pid, syscall.SIGTERM)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			allDead := true
			for _, pid := range develPids {
				if err := syscall.Kill(pid, 0); err == nil {
					allDead = false
					break
				}
			}
			if allDead {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		for _, pid := range develPids {
			if err := syscall.Kill(pid, 0); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}

	// Step 2: Wait for port free
	send("status", "Waiting for port 5173 to be free...")
	if err := waitForPortFree(opsdevelPort, 10*time.Second); err != nil {
		send("error", fmt.Sprintf("Port %d did not free up: %s", opsdevelPort, err))
		return
	}

	// Step 3: Deploy actions
	send("status", "Deploying actions (ops ide deploy)...")
	log.Printf("Redeploy: running ops ide deploy for %s...", req.Name)
	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workbenchPath
	deployCmd.Env = appServiceRuntimeEnv(os.Environ())
	if deployOutput, err := deployCmd.CombinedOutput(); err != nil {
		send("error", fmt.Sprintf("ops ide deploy failed: %s\n%s", err, string(deployOutput)))
		return
	}
	log.Printf("Redeploy: ops ide deploy completed for %s", req.Name)

	// Step 4: Get action list
	send("status", "Getting action list...")
	actionCmd := exec.Command("ops", "action", "list")
	actionCmd.Dir = workbenchPath
	actionOutput, err := actionCmd.CombinedOutput()
	actionList := string(actionOutput)
	if err != nil {
		actionList = fmt.Sprintf("(ops action list failed: %s)\n%s", err, actionList)
	}
	log.Printf("Redeploy: action list:\n%s", actionList)

	// Step 5: Start ops ide devel --fast
	send("status", "Starting dev server (ops ide devel --fast)...")
	watcherLogPath, err := opsDevelLogPath(req.Name)
	if err != nil {
		send("error", fmt.Sprintf("Failed to resolve ops ide devel log: %s", err))
		return
	}
	develLog, err := openRotatingRuntimeLog(watcherLogPath)
	if err != nil {
		send("error", fmt.Sprintf("Failed to open ops ide devel log: %s", err))
		return
	}
	fmt.Fprintf(develLog, "[trustable] restarting managed watcher with --fast at %s\n", time.Now().Format(time.RFC3339))
	develCmd := exec.Command("sh", "-c", fmt.Sprintf("cd %q && ops ide devel --fast", workbenchPath))
	develOutput := io.MultiWriter(os.Stdout, develLog)
	develCmd.Stdout = develOutput
	develCmd.Stderr = develOutput
	develCmd.Env = appServiceRuntimeEnv(os.Environ())
	develCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}

	if err := develCmd.Start(); err != nil {
		fmt.Fprintf(develLog, "[trustable] watcher failed to restart: %s\n", err)
		develLog.Close()
		send("error", fmt.Sprintf("Failed to start ops ide devel: %s", err))
		return
	}

	develExited := make(chan error, 1)
	go func() {
		waitErr := develCmd.Wait()
		fmt.Fprintf(develLog, "[trustable] watcher exited at %s: %v\n", time.Now().Format(time.RFC3339), waitErr)
		develLog.Close()
		develExited <- waitErr
	}()

	select {
	case err := <-develExited:
		errMsg := "ops ide devel exited unexpectedly"
		if err != nil {
			errMsg = fmt.Sprintf("ops ide devel exited with error: %s", err)
		}
		send("error", errMsg)
		return
	case <-time.After(500 * time.Millisecond):
	}

	// Step 6: Wait for dev server to respond
	send("status", "Waiting for dev server to be ready...")
	log.Printf("Redeploy: waiting for HTTP response on port %d...", opsdevelPort)
	if err := waitForHTTP(opsdevelPort, 30*time.Second); err != nil {
		send("error", fmt.Sprintf("Dev server not responding: %s", err))
		return
	}
	log.Printf("Redeploy: dev server on port %d is responding", opsdevelPort)

	// Done - send action list as the data
	send("done", actionList)
	log.Printf("Redeploy: completed successfully for %s", req.Name)
}

// runOpsIdeInWorkbench runs a single `ops ide <subcommand>` in the workbench
// checkout of app and writes the JSON result. The workbench must already exist —
// both Undeploy and Clean operate on a launched app and never provision one.
func runOpsIdeInWorkbench(w http.ResponseWriter, r *http.Request, subcommand, doneMessage string) {
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

	workbenchPath, err := canonicalWorkbenchPath(req.Name)
	if err != nil {
		writeGitJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	info, statErr := os.Stat(workbenchPath)
	if os.IsNotExist(statErr) || (statErr == nil && !info.IsDir()) {
		writeGitJSON(w, http.StatusBadRequest, map[string]string{
			"error": "workbench not found - launch the app first",
		})
		return
	}
	if statErr != nil {
		writeGitJSON(w, http.StatusInternalServerError, map[string]string{"error": statErr.Error()})
		return
	}

	unlock := lockRuntimeLifecycle(subcommand + " " + req.Name)
	defer unlock()

	log.Printf("Running ops ide %s for %s...", subcommand, req.Name)
	cmd := exec.Command("ops", "ide", subcommand)
	cmd.Dir = workbenchPath
	out, runErr := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if runErr != nil {
		log.Printf("ops ide %s for %s failed: %s, output: %s", subcommand, req.Name, runErr, output)
		writeGitJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  fmt.Sprintf("ops ide %s failed: %s", subcommand, runErr),
			"output": output,
		})
		return
	}
	log.Printf("ops ide %s for %s completed successfully", subcommand, req.Name)
	writeGitJSON(w, http.StatusOK, map[string]string{
		"message": doneMessage,
		"output":  output,
	})
}

// handleUndeploy handles POST /api/undeploy - removes the app's deployed actions
// and packages from OpenServerless via `ops ide undeploy`.
func handleUndeploy(w http.ResponseWriter, r *http.Request) {
	runOpsIdeInWorkbench(w, r, "undeploy", "undeploy completed")
}

// handleClean handles POST /api/clean - removes local build artifacts from the
// workbench via `ops ide clean`. It does not redeploy; the preview stays down
// until the user runs Utils > Redeploy.
func handleClean(w http.ResponseWriter, r *http.Request) {
	runOpsIdeInWorkbench(w, r, "clean", "clean completed")
}

// handleLaunch routes launch API requests
func handleLaunch(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	// Extract app name from URL path /api/launch/<app>
	app := strings.TrimPrefix(r.URL.Path, "/api/launch/")
	app = strings.TrimPrefix(app, "/api/launch")

	switch r.Method {
	case http.MethodGet:
		if app == "" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"error": "App name is required"})
			return
		}
		unlock := lockRuntimeLifecycle("launch " + app)
		defer unlock()
		handleLaunchGet(w, r, app)
	case http.MethodDelete:
		unlock := lockRuntimeLifecycle("stop")
		defer unlock()
		handleLaunchDelete(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
