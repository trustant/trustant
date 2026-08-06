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

// starterIndex is the published document. "generated" is informational.
type starterIndex struct {
	Generated string    `json:"generated"`
	Starters  []starter `json:"starters"`
}

var startersCache struct {
	sync.Mutex
	list      []starter
	fetchedAt time.Time
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

// fetchStarterIndex reads the published index over plain HTTPS.
func fetchStarterIndex() ([]starter, error) {
	client := &http.Client{Timeout: startersRequestTimeout}
	req, err := http.NewRequest(http.MethodGet, startersIndexURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", startersUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("starter index returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, startersMaxIndexBytes))
	if err != nil {
		return nil, err
	}
	var index starterIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return nil, fmt.Errorf("starter index is not valid JSON")
	}
	return sanitizeStarters(index.Starters), nil
}

// loadStarters returns the starter list, preferring a fresh cache entry.
func loadStarters() ([]starter, string) {
	startersCache.Lock()
	defer startersCache.Unlock()

	now := time.Now()
	if startersCache.list != nil && now.Sub(startersCache.fetchedAt) < startersCacheTTL {
		return startersCache.list, ""
	}

	list, err := fetchStarterIndex()
	if err != nil {
		log.Printf("Starter index unavailable: %s", err)
		return []starter{}, "Could not load application starters: " + err.Error()
	}

	startersCache.list = list
	startersCache.fetchedAt = now
	return startersCache.list, ""
}

// handleStarters serves GET /api/starters. It never fails the request: an
// unreachable index yields an empty list plus a warning so the browser can
// still offer "My Application Starter".
func handleStarters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list, warning := loadStarters()
	response := map[string]interface{}{"starters": list}
	if warning != "" {
		response["warning"] = warning
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
