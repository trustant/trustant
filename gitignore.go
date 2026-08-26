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
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The managed .gitignore block uses its own markers rather than the AGENTS.md
// ones because '#' is the only comment form Git honours in an ignore file.
const (
	trustableGitignoreBegin = "# >>> trustable managed — do not edit <<<"
	trustableGitignoreEnd   = "# >>> end trustable managed <<<"
)

// trustableGitignoreEntries is everything launch regenerates or that is local
// runtime state. WHY these must be ignored rather than excluded per-callsite:
// `git clean -fd` on revert deletes untracked-but-not-ignored files, which used
// to destroy the app's MCP wiring and truacp's session store. Ignored paths
// survive it. AGENTS.md and .agents/ are deliberately absent: they are real
// committed content that revert should restore from HEAD.
var trustableGitignoreEntries = []string{
	".acp-data/",
	".env",
	".env.production",
	"!.env.dist",
	"node_modules/",
	".mcp.json",
	"CLAUDE.md",
	"CLAUDE.md.removed",
	".claude",
	".claude.removed",
	".openserverless-contract.md",
}

// trustableUntrackPaths are the managed entries that older workbenches committed
// before this block existed. Adding a path to .gitignore does nothing while Git
// still tracks it, so launch untracks them once. Negations and the .removed
// backups are excluded: the former is not a path, the latter never existed
// before this change.
var trustableUntrackPaths = []string{
	".acp-data",
	".env",
	".env.production",
	"node_modules",
	".mcp.json",
	"CLAUDE.md",
	".openserverless-contract.md",
}

func managedGitignoreBlock() string {
	return trustableGitignoreBegin + "\n" +
		strings.Join(trustableGitignoreEntries, "\n") + "\n" +
		trustableGitignoreEnd + "\n"
}

// mergeManagedGitignore replaces only the marked block so user-authored ignore
// rules above and below it survive byte-for-byte.
func mergeManagedGitignore(existing string) string {
	managed := managedGitignoreBlock()
	start := strings.Index(existing, trustableGitignoreBegin)
	end := strings.Index(existing, trustableGitignoreEnd)
	if start >= 0 && end >= start {
		end += len(trustableGitignoreEnd)
		head := existing[:start]
		tail := strings.TrimLeft(existing[end:], "\n")
		if tail != "" && !strings.HasSuffix(tail, "\n") {
			tail += "\n"
		}
		return head + managed + tail
	}

	if strings.TrimSpace(existing) == "" {
		return managed
	}
	if !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	return existing + "\n" + managed
}

// ensureManagedGitignore writes the managed block into the workbench .gitignore.
// It reports whether the file changed so the caller can skip a commit on the
// common no-op launch.
func ensureManagedGitignore(workbenchPath string) (bool, error) {
	path := filepath.Join(workbenchPath, ".gitignore")
	existingBytes, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to read %s: %w", path, err)
	}
	existing := string(existingBytes)
	updated := mergeManagedGitignore(existing)
	if updated == existing {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return false, fmt.Errorf("failed to write %s: %w", path, err)
	}
	return true, nil
}

func gitPathIsTracked(workbenchPath, path string) bool {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", "--", path)
	cmd.Dir = workbenchPath
	return cmd.Run() == nil
}

// untrackManagedGeneratedFiles drops the managed paths from the index while
// leaving the working tree alone, so the running app keeps its .env and
// .mcp.json. Idempotent: once untracked, later launches find nothing to do.
func untrackManagedGeneratedFiles(workbenchPath string) []string {
	var removed []string
	for _, path := range trustableUntrackPaths {
		if !gitPathIsTracked(workbenchPath, path) {
			continue
		}
		cmd := exec.Command("git", "rm", "--cached", "-r", "--quiet", "--", path)
		cmd.Dir = workbenchPath
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("untrackManagedGeneratedFiles: git rm --cached %s failed: %s", path, strings.TrimSpace(string(output)))
			continue
		}
		removed = append(removed, path)
	}
	return removed
}

// ensureWorkbenchGitignore writes the managed block, untracks anything an older
// version committed, and commits both together. Best-effort and non-fatal: a
// scaffolding failure must not abort a launch. It never pushes.
func ensureWorkbenchGitignore(workbenchPath string) {
	changed, err := ensureManagedGitignore(workbenchPath)
	if err != nil {
		log.Printf("ensureWorkbenchGitignore: %s", err)
		return
	}
	untracked := untrackManagedGeneratedFiles(workbenchPath)
	if !changed && len(untracked) == 0 {
		return
	}

	if err := ensureGitIdentity(workbenchPath); err != nil {
		log.Printf("ensureWorkbenchGitignore: %s", err)
		return
	}
	addCmd := exec.Command("git", "add", "--", ".gitignore")
	addCmd.Dir = workbenchPath
	if output, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("ensureWorkbenchGitignore: git add .gitignore failed: %s", strings.TrimSpace(string(output)))
		return
	}
	commitCmd := exec.Command("git", "commit", "-m", "trustable: manage generated files")
	commitCmd.Dir = workbenchPath
	if output, err := commitCmd.CombinedOutput(); err != nil {
		log.Printf("ensureWorkbenchGitignore: git commit failed: %s", strings.TrimSpace(string(output)))
		return
	}
	if len(untracked) > 0 {
		log.Printf("ensureWorkbenchGitignore: untracked generated files %v", untracked)
	}
	log.Printf("ensureWorkbenchGitignore: committed managed .gitignore")
}

// commitEnvDist stages and commits .env.dist right after the generator changed
// it. WHY the server commits instead of waiting for the user's next Save: the
// manifest is the contract a clone reads to discover which variables it must be
// given, so it has to track the app's variable set at all times rather than sit
// dirty until someone happens to save code. Same treatment, and same
// constraints, as the managed .gitignore above: best-effort, non-fatal, and it
// never pushes. Callers must invoke it only when the content actually changed,
// otherwise every launch would attempt an empty commit.
func commitEnvDist(workbenchPath string) {
	// A workbench scaffolded before its first clone has no repository to commit
	// into. Writing .env.dist there is still correct; committing is not.
	if _, err := os.Stat(filepath.Join(workbenchPath, ".git")); err != nil {
		return
	}
	if err := ensureGitIdentity(workbenchPath); err != nil {
		log.Printf("commitEnvDist: %s", err)
		return
	}

	// Scoped pathspec: whatever else the user has dirty in the workbench is none
	// of this commit's business.
	addCmd := exec.Command("git", "add", "--", ".env.dist")
	addCmd.Dir = workbenchPath
	if output, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("commitEnvDist: git add .env.dist failed: %s", strings.TrimSpace(string(output)))
		return
	}

	// The file can differ from disk yet match HEAD (a revert restored it), which
	// leaves nothing staged. Committing then fails; that is a no-op, not an error.
	diffCmd := exec.Command("git", "diff", "--cached", "--quiet", "--", ".env.dist")
	diffCmd.Dir = workbenchPath
	if diffCmd.Run() == nil {
		return
	}

	commitCmd := exec.Command("git", "commit", "-m", "trustable: update .env.dist", "--", ".env.dist")
	commitCmd.Dir = workbenchPath
	if output, err := commitCmd.CombinedOutput(); err != nil {
		log.Printf("commitEnvDist: git commit failed: %s", strings.TrimSpace(string(output)))
		return
	}
	log.Printf("commitEnvDist: committed .env.dist")
}

// rescueClaudeAppLocalNotes folds notes from a soon-to-be-displaced CLAUDE.md
// into AGENTS.md. WHY: CLAUDE.md went through the same managed-block merge as
// AGENTS.md, so an existing one can hold app-local notes that exist nowhere
// else. Renaming it aside would strand them in an ignored .removed file —
// never committed, and invisible to git status. AGENTS.md stays tracked, so
// merging there is what actually preserves them.
func rescueClaudeAppLocalNotes(workbenchPath string) {
	claudePath := filepath.Join(workbenchPath, "CLAUDE.md")
	if info, err := os.Lstat(claudePath); err != nil || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	if agentsHasOnlyTrustableManagedBlock(claudePath) {
		return
	}
	data, err := os.ReadFile(claudePath)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return
	}

	agentsPath := filepath.Join(workbenchPath, "AGENTS.md")
	agentsBytes, err := os.ReadFile(agentsPath)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("rescueClaudeAppLocalNotes: failed to read AGENTS.md: %s", err)
		return
	}
	notes := extractAppLocalNotes(string(data))
	if notes == "" {
		return
	}
	// Already carried over by an earlier launch, or authored identically in
	// AGENTS.md: re-appending would duplicate the section on every launch.
	if strings.Contains(string(agentsBytes), notes) {
		return
	}
	merged := mergeManagedAppAgents(string(agentsBytes))
	if !strings.HasSuffix(merged, "\n") {
		merged += "\n"
	}
	if !strings.Contains(merged, "## App-local notes") {
		merged += "\n## App-local notes\n"
	}
	merged += "\n" + notes + "\n"
	if err := os.WriteFile(agentsPath, []byte(merged), 0644); err != nil {
		log.Printf("rescueClaudeAppLocalNotes: failed to write AGENTS.md: %s", err)
		return
	}
	log.Printf("rescueClaudeAppLocalNotes: moved CLAUDE.md app-local notes into AGENTS.md")
}

// extractAppLocalNotes returns the user-authored part of a managed instruction
// file: whatever follows the managed block, or the whole file when it has no
// markers at all.
func extractAppLocalNotes(content string) string {
	if end := strings.Index(content, trustableAgentsEnd); end >= 0 {
		content = content[end+len(trustableAgentsEnd):]
	}
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "## App-local notes")
	return strings.TrimSpace(content)
}

// launchCommittedFiles is content launch produces that belongs in the repo,
// as opposed to the ignored generated set. package-lock.json pins the exact
// dependency tree npm install resolved; AGENTS.md must exist in HEAD so a
// revert restores a correct managed block instead of deleting the file.
var launchCommittedFiles = []string{
	"package-lock.json",
	"AGENTS.md",
}

// commitLaunchProjectFiles commits the files launch owns but that must live in
// git. Best-effort and non-fatal, like the other launch scaffolding steps, and
// it never pushes.
//
// Unlike gitSaveExcludedFiles, this commits AGENTS.md even when it holds only
// the managed block. WHY the two rules differ: a user-initiated Save skips a
// managed-block-only AGENTS.md as regenerated noise, but launch needs the file
// present in HEAD, or `git checkout .` on revert would delete it outright.
func commitLaunchProjectFiles(workbenchPath string) {
	var present []string
	for _, name := range launchCommittedFiles {
		if _, err := os.Stat(filepath.Join(workbenchPath, name)); err == nil {
			present = append(present, name)
		}
	}
	if len(present) == 0 {
		return
	}

	addArgs := append([]string{"add", "--"}, present...)
	addCmd := exec.Command("git", addArgs...)
	addCmd.Dir = workbenchPath
	if output, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("commitLaunchProjectFiles: git add failed: %s", strings.TrimSpace(string(output)))
		return
	}

	// Nothing staged for these paths means they already match HEAD; committing
	// anyway would add an empty commit on every launch. The pathspec keeps an
	// unrelated file the user staged by hand from triggering a commit here.
	diffArgs := append([]string{"diff", "--cached", "--quiet", "--"}, present...)
	diffCmd := exec.Command("git", diffArgs...)
	diffCmd.Dir = workbenchPath
	if diffCmd.Run() == nil {
		return
	}

	if err := ensureGitIdentity(workbenchPath); err != nil {
		log.Printf("commitLaunchProjectFiles: %s", err)
		return
	}
	// Scoped to these paths so a launch never sweeps in unrelated work the user
	// had staged; that belongs to their own Save.
	commitArgs := append([]string{"commit", "-m", "trustable: update project files", "--"}, present...)
	commitCmd := exec.Command("git", commitArgs...)
	commitCmd.Dir = workbenchPath
	if output, err := commitCmd.CombinedOutput(); err != nil {
		log.Printf("commitLaunchProjectFiles: git commit failed: %s", strings.TrimSpace(string(output)))
		return
	}
	log.Printf("commitLaunchProjectFiles: committed %v", present)
}

// ensureManagedSymlink points name at target, renaming any real entry to
// <name>.removed first. WHY rename instead of merge or delete: the rule stays
// the same for a file and a directory, it always converges, and the previous
// content stays readable. Both .removed names are in the managed ignore block,
// so the backup itself is not churn and revert will not delete it.
func ensureManagedSymlink(workbenchPath, name, target string) error {
	path := filepath.Join(workbenchPath, name)
	if current, err := os.Readlink(path); err == nil && current == target {
		return nil
	}

	if _, err := os.Lstat(path); err == nil {
		backup := path + ".removed"
		// A backup from an earlier launch would make the rename fail on a
		// directory, so it is replaced rather than kept.
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("failed to clear %s: %w", backup, err)
		}
		if err := os.Rename(path, backup); err != nil {
			return fmt.Errorf("failed to rename %s: %w", path, err)
		}
		log.Printf("ensureManagedSymlink: moved %s aside to %s.removed", name, name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}

	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("failed to link %s -> %s: %w", name, target, err)
	}
	log.Printf("ensureManagedSymlink: %s -> %s", name, target)
	return nil
}

// ensureAgentConfigLinks gives Pi, Codex, and Claude Code one shared
// configuration: CLAUDE.md and AGENTS.md are the same bytes, .claude and
// .agents the same directory, so instructions and skills cannot drift.
//
// Ordering: this must run after ensureSkills, which skips cloning when .agents
// already exists and is non-empty. Creating the .claude link first would create
// .agents and permanently suppress skills setup.
func ensureAgentConfigLinks(workbenchPath string) {
	// Must run before the rename: once CLAUDE.md is a .removed backup it is
	// ignored, and any app-local notes in it would never be committed again.
	rescueClaudeAppLocalNotes(workbenchPath)
	if err := ensureManagedSymlink(workbenchPath, "CLAUDE.md", "AGENTS.md"); err != nil {
		log.Printf("ensureAgentConfigLinks: %s", err)
	}
	if err := ensureManagedSymlink(workbenchPath, ".claude", ".agents"); err != nil {
		log.Printf("ensureAgentConfigLinks: %s", err)
	}
}
