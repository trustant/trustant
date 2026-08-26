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

func gitignoreTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Trustable Test"},
		{"config", "user.email", "trustable@example.test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %s", strings.Join(args, " "), err, output)
		}
	}
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %s", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %s", path, err)
	}
}

func TestEnsureManagedGitignoreCreatesBlock(t *testing.T) {
	dir := t.TempDir()
	changed, err := ensureManagedGitignore(dir)
	if err != nil {
		t.Fatalf("ensureManagedGitignore: %s", err)
	}
	if !changed {
		t.Fatal("first call must report changed")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %s", err)
	}
	content := string(data)
	for _, want := range trustableGitignoreEntries {
		if !strings.Contains(content, want+"\n") {
			t.Fatalf("managed block missing %q: %s", want, content)
		}
	}
	// The negation must follow .env, otherwise Git keeps ignoring .env.dist.
	if strings.Index(content, "!.env.dist") < strings.Index(content, "\n.env\n") {
		t.Fatalf("!.env.dist must come after .env: %s", content)
	}

	changed, err = ensureManagedGitignore(dir)
	if err != nil {
		t.Fatalf("second ensureManagedGitignore: %s", err)
	}
	if changed {
		t.Fatal("second call must be a no-op so launch does not commit every time")
	}
	second, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(second) != content {
		t.Fatalf("second call rewrote the file: %s", second)
	}
}

func TestEnsureManagedGitignorePreservesUserLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	writeTestFile(t, path, "# my rules\ndist/\n*.log\n")

	if _, err := ensureManagedGitignore(dir); err != nil {
		t.Fatalf("ensureManagedGitignore: %s", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "# my rules\ndist/\n*.log\n") {
		t.Fatalf("user lines must be preserved verbatim: %s", data)
	}

	// User lines on both sides of the block survive a refresh, and a stale block
	// is replaced rather than duplicated.
	writeTestFile(t, path, "top/\n"+trustableGitignoreBegin+"\nstale-entry\n"+trustableGitignoreEnd+"\nbottom/\n")
	if _, err := ensureManagedGitignore(dir); err != nil {
		t.Fatalf("refresh ensureManagedGitignore: %s", err)
	}
	data, _ = os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "stale-entry") {
		t.Fatalf("stale managed entry survived: %s", content)
	}
	if !strings.HasPrefix(content, "top/\n") || !strings.HasSuffix(content, "bottom/\n") {
		t.Fatalf("user lines around the block must survive: %s", content)
	}
	if strings.Count(content, trustableGitignoreBegin) != 1 {
		t.Fatalf("managed block duplicated: %s", content)
	}
}

func TestUntrackManagedGeneratedFilesLeavesWorkingTree(t *testing.T) {
	dir := gitignoreTestRepo(t)
	for _, name := range []string{".env", ".mcp.json", "CLAUDE.md", ".openserverless-contract.md"} {
		writeTestFile(t, filepath.Join(dir, name), "generated\n")
	}
	writeTestFile(t, filepath.Join(dir, "README.md"), "app\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "initial")

	ensureWorkbenchGitignore(dir)

	for _, name := range []string{".env", ".mcp.json", "CLAUDE.md", ".openserverless-contract.md"} {
		if gitPathIsTracked(dir, name) {
			t.Fatalf("%s must be untracked after migration", name)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s must survive in the working tree: %s", name, err)
		}
	}
	if !gitPathIsTracked(dir, "README.md") {
		t.Fatal("application source must stay tracked")
	}
	if status := gitRun(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("workbench must be clean after launch, got:\n%s", status)
	}

	// Idempotent: nothing left to untrack, so no second commit.
	before := gitRun(t, dir, "rev-parse", "HEAD")
	ensureWorkbenchGitignore(dir)
	if after := gitRun(t, dir, "rev-parse", "HEAD"); after != before {
		t.Fatal("second run must not create another commit")
	}
}

func TestEnsureAgentConfigLinksMovesRealEntriesAside(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), "managed agents\n")
	writeTestFile(t, filepath.Join(dir, "CLAUDE.md"), "old claude notes\n")
	writeTestFile(t, filepath.Join(dir, ".claude", "settings.json"), "{}\n")
	writeTestFile(t, filepath.Join(dir, ".agents", "skills", "s", "SKILL.md"), "skill\n")

	ensureAgentConfigLinks(dir)

	target, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || target != "AGENTS.md" {
		t.Fatalf("CLAUDE.md must link to AGENTS.md, got %q err=%v", target, err)
	}
	target, err = os.Readlink(filepath.Join(dir, ".claude"))
	if err != nil || target != ".agents" {
		t.Fatalf(".claude must link to .agents, got %q err=%v", target, err)
	}

	// Displaced content is preserved, not deleted.
	backup, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md.removed"))
	if err != nil || string(backup) != "old claude notes\n" {
		t.Fatalf("previous CLAUDE.md must be kept as .removed, got %q err=%v", backup, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude.removed", "settings.json")); err != nil {
		t.Fatalf("previous .claude dir must be kept as .removed: %s", err)
	}

	// The link resolves to AGENTS.md, which now also carries the rescued notes
	// from the displaced CLAUDE.md.
	shared, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md through link: %s", err)
	}
	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %s", err)
	}
	if string(shared) != string(agents) {
		t.Fatalf("CLAUDE.md must read AGENTS.md content, got %q", shared)
	}
	if !strings.Contains(string(shared), "old claude notes") {
		t.Fatalf("displaced CLAUDE.md content must be rescued into AGENTS.md: %q", shared)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "s", "SKILL.md")); err != nil {
		t.Fatalf("skills must be reachable through .claude: %s", err)
	}
}

func TestEnsureAgentConfigLinksIdempotentAndOverwritesStaleBackup(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), "managed\n")
	writeTestFile(t, filepath.Join(dir, "CLAUDE.md"), "first\n")
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0755); err != nil {
		t.Fatalf("mkdir .agents: %s", err)
	}

	ensureAgentConfigLinks(dir)
	ensureAgentConfigLinks(dir)

	target, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || target != "AGENTS.md" {
		t.Fatalf("link must survive a second run, got %q err=%v", target, err)
	}
	// A no-op second run must not turn the link into its own backup.
	if backup, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md.removed")); err != nil || string(backup) != "first\n" {
		t.Fatalf("backup must not be overwritten by a no-op run, got %q err=%v", backup, err)
	}

	// A real file reappearing (e.g. restored by an older build) is moved aside
	// again, replacing the earlier backup instead of failing.
	if err := os.Remove(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatalf("remove link: %s", err)
	}
	writeTestFile(t, filepath.Join(dir, "CLAUDE.md"), "second\n")
	ensureAgentConfigLinks(dir)
	if backup, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md.removed")); err != nil || string(backup) != "second\n" {
		t.Fatalf("stale backup must be replaced, got %q err=%v", backup, err)
	}
}

// An existing CLAUDE.md went through the same managed-block merge as AGENTS.md,
// so it can hold notes that exist nowhere else. Renaming it aside would strand
// them in an ignored .removed file that is never committed.
func TestEnsureAgentConfigLinksRescuesClaudeAppLocalNotes(t *testing.T) {
	dir := t.TempDir()
	notes := "IMPORTANT: staging DB password rotates weekly, ask before deploy."
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), managedAppAgentsContent())
	writeTestFile(t, filepath.Join(dir, "CLAUDE.md"),
		managedAppAgentsContent()+"\n## App-local notes\n\n"+notes+"\n")

	ensureAgentConfigLinks(dir)

	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %s", err)
	}
	if !strings.Contains(string(agents), notes) {
		t.Fatalf("app-local notes from CLAUDE.md must survive in AGENTS.md: %s", agents)
	}
	if target, err := os.Readlink(filepath.Join(dir, "CLAUDE.md")); err != nil || target != "AGENTS.md" {
		t.Fatalf("CLAUDE.md must still become a link, got %q err=%v", target, err)
	}

	// Idempotent: a second launch must not append the notes again.
	ensureAgentConfigLinks(dir)
	again, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if strings.Count(string(again), notes) != 1 {
		t.Fatalf("notes duplicated on relaunch: %s", again)
	}
}

// A CLAUDE.md holding only the managed block is pure launch output: there is
// nothing to rescue and AGENTS.md must not gain an empty notes section.
func TestEnsureAgentConfigLinksIgnoresManagedOnlyClaude(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), managedAppAgentsContent())
	writeTestFile(t, filepath.Join(dir, "CLAUDE.md"), managedAppAgentsContent())

	ensureAgentConfigLinks(dir)

	agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if strings.Contains(string(agents), "## App-local notes") {
		t.Fatalf("managed-only CLAUDE.md must not add a notes section: %s", agents)
	}
}

// package-lock.json and AGENTS.md are content, not generated churn: launch must
// leave them committed so git is clean afterwards.
func TestCommitLaunchProjectFilesLeavesWorkbenchClean(t *testing.T) {
	dir := gitignoreTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "package.json"), `{"name":"app"}`)
	writeTestFile(t, filepath.Join(dir, "app.js"), "src\n")
	ensureWorkbenchGitignore(dir)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "initial")

	// What launch produces: npm install's lock file plus the regenerated
	// instruction file.
	writeTestFile(t, filepath.Join(dir, "package-lock.json"), `{"lockfileVersion":3}`)
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), managedAppAgentsContent())

	commitLaunchProjectFiles(dir)

	if status := gitRun(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("workbench must be clean after launch, got:\n%s", status)
	}
	for _, name := range []string{"package-lock.json", "AGENTS.md"} {
		if !gitPathIsTracked(dir, name) {
			t.Fatalf("%s must be committed by launch", name)
		}
	}

	// A relaunch with nothing changed must not add an empty commit.
	before := gitRun(t, dir, "rev-parse", "HEAD")
	commitLaunchProjectFiles(dir)
	if after := gitRun(t, dir, "rev-parse", "HEAD"); after != before {
		t.Fatal("second run must not create another commit")
	}
}

// An app with no package.json has no lock file; the step must still commit
// AGENTS.md rather than failing on the missing path.
func TestCommitLaunchProjectFilesWithoutLockFile(t *testing.T) {
	dir := gitignoreTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "app.js"), "src\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "initial")
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), managedAppAgentsContent())

	commitLaunchProjectFiles(dir)

	if !gitPathIsTracked(dir, "AGENTS.md") {
		t.Fatal("AGENTS.md must be committed even with no lock file")
	}
	if status := gitRun(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("workbench must be clean, got:\n%s", status)
	}
}

// The launch commit is scoped: work the user staged by hand is theirs to save.
func TestCommitLaunchProjectFilesLeavesUserStagedWorkAlone(t *testing.T) {
	dir := gitignoreTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "app.js"), "src\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "initial")

	writeTestFile(t, filepath.Join(dir, "app.js"), "user edit\n")
	gitRun(t, dir, "add", "app.js")
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), managedAppAgentsContent())

	commitLaunchProjectFiles(dir)

	if !gitPathIsTracked(dir, "AGENTS.md") {
		t.Fatal("AGENTS.md must still be committed")
	}
	staged := gitRun(t, dir, "diff", "--cached", "--name-only")
	if !strings.Contains(staged, "app.js") {
		t.Fatalf("user's staged edit must remain staged, not committed: %q", staged)
	}
}

// AGENTS.md must be in HEAD, or `git checkout .` on revert deletes it.
func TestRevertRestoresAgentsAfterLaunchCommit(t *testing.T) {
	dir := gitignoreTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "app.js"), "src\n")
	ensureWorkbenchGitignore(dir)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "initial")
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), managedAppAgentsContent())
	commitLaunchProjectFiles(dir)

	// Simulate an agent mangling the instruction file, then a revert.
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), "clobbered\n")
	gitRun(t, dir, "reset", "HEAD")
	gitRun(t, dir, "checkout", ".")
	gitRun(t, dir, "clean", "-fd")

	restored, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md must be restored by revert, not deleted: %s", err)
	}
	if string(restored) != managedAppAgentsContent() {
		t.Fatalf("revert must restore the managed block, got: %s", restored)
	}
}

// The whole point of the managed block: revert must not destroy runtime state.
func TestRevertPreservesIgnoredRuntimeState(t *testing.T) {
	dir := gitignoreTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), "managed agents\n")
	writeTestFile(t, filepath.Join(dir, ".agents", "skills", "s", "SKILL.md"), "skill\n")
	writeTestFile(t, filepath.Join(dir, "README.md"), "app\n")
	ensureWorkbenchGitignore(dir)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "app content")

	// Launch-generated state, none of it committed.
	writeTestFile(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers":{}}`)
	writeTestFile(t, filepath.Join(dir, ".acp-data", "session.json"), `{"id":"s1"}`)
	writeTestFile(t, filepath.Join(dir, ".env"), "OPS_USER=demo\n")
	ensureAgentConfigLinks(dir)

	// Exactly what handleGit runs for cmd=checkout .
	gitRun(t, dir, "reset", "HEAD")
	gitRun(t, dir, "checkout", ".")
	gitRun(t, dir, "clean", "-fd")

	for _, name := range []string{".mcp.json", ".acp-data/session.json", ".env", "CLAUDE.md", ".claude"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s must survive revert: %s", name, err)
		}
	}
	// Committed content is still restored by the same revert.
	for _, name := range []string{"AGENTS.md", ".agents/skills/s/SKILL.md", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s must be restored from HEAD: %s", name, err)
		}
	}
}
