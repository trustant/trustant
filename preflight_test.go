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
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCheckSSHKeyPersistsKeyInWorkspace(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}

	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	workspace := filepath.Join(tmp, "workspace")
	t.Setenv("HOME", home)
	WorkspaceDir = workspace
	sshKeyAvailable = false

	checkSSHKey()
	if !sshKeyAvailable {
		t.Fatalf("expected ssh key to be available after first check")
	}

	persistentKey := filepath.Join(workspace, ".trustable", "ssh", "id_ed25519")
	persistentPub := persistentKey + ".pub"
	homeKey := filepath.Join(home, ".ssh", "id_ed25519")
	homePub := homeKey + ".pub"
	for _, path := range []string{persistentKey, persistentPub, homeKey, homePub} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
	firstPub, err := os.ReadFile(homePub)
	if err != nil {
		t.Fatalf("failed to read public key: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(home, ".ssh")); err != nil {
		t.Fatalf("failed to remove ephemeral ssh dir: %v", err)
	}
	sshKeyAvailable = false
	checkSSHKey()
	if !sshKeyAvailable {
		t.Fatalf("expected ssh key to be available after simulated pod restart")
	}
	secondPub, err := os.ReadFile(homePub)
	if err != nil {
		t.Fatalf("failed to read public key after restart: %v", err)
	}
	if string(firstPub) != string(secondPub) {
		t.Fatalf("public key changed after simulated pod restart")
	}
}
