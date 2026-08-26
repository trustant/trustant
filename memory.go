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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// handleMemory handles GET/POST /api/memory/<name>
// GET: returns { "content": "<file contents>" }
// POST: saves content to AGENTS.md, creates and git-adds if new
func handleMemory(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/memory/")
	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid name", http.StatusBadRequest)
		return
	}

	workbenchPath := filepath.Join(WorkbenchDir, name)
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		http.Error(w, "Workbench not found", http.StatusNotFound)
		return
	}

	agentsPath := filepath.Join(workbenchPath, "AGENTS.md")

	switch r.Method {
	case http.MethodGet:
		content, err := os.ReadFile(agentsPath)
		if err != nil {
			if os.IsNotExist(err) {
				// File doesn't exist yet, return empty content
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"content": ""})
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"content": string(content)})

	case http.MethodPost:
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		isNew := false
		if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
			isNew = true
		}

		if err := os.WriteFile(agentsPath, []byte(req.Content), 0644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Git-add if the file was just created
		if isNew {
			cmd := exec.Command("git", "add", "AGENTS.md")
			cmd.Dir = workbenchPath
			if out, err := cmd.CombinedOutput(); err != nil {
				http.Error(w, "git add failed: "+string(out), http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
