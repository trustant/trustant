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

//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// prepareProxyCommand puts a proxied stdio MCP server in its own process
// group, so stopping it also stops what it spawned (tsx -> node, npx -> ...).
func prepareProxyCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	setProxyParentDeathSignal(cmd.SysProcAttr)
}

func killProxyProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
