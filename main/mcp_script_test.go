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
	"os"
	"strings"
	"testing"
)

// util/mcp.sh and util/mcp.ps1 configure local agents. The invariants that
// are easy to regress: run from the repo root, push the token to the server
// BEFORE writing any agent config (so client and server cannot drift), keep
// the token on the user's machine (never in the repo or .env), and replace an
// existing entry instead of duplicating it.
func TestMCPClientScripts(t *testing.T) {
	sh, err := os.ReadFile("../util/mcp.sh")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := os.ReadFile("../util/mcp.ps1")
	if err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{"mcp.sh": string(sh), "mcp.ps1": string(ps)} {
		for _, want := range []string{"/api/v1/token", "token.mcp", "mcp-remote", "--allow-http", "mcp_servers.", "--scope user", "--transport http"} {
			if !strings.Contains(script, want) {
				t.Errorf("%s must contain %q", name, want)
			}
		}
		if strings.Contains(script, "TRUSTANT_TOKEN=") || strings.Contains(script, "main/.env") {
			t.Errorf("%s must not read or write the token through .env", name)
		}
	}
	shs := string(sh)
	if !strings.Contains(shs, `cd "$(dirname "$0")/.."`) {
		t.Error("mcp.sh must cd to the repo root")
	}
	if !strings.Contains(string(ps), "Set-Location (Split-Path -Parent $PSScriptRoot)") {
		t.Error("mcp.ps1 must cd to the repo root")
	}
	if !strings.Contains(shs, "Library/Application Support/Trustant") {
		t.Error("mcp.sh must keep the macOS token next to current.ip")
	}
	// add: save + PUT come before the agents are configured.
	add := shs[strings.Index(shs, "\n  add)"):]
	if put, cfg := strings.Index(add, "put_token"), strings.Index(add, "configure_all"); put < 0 || cfg < 0 || put > cfg {
		t.Error("mcp.sh add must PUT the token before configuring agents")
	}
	// Idempotent: remove-then-add for Claude Code, strip-then-append for Codex.
	if !strings.Contains(shs, `run claude mcp remove --scope user "$NAME"`) || !strings.Contains(shs, "codex_strip\n") {
		t.Error("mcp.sh must replace existing entries, not duplicate them")
	}
	// macOS bash rejects {32,256} (repetition > 255).
	if strings.Contains(shs, "{32,256}") {
		t.Error("mcp.sh must not use a {32,256} bash regex: macOS caps repetition at 255")
	}
}
