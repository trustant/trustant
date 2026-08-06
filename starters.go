package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Application starters are the public repositories of the trustable-ai GitHub
// organization whose description begins with "Trustable:". Discovery always
// uses the anonymous public GitHub API — never `gh`, never the managed
// credentials in github.go — so the starter list is identical on a connected
// and a disconnected installation and never consumes the user's rate limit.
// See spec/15-starters.md.
const (
	startersOrg              = "trustable-ai"
	startersAPIBase          = "https://api.github.com"
	startersDescriptionMark  = "trustable:"
	defaultStarterTemplates  = "trustable-ai/templates"
	startersRequestTimeout   = 10 * time.Second
	startersPageSize         = 100
	startersMaxPages         = 10
	startersUserAgent        = "trustable-app"
	startersCacheTTL         = 5 * time.Minute
	startersStaleFallbackTTL = 24 * time.Hour
)

// starter is one entry of the Add App starter list.
type starter struct {
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Templates   string `json:"templates"`
	Description string `json:"description"`
}

// githubPublicRepo is the subset of the public repository payload we decode.
type githubPublicRepo struct {
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Private     bool   `json:"private"`
	Archived    bool   `json:"archived"`
	Disabled    bool   `json:"disabled"`
}

// starterKeyValue matches the <key>=<value> tokens carried in a description.
// Values are unquoted and whitespace-delimited, which is all the GitHub
// description field realistically holds.
var starterKeyValue = regexp.MustCompile(`(?i)\b([a-z][a-z0-9_-]*)=(\S+)`)

var startersCache struct {
	sync.Mutex
	list      []starter
	fetchedAt time.Time
}

// parseStarterDescription splits "Trustable: <text>" into the display text and
// the <key>=<value> parameters. ok is false when the description does not carry
// the Trustable marker, which is how non-starter repositories are filtered out.
func parseStarterDescription(description string) (text string, params map[string]string, ok bool) {
	trimmed := strings.TrimSpace(description)
	if len(trimmed) < len(startersDescriptionMark) ||
		!strings.EqualFold(trimmed[:len(startersDescriptionMark)], startersDescriptionMark) {
		return "", nil, false
	}
	rest := trimmed[len(startersDescriptionMark):]

	params = make(map[string]string)
	for _, match := range starterKeyValue.FindAllStringSubmatch(rest, -1) {
		params[strings.ToLower(match[1])] = match[2]
	}
	// Strip every parameter token from the human-readable part, then collapse
	// the whitespace the removal leaves behind.
	stripped := starterKeyValue.ReplaceAllString(rest, " ")
	text = strings.Join(strings.Fields(stripped), " ")
	return text, params, true
}

// startersFromRepos is the pure transformation from decoded repositories to the
// starter list, kept separate from the HTTP fetch so it is unit-testable.
func startersFromRepos(repos []githubPublicRepo) []starter {
	starters := make([]starter, 0, len(repos))
	for _, repo := range repos {
		if repo.Private || repo.Archived || repo.Disabled {
			continue
		}
		text, params, ok := parseStarterDescription(repo.Description)
		if !ok {
			continue
		}
		name := strings.TrimSpace(repo.Name)
		full := strings.TrimSpace(repo.FullName)
		if full == "" && name != "" {
			full = startersOrg + "/" + name
		}
		if name == "" || full == "" {
			continue
		}
		templates := strings.TrimSpace(params["templates"])
		if normalized, err := normalizeNotebookRepository(templates); err == nil {
			templates = normalized
		} else {
			templates = defaultStarterTemplates
		}
		starters = append(starters, starter{
			Name:        name,
			Repo:        full,
			Templates:   templates,
			Description: text,
		})
	}
	sort.Slice(starters, func(i, j int) bool { return starters[i].Name < starters[j].Name })
	return starters
}

// fetchStarterRepos pages the anonymous org listing. It stops at the first
// short page and refuses to loop forever on a misbehaving endpoint.
func fetchStarterRepos() ([]githubPublicRepo, error) {
	client := &http.Client{Timeout: startersRequestTimeout}
	var all []githubPublicRepo
	for page := 1; page <= startersMaxPages; page++ {
		url := fmt.Sprintf("%s/orgs/%s/repos?type=public&per_page=%d&page=%d",
			startersAPIBase, startersOrg, startersPageSize, page)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		// No Authorization header: this listing is deliberately anonymous.
		// GitHub rejects requests without a User-Agent.
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", startersUserAgent)

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("GitHub rate limit reached (HTTP %d)", resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
		}
		if readErr != nil {
			return nil, readErr
		}
		var pageRepos []githubPublicRepo
		if err := json.Unmarshal(body, &pageRepos); err != nil {
			return nil, fmt.Errorf("GitHub returned invalid repository metadata")
		}
		all = append(all, pageRepos...)
		if len(pageRepos) < startersPageSize {
			break
		}
	}
	return all, nil
}

// loadStarters returns the starter list, preferring a fresh cache entry. On a
// listing failure it serves a stale entry when one is still within the fallback
// window, so a rate limit does not empty the Add App modal.
func loadStarters() ([]starter, string) {
	startersCache.Lock()
	defer startersCache.Unlock()

	now := time.Now()
	if startersCache.list != nil && now.Sub(startersCache.fetchedAt) < startersCacheTTL {
		return startersCache.list, ""
	}

	repos, err := fetchStarterRepos()
	if err != nil {
		log.Printf("Starter listing failed: %s", err)
		if startersCache.list != nil && now.Sub(startersCache.fetchedAt) < startersStaleFallbackTTL {
			return startersCache.list, ""
		}
		return []starter{}, "Could not load application starters: " + err.Error()
	}

	startersCache.list = startersFromRepos(repos)
	startersCache.fetchedAt = now
	return startersCache.list, ""
}

// handleStarters serves GET /api/starters. It never fails the request: a
// listing error yields an empty list plus a warning so the browser can still
// offer "My Application Starter".
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
