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

// mcpproxy.go re-exposes the current app's own MCP servers on the Trustant
// MCP endpoint (spec/22-mcpserver.md). It reads the private, credential-
// bearing config launch writes (writeManagedMCPConfigs), connects to every
// server as an MCP client, and registers each remote tool as
// <server>__<tool>. Credentials stay on this host: the external agent only
// ever sees the tools.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpProxyConnectTimeout bounds one server's start + initialize + tools/list.
// The openserverless MCP runs through tsx and can take a while to boot.
var mcpProxyConnectTimeout = 90 * time.Second

type proxiedMCPServer struct {
	session *mcp.ClientSession
	cmd     *exec.Cmd
	tools   []string
}

type mcpProxyManager struct {
	mu       sync.Mutex
	app      string
	mtime    time.Time
	syncing  bool
	servers  map[string]*proxiedMCPServer
	status   map[string]string
	register func(*mcp.Tool, mcp.ToolHandler) // test seam; default mcpServer.AddTool
	remove   func(...string)
	cfgPath  func(projectDir string) (string, error)
}

var mcpProxies = &mcpProxyManager{}

// proxyState is what the proxies should currently reflect: the current app
// and the mtime of its private MCP config. Empty app means "no proxies".
func (m *mcpProxyManager) wantedState() (app string, mtime time.Time, cfg string) {
	app, err := readCurrentApp()
	if err != nil || app == "" {
		return "", time.Time{}, ""
	}
	pathFn := m.cfgPath
	if pathFn == nil {
		pathFn = managedMCPConfigPath
	}
	cfg, err = pathFn(filepath.Join(WorkbenchDir, app))
	if err != nil {
		return app, time.Time{}, ""
	}
	if info, err := os.Stat(cfg); err == nil {
		mtime = info.ModTime()
	}
	return app, mtime, cfg
}

// maybeSync restarts the proxies in the background when the current app or
// its MCP config changed. It is called on every /mcp request and after
// launch/stop, so nothing is spawned while no MCP client is connected. The
// SDK emits notifications/tools/list_changed when the tool set changes.
func (m *mcpProxyManager) maybeSync() {
	app, mtime, cfg := m.wantedState()
	m.mu.Lock()
	if m.syncing || (app == m.app && mtime.Equal(m.mtime)) {
		m.mu.Unlock()
		return
	}
	m.syncing = true
	m.app, m.mtime = app, mtime
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			m.syncing = false
			m.mu.Unlock()
		}()
		m.restart(app, cfg)
	}()
}

// syncNow is maybeSync without the goroutine (tests).
func (m *mcpProxyManager) syncNow() {
	app, mtime, cfg := m.wantedState()
	m.mu.Lock()
	m.app, m.mtime = app, mtime
	m.mu.Unlock()
	m.restart(app, cfg)
}

func (m *mcpProxyManager) statusSnapshot() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.status))
	for k, v := range m.status {
		out[k] = v
	}
	return out
}

func (m *mcpProxyManager) addTool(t *mcp.Tool, h mcp.ToolHandler) {
	if m.register != nil {
		m.register(t, h)
		return
	}
	s, _ := trustantMCPServer()
	s.AddTool(t, h)
}

func (m *mcpProxyManager) removeTools(names ...string) {
	if len(names) == 0 {
		return
	}
	if m.remove != nil {
		m.remove(names...)
		return
	}
	s, _ := trustantMCPServer()
	s.RemoveTools(names...)
}

// stopAll closes every proxied server and unregisters its tools.
func (m *mcpProxyManager) stopAll() {
	m.mu.Lock()
	servers := m.servers
	m.servers = nil
	m.status = nil
	m.mu.Unlock()
	for _, p := range servers {
		m.removeTools(p.tools...)
		p.close()
	}
}

func (p *proxiedMCPServer) close() {
	if p.session != nil {
		p.session.Close()
	}
	killProxyProcessGroup(p.cmd)
}

type mcpServerEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
}

func (m *mcpProxyManager) restart(app, cfg string) {
	m.stopAll()
	if app == "" || cfg == "" {
		return
	}
	data, err := os.ReadFile(cfg)
	if err != nil {
		return // not launched yet in this workbench: no config, no proxies
	}
	var config struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		m.setStatus("_config", "invalid "+cfg+": "+err.Error())
		return
	}
	projectDir := filepath.Join(WorkbenchDir, app)
	var wg sync.WaitGroup
	for _, name := range sortedKeys(config.MCPServers) {
		entry := config.MCPServers[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.startOne(app, projectDir, name, entry)
		}()
	}
	wg.Wait()
}

func (m *mcpProxyManager) setStatus(name, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status == nil {
		m.status = map[string]string{}
	}
	m.status[name] = value
}

var proxyToolNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// proxyToolName is <server>__<tool>, restricted to the characters and length
// MCP clients accept for tool names.
func proxyToolName(server, tool string) string {
	name := proxyToolNameUnsafe.ReplaceAllString(server, "_") + "__" + proxyToolNameUnsafe.ReplaceAllString(tool, "_")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func (m *mcpProxyManager) startOne(app, projectDir, name string, entry mcpServerEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpProxyConnectTimeout)
	defer cancel()

	var transport mcp.Transport
	var cmd *exec.Cmd
	switch entry.Type {
	case "http", "streamable-http", "remote":
		if entry.URL == "" {
			m.setStatus(name, "error: no url")
			return
		}
		transport = &mcp.StreamableClientTransport{Endpoint: entry.URL}
	default:
		if entry.Command == "" {
			m.setStatus(name, "error: no command")
			return
		}
		cmd = exec.Command(entry.Command, entry.Args...)
		cmd.Dir = projectDir
		cmd.Env = os.Environ()
		for _, k := range sortedKeys(entry.Env) {
			cmd.Env = append(cmd.Env, k+"="+entry.Env[k])
		}
		cmd.Stderr = &proxyStderr{server: name}
		prepareProxyCommand(cmd)
		transport = &mcp.CommandTransport{Command: cmd, TerminateDuration: 3 * time.Second}
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "trustant-proxy", Version: appVersion}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		killProxyProcessGroup(cmd)
		m.setStatus(name, "error: "+err.Error())
		return
	}
	p := &proxiedMCPServer{session: session, cmd: cmd}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			p.close()
			m.setStatus(name, "error listing tools: "+err.Error())
			return
		}
		p.tools = append(p.tools, m.registerProxyTool(name, session, tool))
	}

	m.mu.Lock()
	// The app may have changed while this server was booting.
	if m.app != app {
		m.mu.Unlock()
		m.removeTools(p.tools...)
		p.close()
		return
	}
	if m.servers == nil {
		m.servers = map[string]*proxiedMCPServer{}
	}
	m.servers[name] = p
	m.mu.Unlock()
	m.setStatus(name, fmt.Sprintf("ok (%d tools)", len(p.tools)))
}

func (m *mcpProxyManager) registerProxyTool(server string, session *mcp.ClientSession, tool *mcp.Tool) string {
	name := proxyToolName(server, tool.Name)
	schema := tool.InputSchema
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	desc := "[" + server + "] " + tool.Description
	original := tool.Name
	m.addTool(&mcp.Tool{Name: name, Title: tool.Title, Description: desc, InputSchema: schema, Annotations: tool.Annotations},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args any
			if req.Params != nil && len(req.Params.Arguments) > 0 {
				args = req.Params.Arguments
			}
			res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: original, Arguments: args})
			if err != nil {
				return mcpError("%s: %s", name, err), nil
			}
			return res, nil
		})
	return name
}

// proxyStderr forwards a proxied server's stderr to the Trustant log, line
// prefixed, so a crashing server is diagnosable.
type proxyStderr struct {
	server string
	buf    []byte
}

func (p *proxyStderr) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := strings.IndexByte(string(p.buf), '\n')
		if i < 0 {
			break
		}
		log.Printf("[mcp proxy %s] %s", p.server, string(p.buf[:i]))
		p.buf = p.buf[i+1:]
	}
	return len(b), nil
}
