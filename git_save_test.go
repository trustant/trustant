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
	"strings"
	"testing"
)

func TestGitSaveGeneratedPathspecsExcludeRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Trustant Test"},
		{"config", "user.email", "trustant@example.test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %s", strings.Join(args, " "), err, output)
		}
	}
	// The managed block is what keeps generated files out of a save; the legacy
	// opencode.* names stay ignored for repos that predate it.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("opencode.json\nopencode.md\n"), 0644); err != nil {
		t.Fatalf("write .gitignore: %s", err)
	}
	if _, err := ensureManagedGitignore(dir); err != nil {
		t.Fatalf("ensureManagedGitignore: %s", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("initial\n"), 0644); err != nil {
		t.Fatalf("write README: %s", err)
	}
	for _, args := range [][]string{{"add", ".gitignore", "README.md"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %s", strings.Join(args, " "), err, output)
		}
	}

	files := map[string]string{
		"src.ts":                      "export const ready = true\n",
		"AGENTS.md":                   managedAppAgentsContent(),
		".mcp.json":                   `{"mcpServers":{"redis":{"env":{"REDIS_PWD":"secret"}}}}`,
		".openserverless-contract.md": "generated contract\n",
		"opencode.md":                 "generated guidance\n",
		"opencode.json":               `{"provider":{"options":{"apiKey":"secret"}}}`,
		"tsconfig.app.tsbuildinfo":    "compiler state\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %s", name, err)
		}
	}

	// Reproduce the index left behind by the old failing Commit implementation:
	// one ignored generated file and a compiler artifact were already staged.
	prestage := exec.Command("git", "add", "--force", "opencode.json", "tsconfig.app.tsbuildinfo")
	prestage.Dir = dir
	if output, err := prestage.CombinedOutput(); err != nil {
		t.Fatalf("prestage generated files: %s: %s", err, output)
	}

	dryRun := exec.Command("git", gitSaveDryRunAddArgs(dir)...)
	dryRun.Dir = dir
	if output, err := dryRun.CombinedOutput(); err != nil {
		t.Fatalf("git add dry-run: %s: %s", err, output)
	}

	add := exec.Command("git", gitSaveAddArgs(dir)...)
	add.Dir = dir
	if output, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %s", err, output)
	}
	resetGenerated := exec.Command("git", gitSaveResetGeneratedArgs(dir)...)
	resetGenerated.Dir = dir
	if output, err := resetGenerated.CombinedOutput(); err != nil {
		t.Fatalf("git reset generated files: %s: %s", err, output)
	}

	status := exec.Command("git", "diff", "--cached", "--name-only")
	status.Dir = dir
	output, err := status.Output()
	if err != nil {
		t.Fatalf("git diff --cached: %s", err)
	}
	if got := strings.TrimSpace(string(output)); got != "src.ts" {
		t.Fatalf("unexpected staged files: %q", got)
	}
	for _, name := range []string{".mcp.json", ".openserverless-contract.md", "opencode.md", "opencode.json", "AGENTS.md", "tsconfig.app.tsbuildinfo"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("generated working file %s should remain: %s", name, err)
		}
	}
}
