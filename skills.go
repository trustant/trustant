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
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SkillInfo represents a single skill parsed from SKILL.md
type SkillInfo struct {
	Name       string            `json:"name"`
	FrontMatter map[string]string `json:"front_matter"`
	Body       string            `json:"body"`
}

// SkillsResponse is the response for GET /api/skills/<name>
type SkillsResponse struct {
	Skills []SkillInfo `json:"skills"`
	Source string      `json:"source"`
}

// getSkillsRepo returns the OPS_SKILLS value for an app.
// Uses .env.production if it has OPS_SKILLS, otherwise falls back to .env.
func getSkillsRepo(appName string) string {
	workbenchPath := filepath.Join(WorkbenchDir, appName)

	// Try .env.production first
	prodEnv := parseEnvFile(filepath.Join(workbenchPath, ".env.production"))
	if repo := prodEnv["OPS_SKILLS"]; repo != "" {
		return repo
	}

	// Fall back to .env
	devEnv := parseEnvFile(filepath.Join(workbenchPath, ".env"))
	if repo := devEnv["OPS_SKILLS"]; repo != "" {
		return repo
	}

	// Fall back to global default
	return OpsSkills
}

// setupSkills clones the skills repo into .agents/skills for an app.
// Returns an error if the clone fails.
func setupSkills(appName string) error {
	workbenchPath := filepath.Join(WorkbenchDir, appName)
	agentsDir := filepath.Join(workbenchPath, ".agents")
	skillsDir := filepath.Join(agentsDir, "skills")

	repo := getSkillsRepo(appName)

	// Create .agents if it doesn't exist
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return fmt.Errorf("failed to create .agents dir: %w", err)
	}

	// Clean up any leftover skills dir from a previous failed attempt
	os.RemoveAll(skillsDir)

	// Clone into .agents/skills by cd-ing into .agents and cloning as "skills"
	cloned := false
	homeDir, _ := os.UserHomeDir()
	sshKeyPath := filepath.Join(homeDir, ".ssh", "id_ed25519")
	if _, err := os.Stat(sshKeyPath); err == nil {
		repoURL := fmt.Sprintf("git@github.com:%s.git", repo)
		log.Printf("Cloning skills from %s (SSH)...", repoURL)
		sshCmd := fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=no", sshKeyPath)
		cloneCmd := exec.Command("git", "clone", repoURL, "skills")
		cloneCmd.Dir = agentsDir
		cloneCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
		if output, err := cloneCmd.CombinedOutput(); err != nil {
			log.Printf("SSH clone of skills failed, falling back to HTTPS: %s, output: %s", err, string(output))
			os.RemoveAll(skillsDir)
		} else {
			cloned = true
		}
	}
	if !cloned {
		repoURL := fmt.Sprintf("https://github.com/%s.git", repo)
		log.Printf("Cloning skills from %s (HTTPS)...", repoURL)
		cloneCmd := exec.Command("git", "clone", repoURL, "skills")
		cloneCmd.Dir = agentsDir
		if output, err := cloneCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone failed: %s", strings.TrimSpace(string(output)))
		}
	}

	// Remove skills/.git so skills become part of the app repo
	if err := os.RemoveAll(filepath.Join(skillsDir, ".git")); err != nil {
		return fmt.Errorf("failed to remove .git from skills: %w", err)
	}

	// Add, commit and push to git
	addCmd := exec.Command("git", "add", ".agents/skills")
	addCmd.Dir = workbenchPath
	if output, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("Warning: git add .agents/skills failed: %s", strings.TrimSpace(string(output)))
		return nil
	}

	commitCmd := exec.Command("git", "commit", "-m", "added skills")
	commitCmd.Dir = workbenchPath
	commitOutput, commitErr := commitCmd.CombinedOutput()
	if commitErr != nil {
		log.Printf("Warning: git commit skills failed: %s, output: %s", commitErr, strings.TrimSpace(string(commitOutput)))
		return nil
	}
	log.Printf("Skills committed: %s", strings.TrimSpace(string(commitOutput)))

	pushCmd := exec.Command("git", "push", "origin", "main")
	pushCmd.Dir = workbenchPath
	pushOutput, pushErr := pushCmd.CombinedOutput()
	if pushErr != nil {
		log.Printf("Warning: git push skills failed: %s, output: %s", pushErr, strings.TrimSpace(string(pushOutput)))
	} else {
		log.Printf("Skills pushed: %s", strings.TrimSpace(string(pushOutput)))
	}

	return nil
}

// ensureSkills sets up skills if .agents folder doesn't exist or is empty.
// Called during app launch. Returns true if skills were added.
func ensureSkills(appName string) bool {
	workbenchPath := filepath.Join(WorkbenchDir, appName)
	agentsDir := filepath.Join(workbenchPath, ".agents")

	if entries, err := os.ReadDir(agentsDir); err == nil && len(entries) > 0 {
		// .agents exists and is not empty
		return false
	}

	if err := setupSkills(appName); err != nil {
		log.Printf("Warning: failed to set up skills for %s: %s", appName, err)
		return false
	}
	log.Printf("Skills set up for %s", appName)
	return true
}

// parseSkillMd parses a SKILL.md file into front matter and body
func parseSkillMd(path string) (map[string]string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}

	content := string(data)
	frontMatter := make(map[string]string)
	body := content

	// Parse front matter between --- delimiters
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		trimmed := strings.TrimSpace(content)
		rest := trimmed[3:] // skip first ---
		endIdx := strings.Index(rest, "---")
		if endIdx >= 0 {
			fmBlock := rest[:endIdx]
			body = strings.TrimSpace(rest[endIdx+3:])

			for _, line := range strings.Split(fmBlock, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					frontMatter[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	return frontMatter, body, nil
}

// listSkills reads all skills from .agents/skills/<skill>/SKILL.md
func listSkills(appName string) ([]SkillInfo, error) {
	workbenchPath := filepath.Join(WorkbenchDir, appName)
	skillsDir := filepath.Join(workbenchPath, ".agents", "skills")

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var skills []SkillInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillMdPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		fm, body, err := parseSkillMd(skillMdPath)
		if err != nil {
			log.Printf("Warning: could not read SKILL.md for %s: %s", entry.Name(), err)
			continue
		}

		skills = append(skills, SkillInfo{
			Name:        entry.Name(),
			FrontMatter: fm,
			Body:        body,
		})
	}

	return skills, nil
}

// handleSkills handles GET /api/skills/<name> and POST /api/skills/<name>/update
func handleSkills(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/skills/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]

	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}

	// Check workbench exists
	workbenchPath := filepath.Join(WorkbenchDir, name)
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		http.Error(w, "Workbench not found", http.StatusNotFound)
		return
	}

	if len(parts) == 2 && parts[1] == "update" && r.Method == http.MethodPost {
		handleSkillsUpdate(w, r, name)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	skills, err := listSkills(name)
	if err != nil {
		http.Error(w, "Failed to list skills: "+err.Error(), http.StatusInternalServerError)
		return
	}

	source := getSkillsRepo(name)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SkillsResponse{
		Skills: skills,
		Source: source,
	})
}

// handleSkillsUpdate handles POST /api/skills/<name>/update
// Removes .agents/skills and re-clones
func handleSkillsUpdate(w http.ResponseWriter, r *http.Request, name string) {
	workbenchPath := filepath.Join(WorkbenchDir, name)
	skillsDir := filepath.Join(workbenchPath, ".agents", "skills")

	// Remove existing skills
	if err := os.RemoveAll(skillsDir); err != nil {
		http.Error(w, "Failed to remove skills: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-clone
	if err := setupSkills(name); err != nil {
		http.Error(w, "Failed to update skills: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}
