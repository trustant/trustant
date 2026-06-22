package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"
)

const (
	opencodePort = 4096
	opsdevelPort = 5173
)

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
				"redis-mcp-server",
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
	case "s3", "postgres", "redis", "milvus", "openserverless":
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
	} {
		if s.present {
			log.Printf("  - service %q configured for %s; MCP server will be generated", s.name, app)
		} else {
			log.Printf("  - service %q absent from ~/.ops/config.json for %s; MCP server will be skipped", s.name, app)
		}
	}
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

// setupServiceTooling writes the CLI wrapper scripts into ~/.local/bin that
// accompany the MCP servers (see spec/4-launch.md): `rclone` (s3), `psql`
// (postgres), `redis-cli` (redis), and `milvus_cli` (milvus). Each is gated on
// the same config block that gates its MCP server and is best-effort — failures
// are logged, never fatal to a launch.
//
// Every wrapper sets PATH to localBinPrefix() and then invokes the real binary
// by bare name (rather than an absolute path) so it reaches the system binary
// without re-entering the ~/.local/bin wrapper itself (spec/4-launch.md line 4).
func setupServiceTooling() {
	cfg, err := loadOpsConfig()
	if err != nil {
		log.Printf("Warning: failed to load ~/.ops/config.json for service tooling: %s", err)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Warning: failed to locate home for service tooling: %s", err)
		return
	}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		log.Printf("Warning: failed to create ~/.local/bin: %s", err)
		return
	}
	prefix := localBinPrefix()

	if cfg.S3.Host != "" {
		writeServiceWrapper(binDir, "rclone", fmt.Sprintf(
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
			cfg.S3.Bucket.Static, cfg.S3.Bucket.Data))
	}
	if cfg.Postgres.Database != "" {
		writeServiceWrapper(binDir, "psql", fmt.Sprintf(
			"#!/bin/bash\nexport PATH=%s\nexec psql \"%s\" \"$@\"\n",
			prefix, cfg.Postgres.URL))
	}
	if cfg.Redis.URL != "" || cfg.Redis.Port != 0 {
		user := redisUsername(cfg)
		writeServiceWrapper(binDir, "redis-cli", fmt.Sprintf(
			"#!/bin/bash\n"+
				"export PATH=%s\n"+
				"export REDISCLI_AUTH='%s'\n"+
				"exec redis-cli -h '%s' --user '%s' -p '%d' \"$@\"\n",
			prefix, cfg.Redis.Password, cfg.Redis.Service, user, cfg.Redis.Port))
	}
	if cfg.Milvus.Host != "" {
		// The milvus_cli wrapper is a self-contained Python script (rendered from
		// the embedded milvus_cli.tmpl) that auto-connects to the configured
		// host/db before dropping into the milvus-cli REPL, reusing the installed
		// milvus-cli venv. Its shebang is the venv python taken from the first line
		// of the installed `milvus_client` binary (see spec/4-launch.md).
		if body, err := renderMilvusCliWrapper(prefix, cfg); err != nil {
			log.Printf("Warning: failed to render milvus_cli wrapper: %s", err)
		} else {
			writeServiceWrapper(binDir, "milvus_cli", body)
		}
	}
}

// renderMilvusCliWrapper renders the embedded milvus_cli.tmpl with the milvus
// config and the python venv resolved from the installed `milvus_client`
// binary. <local.prefix> (prefix) is a PATH-style list of bin dirs; the first
// one containing `milvus_client` supplies the venv shebang (its first line).
func renderMilvusCliWrapper(prefix string, cfg *opsConfig) (string, error) {
	pythonVenv, err := milvusPythonVenv(prefix)
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

// milvusPythonVenv returns the venv python interpreter for the milvus_cli
// wrapper shebang: the first line of the installed `milvus_client` binary, found
// by scanning the colon-separated <local.prefix> bin dirs.
func milvusPythonVenv(prefix string) (string, error) {
	for _, dir := range strings.Split(prefix, ":") {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "milvus_client")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		firstLine := string(data)
		if idx := strings.IndexByte(firstLine, '\n'); idx != -1 {
			firstLine = firstLine[:idx]
		}
		firstLine = strings.TrimSpace(strings.TrimPrefix(firstLine, "#!"))
		if firstLine != "" {
			return firstLine, nil
		}
	}
	return "", fmt.Errorf("milvus_client not found in any of: %s", prefix)
}

// writeServiceWrapper writes a single executable wrapper script into binDir.
func writeServiceWrapper(binDir, name, body string) {
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		log.Printf("Warning: failed to write %s wrapper: %s", name, err)
		return
	}
	log.Printf("Configured %s wrapper at %s", name, path)
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
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 500*time.Millisecond)
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
//  2. No pgid file, but an orphaned opencode/devel still holding 4096/5173 (the
//     pgid file was removed while the process kept running) — reclaim the ports
//     directly so the upcoming port-free check doesn't wedge the launch.
// A `current` file lingering without a pgid file is itself stale, so it is
// cleared too.
func terminateLeftoverProcesses() {
	pgid, err := readPgid()
	if err != nil {
		// No pgid file: nothing to kill by group, but a prior process may still
		// be holding the ports. Reclaim them and clear the stale current marker.
		if isPortListening(opencodePort) || isPortListening(opsdevelPort) {
			log.Printf("No pgid file but ports busy; reclaiming orphaned listeners")
			reclaimPort(opencodePort)
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

func normalizeOpenCodeAgentColor(color string) string {
	normalized := strings.TrimSpace(strings.ToLower(color))
	switch normalized {
	case "primary", "secondary", "accent", "success", "warning", "error", "info":
		return normalized
	case "blue", "indigo":
		return "primary"
	case "purple", "violet", "gray", "grey":
		return "secondary"
	case "cyan", "sky", "teal":
		return "info"
	case "green", "emerald", "lime":
		return "success"
	case "yellow", "amber", "orange":
		return "warning"
	case "red", "rose", "pink":
		return "error"
	default:
		if len(color) == 7 && strings.HasPrefix(color, "#") {
			valid := true
			for _, ch := range color[1:] {
				if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
					valid = false
					break
				}
			}
			if valid {
				return color
			}
		}
		return "primary"
	}
}

func sanitizeOpenCodeAgentMetadata(workbenchPath string) {
	agentDir := filepath.Join(workbenchPath, ".opencode", "agent")
	if _, err := os.Stat(agentDir); err != nil {
		return
	}
	if err := filepath.WalkDir(agentDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			log.Printf("Warning: failed to read OpenCode agent metadata %s: %s", path, readErr)
			return nil
		}
		lines := strings.Split(string(data), "\n")
		changed := false
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "color:") {
				continue
			}
			prefix := line[:strings.Index(line, "color:")]
			rawColor := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "color:")), `"'`)
			normalized := normalizeOpenCodeAgentColor(rawColor)
			if normalized != rawColor {
				lines[i] = prefix + "color: " + normalized
				changed = true
			}
			break
		}
		if changed {
			if writeErr := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); writeErr != nil {
				log.Printf("Warning: failed to update OpenCode agent metadata %s: %s", path, writeErr)
				return nil
			}
			log.Printf("Normalized OpenCode agent metadata in %s", path)
		}
		return nil
	}); err != nil {
		log.Printf("Warning: failed to scan OpenCode agent metadata: %s", err)
	}
}

func openCodeProjectID(app string) string {
	sum := sha1.Sum([]byte("trustable:" + app))
	return hex.EncodeToString(sum[:])
}

func ensureOpenCodeProjectID(workbenchPath, app string) {
	gitDirOutput, err := exec.Command("git", "-C", workbenchPath, "rev-parse", "--git-dir").Output()
	gitDir := ""
	if err == nil {
		gitDir = strings.TrimSpace(string(gitDirOutput))
		if gitDir != "" && !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(workbenchPath, gitDir)
		}
	}
	if gitDir == "" {
		gitDir = filepath.Join(workbenchPath, ".git")
	}
	if info, statErr := os.Stat(gitDir); statErr != nil || !info.IsDir() {
		log.Printf("Warning: failed to locate git dir for OpenCode project id: %s", gitDir)
		return
	}
	projectIDPath := filepath.Join(gitDir, "opencode")
	projectID := openCodeProjectID(app)
	if err := os.WriteFile(projectIDPath, []byte(projectID), 0644); err != nil {
		log.Printf("Warning: failed to write OpenCode project id %s: %s", projectIDPath, err)
		return
	}
	log.Printf("OpenCode project id for %s set to %s", app, projectID)
}

func cleanupOpenCodeProjectDirectoryLinks(workbenchPath, app string) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Warning: failed to locate home for OpenCode DB cleanup: %s", err)
		return
	}
	dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if _, err := os.Stat(dbPath); err != nil {
		return
	}

	projectID := openCodeProjectID(app)
	script := `
import json
import sqlite3
import sys

db_path, directory, project_id = sys.argv[1:4]
con = sqlite3.connect(db_path)
cur = con.cursor()
changed = 0

cur.execute(
    "delete from project_directory where directory = ? and project_id != ?",
    (directory, project_id),
)
changed += cur.rowcount

for stale_id, raw_sandboxes in cur.execute(
    "select id, sandboxes from project where id != ?",
    (project_id,),
).fetchall():
    try:
        sandboxes = json.loads(raw_sandboxes or "[]")
    except Exception:
        sandboxes = []
    updated = [entry for entry in sandboxes if entry != directory]
    if updated != sandboxes:
        cur.execute(
            "update project set sandboxes = ? where id = ?",
            (json.dumps(updated), stale_id),
        )
        changed += cur.rowcount

con.commit()
print(changed)
`
	cmd := exec.Command("python3", "-c", script, dbPath, workbenchPath, projectID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Warning: failed to cleanup OpenCode project directory links: %s, output: %s", err, strings.TrimSpace(string(output)))
		return
	}
	changed := strings.TrimSpace(string(output))
	if changed != "" && changed != "0" {
		log.Printf("Cleaned %s stale OpenCode project directory link(s) for %s", changed, workbenchPath)
	}
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

// createOpencodeSession POSTs to opencode's /session/ endpoint with the
// workbench directory header so the running opencode server scopes its session
// to the launched app. The POST targets <domain>:<port> (not localhost) because
// in production opencode is reached through an ingress, not the loopback (see
// spec/4-launch.md). Failures are logged but non-fatal.
func createOpencodeSession(domain string, port int, directory string) string {
	url := fmt.Sprintf("http://%s:%d/session/", domain, port)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		log.Printf("opencode session POST: failed to build request: %s", err)
		return ""
	}
	req.Header.Set("X-Opencode-Directory", directory)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("opencode session POST %s failed: %s", url, err)
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	trimmedBody := strings.TrimSpace(string(body))
	log.Printf("opencode session POST %s -> %d: %s", url, resp.StatusCode, trimmedBody)
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		log.Printf("Warning: failed to parse OpenCode session response: %s", err)
		return ""
	}
	return session.ID
}

// handleLaunchGet handles GET /api/launch/<app>
func handleLaunchGet(w http.ResponseWriter, r *http.Request, app string) {
	w.Header().Set("Content-Type", "application/json")

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

	// Terminate leftover processes
	terminateLeftoverProcesses()

	// Clone to workbench if not already present
	// Use absolute path so child processes (Vite) resolve watches correctly
	workbenchPath, _ := filepath.Abs(filepath.Join(WorkbenchDir, app))
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

		// Generate .env and .env.production from config
		if err := generateAppEnvFiles(app); err != nil {
			log.Printf("Warning: failed to generate workbench .env: %s", err)
		}

		// Run npm install if package.json exists
		if _, err := os.Stat(filepath.Join(workbenchPath, "package.json")); err == nil {
			log.Printf("Running npm install in workbench/%s...", app)
			npmCmd := exec.Command("npm", "install")
			npmCmd.Dir = workbenchPath
			if output, err := npmCmd.CombinedOutput(); err != nil {
				log.Printf("Warning: npm install failed: %s, output: %s", err, string(output))
			}
		}

		log.Printf("Workbench for %s set up successfully", app)
	} else {
		log.Printf("Workbench for %s already exists, reusing", app)
		// On the reuse path, regenerate the .env files to keep them in sync with
		// the current config (spec/4-launch.md). npm install runs only on the
		// initial clone, not on reuse.
		if err := generateAppEnvFiles(app); err != nil {
			log.Printf("Warning: failed to regenerate workbench .env: %s", err)
		}
	}
	ensureOpenCodeProjectID(workbenchPath, app)
	cleanupOpenCodeProjectDirectoryLinks(workbenchPath, app)

	// Set up skills if not already present
	skillsAdded := ensureSkills(app)

	// Ensure the OpenWhisk user exists and password is in sync
	log.Printf("Checking OpenWhisk user for %s...", app)
	cfg, err := loadTrustableConfig()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to load config: %s", err)})
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

	// Run ops ide login (always, even when reusing workbench)
	log.Printf("Running ops ide login for %s...", app)
	loginCmd := exec.Command("ops", "ide", "login")
	loginCmd.Dir = workbenchPath
	if output, err := loginCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide login for %s failed: %s, output: %s", app, err, string(output))
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide login failed: %s", string(output))})
		return
	}
	log.Printf("ops ide login for %s completed successfully", app)

	// ops ide login (re)writes ~/.ops/config.json with the app user's service
	// bindings. The opencode.json/MCP generation below reads that file, so log
	// which service blocks landed — a missing block here is exactly why an MCP
	// server would be skipped or misconfigured (spec/4-launch.md).
	logOpsServiceBlocks(app)

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

	// Run ops ide deploy
	log.Printf("Running ops ide deploy for %s...", app)
	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workbenchPath
	if output, err := deployCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide deploy for %s failed: %s, output: %s", app, err, string(output))
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide deploy failed: %s", string(output))})
		return
	}
	log.Printf("ops ide deploy for %s completed successfully", app)

	// Detect ports
	leftPort := opencodePort
	rightPort := opsdevelPort

	// Check if ports are free. If a port is held — typically by an orphaned
	// opencode/devel from a prior launch whose pgid file is gone, so
	// terminateLeftoverProcesses couldn't clean it up — reclaim it by killing
	// the listener directly before giving up.
	if !isPortFree(leftPort) && !reclaimPort(leftPort) {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Port %d (opencode) is not available", leftPort)})
		return
	}
	if !isPortFree(rightPort) && !reclaimPort(rightPort) {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Port %d (opsdevel) is not available", rightPort)})
		return
	}

	// Generate the complete, self-contained OpenCode config directly in the
	// workbench project folder (<workbench>/<app>/opencode.json): provider,
	// model defaults, lsp, and the mcp servers from ~/.ops/config.json. There is
	// no global ~/.config/opencode/opencode.json (see spec/4-launch.md).
	if cfg, err := loadTrustableConfig(); err != nil {
		log.Printf("Warning: failed to load trustable config for opencode generation: %s", err)
	} else if err := generateOpencodeConfigForApp(cfg, app); err != nil {
		log.Printf("Warning: failed to generate opencode.json: %s", err)
	}
	// Configure the CLI tooling (rclone, psql, redis-cli) that accompanies the
	// MCP servers generated above from ~/.ops/config.json.
	setupServiceTooling()
	sanitizeOpenCodeAgentMetadata(workbenchPath)

	// Start opencode
	log.Printf("Starting opencode for %s on port %d...", app, leftPort)
	opencodeCmd := exec.Command("opencode", "serve", "--port", strconv.Itoa(leftPort), "--hostname", "0.0.0.0", "--log-level", "DEBUG", "--print-logs")
	opencodeCmd.Dir = workbenchPath
	opencodeCmd.Stdout = os.Stdout
	opencodeCmd.Stderr = os.Stderr
	opencodeCmd.Env = os.Environ()
	appEnv := parseEnvFile(filepath.Join(workbenchPath, ".env"))
	for key, value := range appEnv {
		opencodeCmd.Env = append(opencodeCmd.Env, key+"="+value)
	}
	// Set process group so we can kill all child processes
	opencodeCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Log exactly what we are about to run so failures are visible in the air console.
	if bin, lookErr := exec.LookPath("opencode"); lookErr != nil {
		log.Printf("opencode: WARNING `opencode` not found in PATH: %s", lookErr)
	} else {
		log.Printf("opencode: binary=%s", bin)
	}
	log.Printf("opencode: cmd=`%s`", strings.Join(opencodeCmd.Args, " "))
	log.Printf("opencode: dir=%s config=%s appEnvCount=%d", workbenchPath, filepath.Join(workbenchPath, "opencode.json"), len(appEnv))

	if err := opencodeCmd.Start(); err != nil {
		log.Printf("opencode: FAILED to start: %s", err)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to start opencode: %s", err)})
		return
	}

	log.Printf("opencode: started pid=%d in directory: %s", opencodeCmd.Process.Pid, workbenchPath)

	// Get the process group ID
	pgid, err := syscall.Getpgid(opencodeCmd.Process.Pid)
	if err != nil {
		opencodeCmd.Process.Kill()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to get process group: %s", err)})
		return
	}

	// Check opencode doesn't terminate within 0.5 seconds
	opencodeExited := make(chan error, 1)
	go func() {
		opencodeExited <- opencodeCmd.Wait()
	}()

	select {
	case err := <-opencodeExited:
		// Process exited within 0.5 seconds - this is an error
		errMsg := "opencode exited unexpectedly"
		if err != nil {
			errMsg = fmt.Sprintf("opencode exited with error: %s", err)
		}
		log.Printf("opencode: %s (see opencode stdout/stderr above for the cause)", errMsg)
		json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	case <-time.After(500 * time.Millisecond):
		// Process is still running - continue
		log.Printf("opencode: pid=%d still alive after 500ms, serving on port %d", opencodeCmd.Process.Pid, leftPort)
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
	develCmd := exec.Command("sh", "-c", fmt.Sprintf("cd %q && ops ide devel", workbenchPath))
	develCmd.Stdout = os.Stdout
	develCmd.Stderr = os.Stderr
	// Join the same process group as opencode
	develCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}

	if err := develCmd.Start(); err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to start ops ide devel: %s", err)})
		return
	}

	// Check ops ide devel doesn't terminate within 0.5 seconds
	develExited := make(chan error, 1)
	go func() {
		develExited <- develCmd.Wait()
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

	// Wait for both ports to be listening
	log.Printf("Waiting for ports %d and %d to be listening...", leftPort, rightPort)
	if err := waitForPort(leftPort, 30*time.Second); err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("opencode failed to start listening: %s", err)})
		return
	}
	if err := waitForPort(rightPort, 30*time.Second); err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide devel failed to start listening: %s", err)})
		return
	}

	// Calculate URL-encoded absolute path of the app folder
	absPath, err := filepath.Abs(workbenchPath)
	if err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to get absolute path: %s", err)})
		return
	}
	b64Path := base64.RawURLEncoding.EncodeToString([]byte(absPath))

	// Strip any port from the request host to get the bare domain.
	domain := r.Host
	if colonIdx := strings.LastIndex(domain, ":"); colonIdx != -1 {
		if bracketIdx := strings.LastIndex(domain, "]"); bracketIdx == -1 || colonIdx > bracketIdx {
			domain = domain[:colonIdx]
		}
	}

	// Notify opencode of the workbench directory so it scopes the session
	// correctly. The request arrives on the trustable.<domain> host, but opencode
	// is served on opencode.<domain> (the ingress routes by hostname prefix — see
	// middleware.go), so swap the prefix before POSTing.
	opencodeHost := strings.Replace(domain, "trustable.", "opencode.", 1)
	sessionID := createOpencodeSession(opencodeHost, leftPort, absPath)

	log.Printf("Services for %s started - opencode on port %d, opsdevel on port %d", app, leftPort, rightPort)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"left":         leftPort,
		"right":        rightPort,
		"b64dir":       b64Path,
		"encdir":       absPath,
		"session_id":   sessionID,
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
	url := fmt.Sprintf("http://localhost:%d/", port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Head(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("http://localhost:%d not responding after %v", port, timeout)
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

	req := struct{ Name string }{Name: name}

	workbenchPath, _ := filepath.Abs(filepath.Join(WorkbenchDir, req.Name))
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
	develCmd := exec.Command("sh", "-c", fmt.Sprintf("cd %q && ops ide devel --fast", workbenchPath))
	develCmd.Stdout = os.Stdout
	develCmd.Stderr = os.Stderr
	develCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}

	if err := develCmd.Start(); err != nil {
		send("error", fmt.Sprintf("Failed to start ops ide devel: %s", err))
		return
	}

	develExited := make(chan error, 1)
	go func() {
		develExited <- develCmd.Wait()
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
		handleLaunchGet(w, r, app)
	case http.MethodDelete:
		handleLaunchDelete(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
