package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Application starters are published as a static index.json in the
// trustable-ai/.github repository and read here over raw.githubusercontent.com.
// Trustable never calls the GitHub API: the index is generated and pushed by
// support/index.py, which is the only thing that talks to the API. That keeps
// discovery free of rate limits and identical on every installation.
// See spec/15-starters.md.
const (
	startersIndexURL       = "https://raw.githubusercontent.com/trustable-ai/.github/refs/heads/main/index.json"
	startersRequestTimeout = 10 * time.Second
	startersUserAgent      = "trustable-app"
	startersCacheTTL       = 5 * time.Minute
	startersMaxIndexBytes  = 4 << 20
)

// starter is one entry of the Add App starter list.
type starter struct {
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Templates   string `json:"templates"`
	Description string `json:"description"`
}

// application is one entry of the grouped application catalog. Unlike a
// starter it carries a display "title" distinct from "name": "name" is the
// slug derived from the linked .md filename and is already a legal application
// name, while "title" is what the tile shows. Its published "repo" is a full
// https://github.com/... URL, which sanitizeApplications reduces to the
// owner/repository form the rest of the app speaks.
type application struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Repo        string `json:"repo"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
}

// starterIndex is the published document. "generated" is informational.
type starterIndex struct {
	Generated    string                   `json:"generated"`
	Starters     []starter                `json:"starters"`
	Applications map[string][]application `json:"applications"`
}

var startersCache struct {
	sync.Mutex
	list         []starter
	applications map[string][]application
	fetchedAt    time.Time
}

// sanitizeStarters drops entries the index should never carry, so a malformed
// or hand-edited index cannot put an unusable row in the Add App modal.
func sanitizeStarters(entries []starter) []starter {
	starters := make([]starter, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		repo := strings.TrimSpace(entry.Repo)
		if name == "" || !repoPattern.MatchString(repo) {
			continue
		}
		templates := strings.TrimSpace(entry.Templates)
		if normalized, err := normalizeNotebookRepository(templates); err == nil {
			templates = normalized
		} else {
			templates = defaultNotebookRepository
		}
		starters = append(starters, starter{
			Name:        name,
			Repo:        repo,
			Templates:   templates,
			Description: strings.Join(strings.Fields(entry.Description), " "),
		})
	}
	sort.Slice(starters, func(i, j int) bool { return starters[i].Name < starters[j].Name })
	return starters
}

// normalizeApplicationRepo reduces a published application "repo" to the
// owner/repository form. The catalog publishes the full https://github.com/...
// URL, which repoPattern rejects, so stripping the prefix here is what keeps
// POST /api/repo receiving the same shape it gets from a starter. Returns ""
// when the value cannot be understood.
func normalizeApplicationRepo(value string) string {
	repo := strings.TrimSpace(value)
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")
	if !repoPattern.MatchString(repo) {
		return ""
	}
	return repo
}

// sanitizeApplications re-validates the grouped catalog, mirroring
// sanitizeStarters. The index is a hand-editable file, so an entry with a junk
// repository or a non-https icon must not reach the modal. Entry order within
// a group is kept as published (index.py already sorts by (title, name)).
func sanitizeApplications(groups map[string][]application) map[string][]application {
	sanitized := make(map[string][]application, len(groups))
	for group, entries := range groups {
		group = strings.Join(strings.Fields(group), " ")
		if group == "" {
			continue
		}
		kept := make([]application, 0, len(entries))
		for _, entry := range entries {
			name := strings.TrimSpace(entry.Name)
			repo := normalizeApplicationRepo(entry.Repo)
			if name == "" || repo == "" {
				continue
			}
			// An empty icon is expected: index.py deliberately publishes an
			// entry whose icon is not uploaded yet, and the tile falls back to
			// a placeholder. A non-empty one must be a plain https URL so it
			// can never carry a javascript: payload into the modal.
			icon := strings.TrimSpace(entry.Icon)
			if icon != "" && !strings.HasPrefix(icon, "https://") {
				continue
			}
			title := strings.Join(strings.Fields(entry.Title), " ")
			if title == "" {
				title = name
			}
			kept = append(kept, application{
				Name:        name,
				Title:       title,
				Repo:        repo,
				Icon:        icon,
				Description: strings.Join(strings.Fields(entry.Description), " "),
			})
		}
		if len(kept) == 0 {
			continue
		}
		sanitized[group] = kept
	}
	return sanitized
}

// fetchStarterIndex reads the published index over plain HTTPS.
func fetchStarterIndex() ([]starter, map[string][]application, error) {
	client := &http.Client{Timeout: startersRequestTimeout}
	req, err := http.NewRequest(http.MethodGet, startersIndexURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", startersUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("starter index returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, startersMaxIndexBytes))
	if err != nil {
		return nil, nil, err
	}
	var index starterIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return nil, nil, fmt.Errorf("starter index is not valid JSON")
	}
	return sanitizeStarters(index.Starters), sanitizeApplications(index.Applications), nil
}

// loadStarters returns the starter list and the grouped application catalog,
// preferring a fresh cache entry. Both come from the same single fetch and
// share the same TTL. The warm-cache check is guarded on list != nil so a
// valid index carrying no applications is not refetched on every call.
func loadStarters() ([]starter, map[string][]application, string) {
	startersCache.Lock()
	defer startersCache.Unlock()

	now := time.Now()
	if startersCache.list != nil && now.Sub(startersCache.fetchedAt) < startersCacheTTL {
		return startersCache.list, startersCache.applications, ""
	}

	list, applications, err := fetchStarterIndex()
	if err != nil {
		log.Printf("Starter index unavailable: %s", err)
		return []starter{}, map[string][]application{},
			"Could not load application starters: " + err.Error()
	}

	startersCache.list = list
	startersCache.applications = applications
	startersCache.fetchedAt = now
	return startersCache.list, startersCache.applications, ""
}

// handleStarters serves GET /api/starters. It never fails the request: an
// unreachable index yields an empty list plus a warning so the browser can
// still offer "My Application Starter". "applications" is always an object,
// never null, because the frontend iterates it unconditionally.
func handleStarters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list, applications, warning := loadStarters()
	if applications == nil {
		applications = map[string][]application{}
	}
	response := map[string]interface{}{
		"starters":     list,
		"applications": applications,
	}
	if warning != "" {
		response["warning"] = warning
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
