package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// piConfig holds the single coding model selected for Pi. Pi has no secondary
// "small model" role, so keeping a second field would create configuration that
// the runtime cannot consume.
type piConfig struct {
	Default string `json:"default"`
}

// ModelLimits is the per-model hint block from /api/v2/status and what we
// persist in trustable.json under the active provider's `models` map.
// All three fields are optional (omitempty); zero values are dropped.
type ModelLimits struct {
	MaxToken         int                `json:"maxToken,omitempty"`
	MaxInput         int                `json:"maxInput,omitempty"`
	MaxOutput        int                `json:"maxOutput,omitempty"`
	Reasoning        *bool              `json:"reasoning,omitempty"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap,omitempty"`
	Enabled          *bool              `json:"enabled,omitempty"`
	Recommended      bool               `json:"recommended,omitempty"`
	Roles            []string           `json:"roles,omitempty"`
	Reason           string             `json:"reason,omitempty"`
}

// UnmarshalJSON accepts the new object form AND the legacy "256K" string form
// so workspace trustable.json files written by older builds keep loading.
func (m *ModelLimits) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		n, err := parseContextSize(s)
		if err != nil || n == 0 {
			*m = ModelLimits{}
			return nil
		}
		*m = ModelLimits{MaxToken: n}
		return nil
	}
	type raw ModelLimits
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*m = ModelLimits(r)
	return nil
}

// AppConfig holds per-app configuration within trustable.json
type AppConfig struct {
	Password    string            `json:"password"`
	Development map[string]string `json:"development"`
	Production  map[string]string `json:"production"`
}

// GitConfig holds git user configuration
type GitConfig struct {
	User  string `json:"user"`
	Email string `json:"email"`
}

// NotebookConfig contains the non-secret GitHub source selected in Configure.
// The write token is stored separately under WorkspaceDir and never serialized
// into trustable.json or returned to the browser.
type NotebookConfig struct {
	Repository string `json:"repository,omitempty"`
	Ref        string `json:"ref,omitempty"`
}

// trustableConfig represents the structure of trustable.json
type trustableConfig struct {
	Provider string `json:"provider,omitempty"`
	// BaseURL and APIKey are the top-level provider credentials.
	// Ollama: BaseURL="http://localhost:11434/v1", APIKey="dummy".
	// Trustable: posted by the ai-proxy registration iframe.
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	// License is the signed "lic_<payload>.<sig>" token gating git push and
	// production publishing. Workspace-only, like Provider and Apps: the base
	// trustable.json never carries it. See spec/14-license.md.
	License string `json:"license,omitempty"`
	// ModelVersions is keyed by provider name ("ollama", "trustable", ...) and
	// stores the last per-provider `modelsVersion` value seen from /api/v2/status.
	// On every splash boot and every applist load the frontend compares the
	// live value against this map; only a version mismatch routes the user
	// through configure.html?reselect=1. A different Pi default is a valid user
	// choice and must not be treated as catalog drift.
	ModelVersions map[string]int          `json:"model_versions,omitempty"`
	Models        map[string]*ModelLimits `json:"models,omitempty"`
	Pi            *piConfig               `json:"pi,omitempty"`
	Git           *GitConfig              `json:"git,omitempty"`
	Notebook      *NotebookConfig         `json:"notebook,omitempty"`
	Apps          map[string]*AppConfig   `json:"apps,omitempty"`
	Current       string                  `json:"current,omitempty"`

	// RegisterURL is populated at GET-time from the AIP_REGISTER_URL env var
	// (mandatory at startup). It points at the proxy's registration UI; the
	// top-up form lives at <register_url>/top-up. Not persisted.
	RegisterURL string `json:"register_url,omitempty"`
}

// UnmarshalJSON tolerates the legacy singular `model_version` field by
// folding it into ModelVersions under the active provider key. Lets existing
// workspace trustable.json files written by older builds load cleanly.
func (c *trustableConfig) UnmarshalJSON(data []byte) error {
	type alias trustableConfig
	aux := &struct {
		LegacyModelVersion *int `json:"model_version,omitempty"`
		*alias
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if aux.LegacyModelVersion != nil && *aux.LegacyModelVersion != 0 {
		if c.ModelVersions == nil {
			c.ModelVersions = map[string]int{}
		}
		key := c.Provider
		if key == "" {
			key = "_legacy"
		}
		if _, ok := c.ModelVersions[key]; !ok {
			c.ModelVersions[key] = *aux.LegacyModelVersion
		}
	}
	return nil
}

func developmentAPIHost() string {
	for _, key := range []string{"OPS_APIHOST", "APIHOST", "TRUSTABLE_DEFAULT_APIHOST", "OPERATOR_CONFIG_APIHOST"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
				proto := strings.TrimSpace(os.Getenv("OPERATOR_CONFIG_HOSTPROTOCOL"))
				if proto != "https" {
					proto = "http"
				}
				value = proto + "://" + value
			}
			return strings.TrimRight(value, "/")
		}
	}
	if path := apihostFilePath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if value := strings.TrimSpace(string(data)); value != "" {
				return strings.TrimRight(value, "/")
			}
		}
	}
	return "http://miniops.me"
}

// apihostFilePath returns the OS-specific path to the user-level apihost file,
// or "" if the platform has no defined location.
func apihostFilePath() string {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", "Trustable", "apihost")
	case "windows":
		appData := strings.TrimSpace(os.Getenv("APPDATA"))
		if appData == "" {
			return ""
		}
		return filepath.Join(appData, "Trustable", "apihost")
	default:
		return ""
	}
}

// loadBaseConfig reads the app-root trustable.json (immutable defaults)
func loadBaseConfig() (*trustableConfig, error) {
	data, err := os.ReadFile("trustable.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read base trustable.json: %w", err)
	}
	var cfg trustableConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse base trustable.json: %w", err)
	}
	return &cfg, nil
}

// loadWorkspaceConfig reads the workspace trustable.json (overrides)
func loadWorkspaceConfig() (*trustableConfig, error) {
	configPath := filepath.Join(WorkspaceDir, "trustable.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &trustableConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read workspace trustable.json: %w", err)
	}
	var cfg trustableConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse workspace trustable.json: %w", err)
	}
	return &cfg, nil
}

// mergeConfigs merges workspace overrides onto base config.
// Non-nil/non-empty workspace fields override base fields.
// Maps are merged key-by-key (workspace keys override base keys).
func mergeConfigs(base, override *trustableConfig) *trustableConfig {
	result := *base // shallow copy

	if override.Provider != "" {
		result.Provider = override.Provider
	}

	if override.BaseURL != "" {
		result.BaseURL = override.BaseURL
	}

	if override.APIKey != "" {
		result.APIKey = override.APIKey
	}

	if len(override.ModelVersions) > 0 {
		merged := make(map[string]int)
		for k, v := range base.ModelVersions {
			merged[k] = v
		}
		for k, v := range override.ModelVersions {
			merged[k] = v
		}
		result.ModelVersions = merged
	}

	if len(override.Models) > 0 {
		merged := make(map[string]*ModelLimits)
		for k, v := range base.Models {
			merged[k] = v
		}
		for k, v := range override.Models {
			merged[k] = v
		}
		result.Models = merged
	}

	if override.Pi != nil {
		result.Pi = override.Pi
	}

	if override.Git != nil {
		result.Git = override.Git
	}

	if override.Notebook != nil {
		notebook := NotebookConfig{}
		if base.Notebook != nil {
			notebook = *base.Notebook
		}
		if override.Notebook.Repository != "" {
			notebook.Repository = override.Notebook.Repository
		}
		if override.Notebook.Ref != "" {
			notebook.Ref = override.Notebook.Ref
		}
		result.Notebook = &notebook
	}

	if override.Apps != nil {
		result.Apps = override.Apps
	}

	return &result
}

// loadTrustableConfig loads merged config (base + workspace overrides).
// The AIP_REGISTER_URL env var (mandatory at startup) is exposed on the
// returned config as RegisterURL; it is not persisted.
func loadTrustableConfig() (*trustableConfig, error) {
	base, err := loadBaseConfig()
	if err != nil {
		return nil, err
	}
	ws, err := loadWorkspaceConfig()
	if err != nil {
		return nil, err
	}
	cfg := mergeConfigs(base, ws)
	cfg.RegisterURL = AIPRegisterURL
	return cfg, nil
}

// saveWorkspaceConfig writes only the workspace trustable.json
func saveWorkspaceConfig(cfg *trustableConfig) error {
	formatted, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to format configuration: %w", err)
	}
	configPath := filepath.Join(WorkspaceDir, "trustable.json")
	if err := os.MkdirAll(WorkspaceDir, 0755); err != nil {
		return fmt.Errorf("failed to create workspace dir: %w", err)
	}
	return os.WriteFile(configPath, formatted, 0644)
}

const (
	defaultNotebookRepository = "trustable-ai/templates"
	defaultNotebookRef        = "main"
)

func notebookGitHubTokenPath() string {
	return filepath.Join(
		WorkspaceDir,
		".trustable",
		"secrets",
		"notebook-github-token",
	)
}

func readNotebookGitHubToken() (string, error) {
	data, err := os.ReadFile(notebookGitHubTokenPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read notebook GitHub token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func validateNotebookGitHubTokenUpdate(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", nil
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("notebook GitHub token cannot contain whitespace")
	}
	return token, nil
}

func updateNotebookGitHubToken(token string, clear bool) error {
	path := notebookGitHubTokenPath()
	if clear {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to clear notebook GitHub token: %w", err)
		}
		return nil
	}
	token, err := validateNotebookGitHubTokenUpdate(token)
	if err != nil {
		return err
	}
	if token == "" {
		return nil
	}
	secretDir := filepath.Dir(path)
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return fmt.Errorf("failed to create notebook secret directory: %w", err)
	}
	if err := os.Chmod(secretDir, 0700); err != nil {
		return fmt.Errorf("failed to protect notebook secret directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(token), 0600); err != nil {
		return fmt.Errorf("failed to save notebook GitHub token: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("failed to protect notebook GitHub token: %w", err)
	}
	return nil
}

func isNotebookRepositorySegment(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	for index, char := range value {
		alphaNumeric := char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9'
		if index == 0 || index == len(value)-1 {
			if !alphaNumeric {
				return false
			}
			continue
		}
		if !alphaNumeric && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func normalizeNotebookRepository(value string) (string, error) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, prefix := range []string{"https://github.com/", "http://github.com/"} {
		if strings.HasPrefix(lower, prefix) {
			value = value[len(prefix):]
			break
		}
	}
	if strings.Contains(value, "://") {
		return "", fmt.Errorf("notebook source must use github.com")
	}
	value = strings.Trim(strings.TrimSuffix(value, ".git"), "/")
	parts := strings.Split(value, "/")
	if len(parts) != 2 ||
		!isNotebookRepositorySegment(parts[0]) ||
		!isNotebookRepositorySegment(parts[1]) {
		return "", fmt.Errorf("notebook source must be owner/repository or a GitHub repository URL")
	}
	return parts[0] + "/" + parts[1], nil
}

func normalizeNotebookRef(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultNotebookRef
	}
	if len(value) > 200 ||
		strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") ||
		strings.Contains(value, "..") ||
		strings.Contains(value, "\\") {
		return "", fmt.Errorf("invalid notebook branch/ref")
	}
	hasAlphaNumeric := false
	for _, char := range value {
		alphaNumeric := char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9'
		if alphaNumeric {
			hasAlphaNumeric = true
			continue
		}
		if char != '.' && char != '_' && char != '/' && char != '-' {
			return "", fmt.Errorf("invalid notebook branch/ref")
		}
	}
	if !hasAlphaNumeric {
		return "", fmt.Errorf("invalid notebook branch/ref")
	}
	return value, nil
}

func normalizeNotebookConfig(cfg *trustableConfig) error {
	if cfg.Notebook == nil {
		cfg.Notebook = &NotebookConfig{
			Repository: defaultNotebookRepository,
			Ref:        defaultNotebookRef,
		}
	}
	repository, err := normalizeNotebookRepository(cfg.Notebook.Repository)
	if err != nil {
		return err
	}
	ref, err := normalizeNotebookRef(cfg.Notebook.Ref)
	if err != nil {
		return err
	}
	cfg.Notebook.Repository = repository
	cfg.Notebook.Ref = ref
	return nil
}

func notebookRuntimeEnvironment() ([]string, error) {
	cfg, err := loadTrustableConfig()
	if err != nil {
		return nil, err
	}
	if err := normalizeNotebookConfig(cfg); err != nil {
		return nil, err
	}
	token, err := readNotebookGitHubToken()
	if err != nil {
		return nil, err
	}
	return []string{
		"NOTEBOOK_GITHUB_REPOSITORY=" + cfg.Notebook.Repository,
		"NOTEBOOK_GITHUB_REF=" + cfg.Notebook.Ref,
		"NOTEBOOK_GITHUB_TOKEN=" + token,
	}, nil
}

func configurationPayload(cfg *trustableConfig) (map[string]interface{}, error) {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, err
	}
	notebook, _ := payload["notebook"].(map[string]interface{})
	if notebook == nil {
		notebook = map[string]interface{}{}
	}
	token, err := readNotebookGitHubToken()
	if err != nil {
		return nil, err
	}
	notebook["has_token"] = token != ""
	payload["notebook"] = notebook
	return payload, nil
}

// modelLimitsEqual reports whether two map[string]*ModelLimits values have
// identical key sets and identical per-model hint blocks. Nil pointers compare
// equal to nil pointers. Used by the preflight migration to detect whether a
// workspace `models` override is just a copy of the base config.
func modelLimitsEqual(a, b map[string]*ModelLimits) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if av == nil && bv == nil {
			continue
		}
		if av == nil || bv == nil {
			return false
		}
		if av.MaxToken != bv.MaxToken ||
			av.MaxInput != bv.MaxInput ||
			av.MaxOutput != bv.MaxOutput ||
			!boolPtrEqual(av.Reasoning, bv.Reasoning) ||
			!stringPtrMapsEqual(av.ThinkingLevelMap, bv.ThinkingLevelMap) ||
			!boolPtrEqual(av.Enabled, bv.Enabled) ||
			av.Recommended != bv.Recommended ||
			av.Reason != bv.Reason ||
			!stringSlicesEqual(av.Roles, bv.Roles) {
			return false
		}
	}
	return true
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func stringPtrMapsEqual(a, b map[string]*string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, av := range a {
		bv, ok := b[key]
		if !ok {
			return false
		}
		if av == nil || bv == nil {
			if av != nil || bv != nil {
				return false
			}
			continue
		}
		if *av != *bv {
			return false
		}
	}
	return true
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// parseContextSize parses a context size string like "256K" or "512"
func parseContextSize(s string) (int, error) {
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)
	if strings.HasSuffix(upper, "K") {
		val, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil {
			return 0, err
		}
		return val * 1024, nil
	}
	return strconv.Atoi(s)
}

var modelParamSizePattern = regexp.MustCompile(`(?i)(^|[/:_-])([0-9]+(?:\.[0-9]+)?)b($|[/:_-])`)

func modelHasRole(limits *ModelLimits, roles ...string) bool {
	if limits == nil {
		return false
	}
	for _, got := range limits.Roles {
		got = strings.ToLower(strings.TrimSpace(got))
		for _, want := range roles {
			if got == strings.ToLower(want) {
				return true
			}
		}
	}
	return false
}

func modelAllowedForPi(provider, modelID string, limits *ModelLimits) (bool, string) {
	name := strings.ToLower(strings.TrimSpace(modelID))
	if name == "" {
		return false, "model id is empty"
	}
	if limits != nil && limits.Enabled != nil && !*limits.Enabled {
		reason := strings.TrimSpace(limits.Reason)
		if reason == "" {
			reason = "disabled by model catalog"
		}
		return false, reason
	}
	if modelHasRole(limits, "embedding", "embed", "rerank", "vector") {
		return false, "not a chat/coding model"
	}
	// Catalogs may still use the historical "opencode" role. It describes model
	// capability rather than a runtime dependency, so accept it during the Pi
	// cutover while all newly emitted configuration remains Pi-native.
	if modelHasRole(limits, "opencode", "agent", "coding", "chat") {
		return true, ""
	}

	blockedNameParts := []string{
		"embedding",
		"embed-text",
		"nomic-embed",
		"gte-",
		"bge-",
		"rerank",
		"/tiny:",
		"/small:",
		"flash:e4b",
		"whisper",
		"tts",
		"vision",
		"audio",
	}
	for _, part := range blockedNameParts {
		if strings.Contains(name, part) {
			return false, "not suitable for Pi agent work"
		}
	}

	if match := modelParamSizePattern.FindStringSubmatch(name); len(match) >= 3 {
		params, err := strconv.ParseFloat(match[2], 64)
		if err == nil && params > 0 && params < 20 {
			return false, "model is below the recommended 20B minimum for Pi agent work"
		}
	}

	return true, ""
}

func validatePiModelSelection(cfg *trustableConfig) error {
	if cfg == nil {
		return nil
	}
	models := cfg.Models
	defaultModel := ""
	if cfg.Pi != nil {
		defaultModel = strings.TrimSpace(cfg.Pi.Default)
	}

	// The own-host Ollama choice intentionally persists an empty model set
	// first; configure.html discovers models in the next step. Private AI does
	// not appear here — it discovers its models before persisting anything.
	if len(models) == 0 && defaultModel == "" {
		// Trustable Cloud is catalog-backed and has no deferred discovery page.
		// Rejecting its empty state here prevents a partial status response or
		// legacy payload from being saved and failing later in testmodel.
		if cfg.Provider == "trustable" {
			return fmt.Errorf("Trustable model catalog is empty")
		}
		return nil
	}
	if defaultModel == "" {
		return fmt.Errorf("pi.default model must be selected")
	}
	limits, ok := models[defaultModel]
	if !ok {
		return fmt.Errorf("pi.default model %q is not in the configured model list", defaultModel)
	}
	if ok, reason := modelAllowedForPi(cfg.Provider, defaultModel, limits); !ok {
		return fmt.Errorf("pi.default model %q is not allowed: %s", defaultModel, reason)
	}
	return nil
}

// numberWithExtPattern matches a number followed by a size suffix like "480b", "1.7b", "123b"
var numberWithExtPattern = regexp.MustCompile(`^(\d+\.?\d*[a-zA-Z]+)$`)

// modelDisplayName converts a model id to a display name
// Split on "-" and ":", capitalize each part, put numbers with extensions in parentheses
// Example: qwen3-coder:480b-cloud => Qwen3 Coder (480B) Cloud
func modelDisplayName(modelID string) string {
	parts := strings.FieldsFunc(modelID, func(r rune) bool {
		return r == '-' || r == ':'
	})
	var result []string
	for _, p := range parts {
		if numberWithExtPattern.MatchString(p) {
			result = append(result, "("+strings.ToUpper(p)+")")
		} else {
			// Capitalize first letter
			runes := []rune(p)
			if len(runes) > 0 {
				runes[0] = unicode.ToUpper(runes[0])
			}
			result = append(result, string(runes))
		}
	}
	return strings.Join(result, " ")
}

// resolveOllamaRoot returns the HTTP root for talking to Ollama (i.e. the
// base used to build /api/show, /api/pull, /api/tags) and a flag indicating
// whether the user has pointed Trustable at their own remote host.
//
// "Own host" means cfg.BaseURL is set AND its host is neither the in-VM
// embedded Ollama (localhost / 127.0.0.1 / "ollama") at port 11434. In that
// case we return cfg.BaseURL stripped of its /v1 suffix. Otherwise we fall
// back to the OLLAMA_ENDPOINT loaded by preflight.
func resolveOllamaRoot(cfg *trustableConfig) (root string, isOwnHost bool) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return OllamaEndpoint, false
	}
	stripped := strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")
	parsed, err := url.Parse(stripped)
	if err != nil || parsed.Host == "" {
		return OllamaEndpoint, false
	}
	host := parsed.Hostname()
	switch host {
	case "localhost", "127.0.0.1", "ollama":
		return OllamaEndpoint, false
	}
	return stripped, true
}

const (
	piLocalProviderName     = "local"
	piOllamaProviderName    = "ollama"
	piTrustableProviderName = "trustable"
	// Pi resolves this reference through auth.json, keeping the real secret out
	// of the model catalog regardless of the selected provider origin.
	piAPIKeyRef = "$OPENAI_API_KEY"
)

// piProviderNameForConfig keeps Pi's provider prefix aligned with the source
// boundary shown to users: status-backed catalogs retain their product name,
// while user-supplied/direct endpoints share the neutral "local" namespace.
func piProviderNameForConfig(cfg *trustableConfig) string {
	if cfg == nil {
		return piLocalProviderName
	}
	switch cfg.Provider {
	case "trustable":
		return piTrustableProviderName
	case "ollama":
		_, ownHost := resolveOllamaRoot(cfg)
		if !ownHost {
			return piOllamaProviderName
		}
	}
	return piLocalProviderName
}

// piAgentDir follows Pi's native directory contract while allowing tests and
// packaged runtimes to relocate the state without inventing another config tree.
func piAgentDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home dir for pi config: %w", err)
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// readPiJSONFile is deliberately merge-friendly: a fresh or damaged optional
// file is treated as empty, while valid unrelated user-owned keys are preserved.
func readPiJSONFile(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: failed to read %s: %s", path, err)
		}
		return make(map[string]interface{})
	}
	parsed := make(map[string]interface{})
	if err := json.Unmarshal(data, &parsed); err != nil {
		log.Printf("Warning: ignoring unparseable %s: %s", path, err)
		return make(map[string]interface{})
	}
	return parsed
}

// writePiJSONFile reapplies the requested mode after every write because Pi's
// auth file contains a real credential whereas settings.json is non-secret.
func writePiJSONFile(path string, content map[string]interface{}, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create pi config directory: %w", err)
	}
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), mode); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("failed to chmod %s: %w", path, err)
	}
	return nil
}

type piGlobalConfigFile struct {
	name string
	mode os.FileMode
}

var piGlobalConfigFiles = []piGlobalConfigFile{
	{name: "models.json", mode: 0600},
	{name: "settings.json", mode: 0644},
	{name: "auth.json", mode: 0600},
}

// piPersistentConfigDir keeps only Pi's small managed JSON files on the
// workspace volume. WHY: the npm extension tree is supplied by each image and
// must be allowed to upgrade, while models/auth/settings must survive replacing
// the pod's otherwise ephemeral home directory.
func piPersistentConfigDir() string {
	if strings.TrimSpace(WorkspaceDir) == "" {
		return ""
	}
	return filepath.Join(WorkspaceDir, ".trustable", "pi-agent-config")
}

func readPiJSONFileStrict(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed := make(map[string]interface{})
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return parsed, nil
}

// persistPiGlobalConfig snapshots the successfully written live configuration.
// The containing directory is private because auth.json contains the real key.
func persistPiGlobalConfig(liveDir string) error {
	persistentDir := piPersistentConfigDir()
	if persistentDir == "" {
		return nil
	}
	liveAbs, _ := filepath.Abs(liveDir)
	persistentAbs, _ := filepath.Abs(persistentDir)
	if liveAbs == persistentAbs {
		return nil
	}
	if err := os.MkdirAll(persistentDir, 0700); err != nil {
		return fmt.Errorf("failed to create persistent pi config directory: %w", err)
	}
	if err := os.Chmod(persistentDir, 0700); err != nil {
		return fmt.Errorf("failed to secure persistent pi config directory: %w", err)
	}
	for _, file := range piGlobalConfigFiles {
		content, err := readPiJSONFileStrict(filepath.Join(liveDir, file.name))
		if err != nil {
			return fmt.Errorf("failed to snapshot pi %s: %w", file.name, err)
		}
		if err := writePiJSONFile(filepath.Join(persistentDir, file.name), content, file.mode); err != nil {
			return fmt.Errorf("failed to persist pi %s: %w", file.name, err)
		}
	}
	return nil
}

// restorePiGlobalConfigAtStartup overlays the durable JSON snapshot onto the
// fresh image's Pi directory, preserving the image's current package registry.
// On the first upgraded boot there is no snapshot yet, so the already-persisted
// Trustable provider selection is materialized once without changing it or
// performing a new network probe.
func restorePiGlobalConfigAtStartup() error {
	liveDir, err := piAgentDir()
	if err != nil {
		return err
	}
	persistentDir := piPersistentConfigDir()
	restored := 0
	if persistentDir != "" {
		for _, file := range piGlobalConfigFiles {
			persistentPath := filepath.Join(persistentDir, file.name)
			persistent, readErr := readPiJSONFileStrict(persistentPath)
			if readErr != nil {
				if os.IsNotExist(readErr) {
					continue
				}
				log.Printf("Warning: ignoring invalid persistent Pi %s: %s", file.name, readErr)
				continue
			}
			live := readPiJSONFile(filepath.Join(liveDir, file.name))
			imagePackages, imageHasPackages := live["packages"]
			for key, value := range persistent {
				live[key] = value
			}
			// setup.sh owns the installed extension set. WHY: a persisted
			// settings file from an older pod must not pin stale image packages.
			if file.name == "settings.json" && imageHasPackages {
				live["packages"] = imagePackages
			}
			if err := writePiJSONFile(filepath.Join(liveDir, file.name), live, file.mode); err != nil {
				return fmt.Errorf("failed to restore pi %s: %w", file.name, err)
			}
			restored++
		}
	}

	cfg, err := loadTrustableConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration for pi restore: %w", err)
	}
	if piDefaultModel(cfg) != "" {
		// Re-render the selected managed provider so a first upgrade can recover
		// from workspace state and a later image cannot retain stale limits.
		return writePiGlobalConfig(cfg)
	}
	if restored > 0 {
		log.Printf("Restored %d Pi configuration files; no managed default is selected", restored)
	}
	return nil
}

// piDefaultModel reads the only model selector supported by Pi. Legacy
// opencode configuration is intentionally ignored: issue #51 defines a hard
// cutover so first-run recovery happens through Configure instead of migration.
func piDefaultModel(cfg *trustableConfig) string {
	if cfg == nil || cfg.Pi == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Pi.Default)
}

var piThinkingLevels = map[string]struct{}{
	"off": {}, "minimal": {}, "low": {}, "medium": {},
	"high": {}, "xhigh": {}, "max": {},
}

// piReasoningConfig translates only capabilities that Pi can enforce. Trustable
// Cloud supplies the high-effort compatibility baseline; extended levels remain
// model declarations so the UI cannot claim xhigh while Pi clamps it to high.
func piReasoningConfig(provider string, limits *ModelLimits) (bool, map[string]*string) {
	reasoning := provider == piTrustableProviderName
	if limits != nil && limits.Reasoning != nil {
		reasoning = *limits.Reasoning
	}
	if !reasoning || limits == nil || len(limits.ThinkingLevelMap) == 0 {
		return reasoning, nil
	}
	levelMap := make(map[string]*string)
	for level, value := range limits.ThinkingLevelMap {
		if _, allowed := piThinkingLevels[level]; !allowed {
			continue
		}
		levelMap[level] = value
	}
	if len(levelMap) == 0 {
		return reasoning, nil
	}
	return reasoning, levelMap
}

// buildPiModels converts Trustable's catalog into Pi's native shape, retaining
// only coding-capable models and conservative limits when metadata is incomplete.
func buildPiModels(cfg *trustableConfig) []map[string]interface{} {
	ids := make([]string, 0, len(cfg.Models)+1)
	for modelID := range cfg.Models {
		ids = append(ids, modelID)
	}
	defaultModel := piDefaultModel(cfg)
	if defaultModel != "" {
		if _, found := cfg.Models[defaultModel]; !found {
			ids = append(ids, defaultModel)
		}
	}
	sort.Strings(ids)

	models := make([]map[string]interface{}, 0, len(ids))
	for _, modelID := range ids {
		limits := cfg.Models[modelID]
		if ok, reason := modelAllowedForPi(cfg.Provider, modelID, limits); !ok {
			log.Printf("Skipping Pi model %s: %s", modelID, reason)
			continue
		}
		contextWindow, maxTokens := 32768, 32768
		if limits != nil {
			if limits.MaxToken > 0 {
				contextWindow = limits.MaxToken
			} else if limits.MaxInput > 0 {
				contextWindow = limits.MaxInput
			}
			if limits.MaxOutput > 0 {
				maxTokens = limits.MaxOutput
			}
		}
		reasoning, thinkingLevelMap := piReasoningConfig(cfg.Provider, limits)
		model := map[string]interface{}{
			"id":            modelID,
			"name":          modelDisplayName(modelID),
			"contextWindow": contextWindow,
			"maxTokens":     maxTokens,
			"reasoning":     reasoning,
		}
		if len(thinkingLevelMap) > 0 {
			model["thinkingLevelMap"] = thinkingLevelMap
		}
		models = append(models, model)
	}
	return models
}

// piBaseURL normalizes every provider to the OpenAI-compatible API consumed by
// Pi; Ollama needs an explicit /v1 suffix while cloud endpoints already include it.
func piBaseURL(cfg *trustableConfig) string {
	if cfg.Provider == "ollama" {
		root, _ := resolveOllamaRoot(cfg)
		return strings.TrimRight(root, "/") + "/v1"
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return "http://localhost:11434/v1"
	}
	return baseURL
}

// writePiGlobalConfig materializes Trustable's selected provider in Pi's
// native config. It intentionally preserves unrelated Pi providers/settings.
// Configure owns this global write; app launch only emits project-local assets.
func writePiGlobalConfig(cfg *trustableConfig) error {
	if cfg == nil {
		return fmt.Errorf("configuration not loaded")
	}
	defaultModel := piDefaultModel(cfg)
	if defaultModel == "" {
		return fmt.Errorf("no default coding model selected; open Configure first")
	}
	models := buildPiModels(cfg)
	if len(models) == 0 {
		return fmt.Errorf("no configured model is suitable for Pi")
	}
	providerName := piProviderNameForConfig(cfg)
	dir, err := piAgentDir()
	if err != nil {
		return err
	}

	modelsPath := filepath.Join(dir, "models.json")
	modelsConfig := readPiJSONFile(modelsPath)
	providers, _ := modelsConfig["providers"].(map[string]interface{})
	if providers == nil {
		providers = make(map[string]interface{})
	}
	providers[providerName] = map[string]interface{}{
		"baseUrl": piBaseURL(cfg),
		"api":     "openai-completions",
		// Do not put cfg.APIKey here: models.json is configuration, while the
		// matching secret is written to auth.json below.
		"apiKey": piAPIKeyRef,
		"models": models,
	}
	modelsConfig["providers"] = providers
	if err := writePiJSONFile(modelsPath, modelsConfig, 0600); err != nil {
		return err
	}

	settingsPath := filepath.Join(dir, "settings.json")
	settings := readPiJSONFile(settingsPath)
	settings["defaultProvider"] = providerName
	settings["defaultModel"] = defaultModel
	// Pi knows built-in providers even when Trustable configures only its own.
	// Scope model cycling to the active managed prefix so stale or built-in
	// providers cannot become active through Pi's selector or shortcuts.
	settings["enabledModels"] = []string{providerName + "/*"}
	if err := writePiJSONFile(settingsPath, settings, 0644); err != nil {
		return err
	}

	authPath := filepath.Join(dir, "auth.json")
	auth := readPiJSONFile(authPath)
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = "dummy"
	}
	auth[providerName] = map[string]interface{}{"type": "api_key", "key": apiKey}
	if err := writePiJSONFile(authPath, auth, 0600); err != nil {
		return err
	}
	return persistPiGlobalConfig(dir)
}

// handleConfigure handles GET /api/configure - pulls models and materializes Pi's global config, streaming progress.
func handleConfigure(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set up streaming response
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	sendMsg := func(msg string) {
		fmt.Fprintf(w, "%s\n", msg)
		flusher.Flush()
	}

	// Load trustable.json config first so we can branch on provider.
	cfg, err := loadTrustableConfig()
	if err != nil {
		sendMsg("ERROR: " + err.Error())
		return
	}

	// Provider must already be chosen (handled by the splash flow). Do not
	// prompt for provider here; just refuse to configure if it's missing.
	if cfg.Provider == "" {
		sendMsg("ERROR: provider not set; choose a provider on the splash page first")
		return
	}

	// Configure git user (provider-independent). Run from $HOME so git does not
	// discover the process CWD's .git — in a submodule checkout that .git points
	// at a parent gitdir that may be absent, making even `git config --global`
	// fail with exit 128.
	if cfg.Git != nil {
		gitConfigGlobal := func(key, val string) error {
			c := exec.Command("git", "config", "--global", key, val)
			if home, err := os.UserHomeDir(); err == nil {
				c.Dir = home
			}
			return c.Run()
		}
		if cfg.Git.User != "" {
			if err := gitConfigGlobal("user.name", cfg.Git.User); err != nil {
				sendMsg("ERROR: failed to set git user.name: " + err.Error())
			} else {
				sendMsg("OK: git user.name set to " + cfg.Git.User)
			}
		}
		if cfg.Git.Email != "" {
			if err := gitConfigGlobal("user.email", cfg.Git.Email); err != nil {
				sendMsg("ERROR: failed to set git user.email: " + err.Error())
			} else {
				sendMsg("OK: git user.email set to " + cfg.Git.Email)
			}
		}
	}

	if cfg.Provider == "trustable" || cfg.Provider == "private" {
		if cfg.Provider == "private" {
			// A user-supplied OpenAI-compatible endpoint must not go through
			// the Ollama connectivity check and model-pull loop.
			sendMsg("OK: Skipping Ollama setup (Private AI)")
		} else {
			sendMsg("OK: Skipping Ollama setup (Trustable Cloud)")
		}
	} else {
		ollamaRoot, isOwnHost := resolveOllamaRoot(cfg)

		// Step 1: Check connectivity with retries by hitting the OpenAI-
		// compatible /v1/models endpoint. This matches the Test button and
		// works against any OpenAI-compatible server (Ollama, vLLM, etc.).
		probe := strings.TrimRight(ollamaRoot, "/") + "/v1/models"
		sendMsg("Checking Ollama connection at " + probe + "...")
		ollamaOK := false
		maxAttempts := 12 // 2 minutes at 10-second intervals
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(probe)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					sendMsg("OK: Ollama is running")
					ollamaOK = true
					break
				}
			}
			if attempt < maxAttempts {
				sendMsg(fmt.Sprintf("Attempt %d/%d: Cannot reach Ollama at %s - retrying in 10 seconds...", attempt, maxAttempts, probe))
				time.Sleep(10 * time.Second)
			} else {
				sendMsg(fmt.Sprintf("ERROR: Cannot connect to Ollama at %s after 2 minutes. Please check that Ollama is running and try again.", probe))
			}
		}
		if !ollamaOK {
			return
		}

		// Step 2: Pull each model — only for internal Ollama. When the user
		// points at their own host, the models were discovered there via
		// /api/tags and are already present; pulling them again would be
		// redundant and slow.
		if isOwnHost {
			sendMsg("OK: Skipping model pull (using your own Ollama host — models are already installed there)")
		} else {
			client := &http.Client{Timeout: 600 * time.Second}
			for modelName := range cfg.Models {
				sendMsg("Pulling model " + modelName)
				log.Printf("  - Pulling %s...", modelName)

				reqBody, _ := json.Marshal(map[string]string{"name": modelName})
				resp, err := client.Post(ollamaRoot+"/api/pull", "application/json", bytes.NewReader(reqBody))
				if err != nil {
					sendMsg("ERROR: Failed to pull " + modelName + ": " + err.Error())
					return
				}
				// Read through the streaming response to completion
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					sendMsg(fmt.Sprintf("ERROR: Pull %s returned status %d", modelName, resp.StatusCode))
					return
				}
				sendMsg("OK: " + modelName + " pulled")
				log.Printf("  - ✓ %s pulled", modelName)
			}
		}
	}

	// Provider-choice setup also enters through this streamed endpoint. Probe
	// after any Ollama pull, then write the same Pi-native files as POST
	// /api/configuration. This prevents a failed provider choice from replacing
	// a previously working global Pi configuration. Authentication failures use
	// a distinct stream marker: treating them as generic ERROR lines prevents
	// the browser from opening the managed Ollama Cloud sign-in flow.
	if piDefaultModel(cfg) != "" {
		test := runTestModel(cfg)
		if !test.OK {
			sendMsg(configurePiTestFailureLine(test))
			return
		}
		if err := writePiGlobalConfig(cfg); err != nil {
			sendMsg("ERROR: Failed to write Pi configuration: " + err.Error())
			return
		}
		sendMsg("OK: Pi global configuration written")
	} else {
		sendMsg("OK: Models ready; choose the Pi model in Configure")
	}

	sendMsg("DONE")
}

const configureAuthRequiredPrefix = "AUTH_REQUIRED: "

// configurePiTestFailureLine preserves the structured authentication outcome
// across /api/configure's text stream. The splash page can then pause, complete
// `ollama signin`, and rerun this same gate so Pi config is written only after
// the selected cloud model is genuinely usable.
func configurePiTestFailureLine(test testModelResult) string {
	msg := test.Error
	if msg == "" {
		msg = test.Warning
	}
	if msg == "" {
		msg = "connection test failed"
	}
	if test.AuthRequired {
		return configureAuthRequiredPrefix + msg
	}
	return "ERROR: Pi model test failed: " + msg
}

// generateProjectAssetsForApp writes the runtime assets consumed by Pi and
// other ACP agents without generating an OpenCode configuration. Provider and
// model configuration is global under ~/.pi/agent; project-local state is the
// standard .mcp.json plus managed instructions, contract, and checkers.
func generateProjectAssetsForApp(appName string) error {
	projectDir := filepath.Join(WorkbenchDir, appName)
	return generateProjectAssetsInDir(projectDir, buildLaunchMCPConfig())
}

const (
	trustableAgentsBegin = "<!-- TRUSTABLE-MANAGED-AGENTS-BEGIN -->"
	trustableAgentsEnd   = "<!-- TRUSTABLE-MANAGED-AGENTS-END -->"
)

func managedAppAgentsContent() string {
	// opencode.md keeps its historical filename for compatibility, but its
	// product guidance is agent-neutral and must reach Pi through standard files.
	body := strings.TrimSpace(appAgentsMd) + "\n\n" + strings.TrimSpace(opencodeMd)
	return trustableAgentsBegin + "\n" + body + "\n" + trustableAgentsEnd + "\n"
}

func mergeManagedAppAgents(existing string) string {
	managed := managedAppAgentsContent()
	start := strings.Index(existing, trustableAgentsBegin)
	end := strings.Index(existing, trustableAgentsEnd)
	if start >= 0 && end >= start {
		end += len(trustableAgentsEnd)
		rest := strings.TrimSpace(existing[end:])
		if rest == "" {
			return managed
		}
		if strings.HasPrefix(rest, "## App-local notes") {
			return managed + "\n" + rest + "\n"
		}
		return managed + "\n## App-local notes\n\n" + rest + "\n"
	}

	existing = strings.TrimSpace(existing)
	if existing == "" {
		return managed
	}
	return managed + "\n## App-local notes\n\n" + existing + "\n"
}

// writeManagedInstructionFile replaces only the marked Trustable section, so
// reruns can refresh policy without destroying an application's local notes.
func writeManagedInstructionFile(projectDir, filename string) error {
	path := filepath.Join(projectDir, filename)
	existingBytes, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	content := mergeManagedAppAgents(string(existingBytes))
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// writeManagedAppAgents writes the single instruction file. CLAUDE.md is no
// longer a generated duplicate: launch links it to AGENTS.md (see
// ensureAgentConfigLinks), so Claude-compatible agents read the same bytes and
// the two can never drift.
func writeManagedAppAgents(projectDir string) error {
	return writeManagedInstructionFile(projectDir, "AGENTS.md")
}

// generateProjectAssetsInDir writes only agent-neutral project assets. The MCP
// map uses Trustable's launcher representation and is split into a
// credential-free workbench config plus a private host config. WHY: every agent
// needs the same server names, but model-readable discovery must never carry
// service credentials or credential-bearing URIs.
func generateProjectAssetsInDir(projectDir string, mcp map[string]interface{}) error {
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return fmt.Errorf("failed to create project directory: %w", err)
	}
	canonicalProjectDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return fmt.Errorf("failed to resolve canonical project directory %s: %w", projectDir, err)
	}
	canonicalProjectDir, err = filepath.Abs(canonicalProjectDir)
	if err != nil {
		return fmt.Errorf("failed to make canonical project directory absolute: %w", err)
	}

	if mcp == nil {
		mcp = make(map[string]interface{})
	}
	// WHY: application .env files are owned by Trustable's user-facing
	// configuration flow. The agent-side MCP must not receive a writable
	// secret-store path that could mutate or synchronize those files.
	mcp["openserverless"] = map[string]interface{}{
		"type":    "local",
		"command": []string{"openserverless-mcp"},
	}
	runtimeConfigPath, err := trustablePiRuntimeManifestPath()
	if err != nil {
		return err
	}
	mcp["browser"] = browserMCPConfig(projectDir, runtimeConfigPath)
	// WHY: Agentic React exposes selection context, not deterministic source
	// validation. Keep a separate read-only React server in every workbench so
	// route/auth/type failures are reported before browser verification.
	mcp["react"] = map[string]interface{}{
		"type":    "local",
		"command": []string{"trustable-react-mcp"},
		"environment": map[string]string{
			"TRUSTABLE_RUNTIME_CONFIG": runtimeConfigPath,
		},
		"enabled": true,
		"timeout": 30_000,
	}
	if appUsesAgenticReact(projectDir) {
		mcp["agentireact"] = map[string]interface{}{
			"type": "remote",
			"url":  "http://localhost:5173/mcp",
		}
	}
	if _, err := writeManagedMCPConfigs(projectDir, mcp); err != nil {
		return fmt.Errorf("failed to write managed MCP configs: %w", err)
	}
	log.Printf("  - Written to %s", filepath.Join(projectDir, ".mcp.json"))

	if err := writeManagedAppAgents(projectDir); err != nil {
		return err
	}
	log.Printf("  - Written to %s", filepath.Join(projectDir, "AGENTS.md"))

	contractPath := filepath.Join(canonicalProjectDir, ".openserverless-contract.md")
	if err := os.WriteFile(contractPath, []byte(openserverlessContractMd), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", contractPath, err)
	}
	log.Printf("  - Written to %s", contractPath)

	checkerPath, err := ensureOpenServerlessCheckerInstalled()
	if err != nil {
		return err
	}
	log.Printf("  - OpenServerless checker available at %s", checkerPath)
	frontendCheckerPath, err := ensureFrontendCheckerInstalled()
	if err != nil {
		return err
	}
	log.Printf("  - Frontend checker available at %s", frontendCheckerPath)
	appCheckerPath, err := ensureAppCheckerInstalled()
	if err != nil {
		return err
	}
	log.Printf("  - Completion checker available at %s", appCheckerPath)
	return nil
}

func browserExternalOrigin() string {
	apiHost := developmentAPIHost()
	parsed, err := url.Parse(apiHost)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := "vite." + parsed.Hostname()
	if parsed.Port() != "" {
		host += ":" + parsed.Port()
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: host}).String()
}

func browserMCPConfig(projectDir string, runtimeConfigPaths ...string) map[string]interface{} {
	environment := map[string]string{
		"TRUSTABLE_BROWSER_ARTIFACT_DIR": filepath.Join(WorkspaceDir, ".trustable", "browser", filepath.Base(projectDir)),
	}
	if len(runtimeConfigPaths) > 0 && runtimeConfigPaths[0] != "" {
		environment["TRUSTABLE_RUNTIME_CONFIG"] = runtimeConfigPaths[0]
	}
	if origin := browserExternalOrigin(); origin != "" {
		environment["TRUSTABLE_BROWSER_EXTERNAL_ORIGIN"] = origin
	}
	return map[string]interface{}{
		"type":        "local",
		"command":     []string{"trustable-browser-mcp"},
		"environment": environment,
		"enabled":     true,
		"timeout":     30_000,
	}
}

var openServerlessCheckerInstallPathOverride string
var frontendCheckerInstallPathOverride string
var appCheckerInstallPathOverride string

func openServerlessCheckerInstallPath() (string, error) {
	if openServerlessCheckerInstallPathOverride != "" {
		return openServerlessCheckerInstallPathOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home dir for OpenServerless checker: %w", err)
	}
	return filepath.Join(home, ".local", "bin", "check_openserverless_actions.sh"), nil
}

func ensureOpenServerlessCheckerInstalled() (string, error) {
	checkerPath, err := openServerlessCheckerInstallPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(checkerPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create %s: %w", filepath.Dir(checkerPath), err)
	}
	content := []byte(openserverlessCheckerSh)
	if existing, err := os.ReadFile(checkerPath); err == nil && bytes.Equal(existing, content) {
		if err := os.Chmod(checkerPath, 0755); err != nil {
			return "", fmt.Errorf("failed to chmod %s: %w", checkerPath, err)
		}
		return checkerPath, nil
	}
	if err := os.WriteFile(checkerPath, content, 0755); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", checkerPath, err)
	}
	return checkerPath, nil
}

func localBinInstallPath(override, name string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home dir for %s: %w", name, err)
	}
	return filepath.Join(home, ".local", "bin", name), nil
}

func ensureEmbeddedExecutable(path, name, content string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("failed to create %s directory: %w", name, err)
	}
	data := []byte(content)
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		if err := os.Chmod(path, 0755); err != nil {
			return "", fmt.Errorf("failed to chmod %s: %w", path, err)
		}
		return path, nil
	}
	if err := os.WriteFile(path, data, 0755); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", path, err)
	}
	return path, nil
}

func ensureFrontendCheckerInstalled() (string, error) {
	path, err := localBinInstallPath(frontendCheckerInstallPathOverride, "check_trustable_frontend.sh")
	if err != nil {
		return "", err
	}
	return ensureEmbeddedExecutable(path, "Trustable frontend checker", trustableFrontendCheckerSh)
}

func ensureAppCheckerInstalled() (string, error) {
	path, err := localBinInstallPath(appCheckerInstallPathOverride, "check_trustable_app.sh")
	if err != nil {
		return "", err
	}
	return ensureEmbeddedExecutable(path, "Trustable app checker", trustableAppCheckerSh)
}

var (
	agenticReactImportPattern = regexp.MustCompile(`(?m)^[\t ]*import[\t\r\n ]+(?:AgenticReact|\{[^}]*\bAgenticReact\b[^}]*\})[\t\r\n ]+from[\t\r\n ]+["']@agentic-react/vite["'][\t ]*;?`)
	agenticReactCallPattern   = regexp.MustCompile(`\bAgenticReact[\t ]*\(`)
)

// stripJavaScriptComments removes line and block comments while preserving
// quoted source. WHY: the Agentic React opt-in is a runtime capability and a
// README/example left in vite.config must not silently add tools to Pi.
func stripJavaScriptComments(source string) string {
	const (
		jsNormal = iota
		jsSingleQuote
		jsDoubleQuote
		jsTemplateQuote
		jsLineComment
		jsBlockComment
	)
	state := jsNormal
	escaped := false
	var out strings.Builder
	out.Grow(len(source))
	for i := 0; i < len(source); i++ {
		ch := source[i]
		next := byte(0)
		if i+1 < len(source) {
			next = source[i+1]
		}

		switch state {
		case jsLineComment:
			if ch == '\n' {
				state = jsNormal
				out.WriteByte(ch)
			} else {
				out.WriteByte(' ')
			}
		case jsBlockComment:
			if ch == '*' && next == '/' {
				out.WriteString("  ")
				i++
				state = jsNormal
			} else if ch == '\n' {
				out.WriteByte(ch)
			} else {
				out.WriteByte(' ')
			}
		case jsTemplateQuote:
			// Template literals can span lines and contain text that looks like
			// a complete import. Mask their payload so examples stored in a
			// string cannot opt the app into a live MCP dependency.
			if ch == '\n' {
				out.WriteByte(ch)
			} else {
				out.WriteByte(' ')
			}
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '`' {
				state = jsNormal
			}
		case jsSingleQuote, jsDoubleQuote:
			out.WriteByte(ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if (state == jsSingleQuote && ch == '\'') ||
				(state == jsDoubleQuote && ch == '"') {
				state = jsNormal
			}
		default:
			switch {
			case ch == '/' && next == '/':
				out.WriteString("  ")
				i++
				state = jsLineComment
			case ch == '/' && next == '*':
				out.WriteString("  ")
				i++
				state = jsBlockComment
			default:
				out.WriteByte(ch)
				switch ch {
				case '\'':
					state = jsSingleQuote
				case '"':
					state = jsDoubleQuote
				case '`':
					state = jsTemplateQuote
				}
			}
		}
	}
	return out.String()
}

// stripJavaScriptStrings masks quoted values in already comment-free source.
// The import detector still sees module specifiers in the original cleaned
// source, while the invocation detector uses this view so "AgenticReact()" in
// an example string is not executable evidence.
func stripJavaScriptStrings(source string) string {
	const (
		jsCode = iota
		jsQuoted
	)
	state := jsCode
	quote := byte(0)
	escaped := false
	var out strings.Builder
	out.Grow(len(source))
	for i := 0; i < len(source); i++ {
		ch := source[i]
		if state == jsQuoted {
			if ch == '\n' {
				out.WriteByte(ch)
			} else {
				out.WriteByte(' ')
			}
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				state = jsCode
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			state = jsQuoted
			quote = ch
			out.WriteByte(' ')
		default:
			out.WriteByte(ch)
		}
	}
	return out.String()
}

// appUsesAgenticReact reports whether a supported Vite config imports the real
// plugin and invokes AgenticReact(). WHY: matching only a misspelled call made
// unrelated or comment-only configs receive an unreachable eager MCP server.
func appUsesAgenticReact(projectDir string) bool {
	for _, name := range []string{"vite.config.js", "vite.config.ts"} {
		data, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue
		}
		source := stripJavaScriptComments(string(data))
		if agenticReactImportPattern.MatchString(source) &&
			agenticReactCallPattern.MatchString(stripJavaScriptStrings(source)) {
			return true
		}
	}
	return false
}

// standardMCPServers translates Trustable's launcher map to the shared
// mcpServers format (see spec/4-launch.md):
//   - type "local" (command array + optional environment) -> stdio (command
//     string + args + env)
//   - type "remote" (url) -> http (url)
//
// Launcher-only fields (enabled, timeout) are dropped. Every translated server
// is eager so Pi reports real connection state at session start instead of
// discovering service failures only after the first model-issued tool call.
func standardMCPServers(mcp map[string]interface{}) map[string]interface{} {
	servers := make(map[string]interface{})
	for name, raw := range mcp {
		server, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		switch server["type"] {
		case "remote":
			url, _ := server["url"].(string)
			if url == "" {
				continue
			}
			servers[name] = map[string]interface{}{
				"type":      "http",
				"url":       url,
				"lifecycle": "eager",
			}
		default: // "local" (or unset) -> stdio
			// command may be []string (servers we generate) or []interface{}
			// (maps decoded from JSON-based service configuration).
			cmd := toStringSlice(server["command"])
			if len(cmd) == 0 {
				continue
			}
			entry := map[string]interface{}{
				"type":      "stdio",
				"command":   cmd[0],
				"args":      cmd[1:],
				"lifecycle": "eager",
			}
			if env := toStringMap(server["environment"]); len(env) > 0 {
				entry["env"] = env
			}
			servers[name] = entry
		}
	}
	return servers
}

var managedMCPConfigPathOverride string
var managedMCPLauncherInstallPathOverride string

func managedMCPConfigPath(projectDir string) (string, error) {
	if managedMCPConfigPathOverride != "" {
		return managedMCPConfigPathOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home for managed MCP config: %w", err)
	}
	return filepath.Join(
		home,
		".config",
		"trustable",
		"runtime",
		filepath.Base(projectDir),
		"mcp.json",
	), nil
}

func managedMCPLauncherInstallPath() (string, error) {
	return localBinInstallPath(
		managedMCPLauncherInstallPathOverride,
		"trustable-mcp-launch",
	)
}

const managedMCPLauncherJS = `#!/usr/bin/env node
"use strict";
const fs = require("node:fs");
const path = require("node:path");
const { spawn } = require("node:child_process");

function fail(message) {
  process.stderr.write("trustable-mcp-launch: " + message + "\n");
  process.exit(1);
}
function within(root, target) {
  const rel = path.relative(root, target);
  return rel === "" || (!rel.startsWith("..") && !path.isAbsolute(rel));
}

const serverName = process.argv[2] || "";
if (!/^[a-zA-Z0-9_-]+$/.test(serverName)) fail("invalid server name");
const manifestPath = process.env.TRUSTABLE_RUNTIME_CONFIG || "";
if (!path.isAbsolute(manifestPath)) fail("managed runtime is unavailable");

let manifest;
try {
  manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
} catch {
  fail("managed runtime is unavailable");
}
const cwd = fs.realpathSync(process.cwd());
const matches = Array.isArray(manifest.workbenches)
  ? manifest.workbenches.filter((item) => {
      if (!item || !path.isAbsolute(item.workspace)) return false;
      return within(fs.realpathSync(item.workspace), cwd);
    })
  : [];
if (matches.length !== 1) fail("runtime does not select this workbench");
const selected = matches[0];
if (!path.isAbsolute(selected.mcpConfig)) fail("private MCP config is unavailable");

let privatePath;
let config;
try {
  privatePath = fs.realpathSync(selected.mcpConfig);
  const info = fs.statSync(privatePath);
  if (!info.isFile() || (info.mode & 0o077) !== 0) fail("private MCP config is not protected");
  if (within(fs.realpathSync(selected.workspace), privatePath)) fail("private MCP config escaped its host boundary");
  config = JSON.parse(fs.readFileSync(privatePath, "utf8"));
} catch (error) {
  if (error && error.message && error.message.startsWith("private MCP")) fail(error.message);
  fail("private MCP config is unavailable");
}
const server = config && config.mcpServers && config.mcpServers[serverName];
if (!server || server.type !== "stdio" || typeof server.command !== "string") {
  fail("requested server is not a managed stdio server");
}
const args = Array.isArray(server.args) && server.args.every((item) => typeof item === "string")
  ? server.args
  : [];
const extraEnv = server.env && typeof server.env === "object" && !Array.isArray(server.env)
  ? Object.fromEntries(Object.entries(server.env).filter((entry) => typeof entry[1] === "string"))
  : {};
const child = spawn(server.command, args, {
  cwd,
  env: { ...process.env, ...extraEnv },
  stdio: "inherit",
});
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => child.kill(signal));
}
child.on("error", () => fail("managed MCP server failed to start"));
child.on("exit", (code, signal) => {
  if (signal) process.exit(signal === "SIGINT" ? 130 : 143);
  process.exit(code === null ? 1 : code);
});
`

func writeJSONAtomic(path string, value interface{}, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if mode == 0600 {
		if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
			return err
		}
	}
	temporaryPath := path + ".tmp"
	defer os.Remove(temporaryPath)
	if err := os.WriteFile(temporaryPath, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, mode); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func credentialBearingMCPServer(name string) bool {
	switch name {
	case "s3", "postgres", "redis", "milvus", "mongodb":
		return true
	default:
		return false
	}
}

// writeManagedMCPConfigs keeps the complete launch contract in one private
// host-owned file and publishes only credential-free launch descriptors in the
// workbench. Pi uses the launcher for secret-bearing stdio servers; ACP-native
// agents receive the private entries directly from TruACP.
func writeManagedMCPConfigs(projectDir string, mcp map[string]interface{}) (string, error) {
	fullServers := standardMCPServers(mcp)
	privatePath, err := managedMCPConfigPath(projectDir)
	if err != nil {
		return "", err
	}
	if err := writeJSONAtomic(
		privatePath,
		map[string]interface{}{"mcpServers": fullServers},
		0600,
	); err != nil {
		return "", fmt.Errorf("failed to write private MCP config: %w", err)
	}

	launcherPath, err := managedMCPLauncherInstallPath()
	if err != nil {
		return "", err
	}
	if _, err := ensureEmbeddedExecutable(
		launcherPath,
		"Trustable managed MCP launcher",
		managedMCPLauncherJS,
	); err != nil {
		return "", err
	}

	publicServers := make(map[string]interface{}, len(fullServers))
	for name, raw := range fullServers {
		if credentialBearingMCPServer(name) {
			publicServers[name] = map[string]interface{}{
				"type":      "stdio",
				"command":   "trustable-mcp-launch",
				"args":      []string{name},
				"lifecycle": "eager",
			}
			continue
		}
		publicServers[name] = raw
	}
	if err := writeJSONAtomic(
		filepath.Join(projectDir, ".mcp.json"),
		map[string]interface{}{"mcpServers": publicServers},
		0644,
	); err != nil {
		return "", fmt.Errorf("failed to write credential-free MCP config: %w", err)
	}
	return privatePath, nil
}

// writeClaudeMCPConfig remains as a test/source compatibility helper. The
// managed launch path uses writeManagedMCPConfigs so credentials never enter
// the workbench.
func writeClaudeMCPConfig(projectDir string, mcp map[string]interface{}) error {
	return writeJSONAtomic(
		filepath.Join(projectDir, ".mcp.json"),
		map[string]interface{}{"mcpServers": standardMCPServers(mcp)},
		0644,
	)
}

// toStringSlice coerces a []string or []interface{} (as produced by JSON
// unmarshaling) into a []string, dropping non-string elements.
func toStringSlice(v interface{}) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []interface{}:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

// toStringMap coerces a map[string]string or map[string]interface{} (as produced
// by JSON unmarshaling) into a map[string]string, keeping only string values.
func toStringMap(v interface{}) map[string]string {
	switch m := v.(type) {
	case map[string]string:
		return m
	case map[string]interface{}:
		out := make(map[string]string, len(m))
		for k, e := range m {
			if str, ok := e.(string); ok {
				out[k] = str
			}
		}
		return out
	}
	return nil
}

// handleTestModel handles GET /api/testmodel - tests the OpenAI API connection
// testModelResult is the outcome of a hello-prompt connectivity probe against
// the configured small model. Exactly one of OK / Warning / AuthRequired is
// true (Error is set when the request itself failed before we could classify
// the upstream response).
type testModelResult struct {
	OK           bool
	Warning      string
	AuthRequired bool
	Error        string
}

// runTestModel sends a "hello" prompt to Pi's configured default model using
// the resolved provider URL and cfg.APIKey. Pi has no small-model role, so the
// connectivity probe must exercise the exact model the coding session will use.
func runTestModel(cfg *trustableConfig) testModelResult {
	if cfg == nil {
		return testModelResult{Error: "configuration not loaded"}
	}
	if cfg.Pi == nil || strings.TrimSpace(cfg.Pi.Default) == "" {
		return testModelResult{Error: "pi.default not defined in trustable.json"}
	}
	model := cfg.Pi.Default

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	apiKey := strings.TrimSpace(cfg.APIKey)
	if baseURL == "" {
		return testModelResult{Error: "base_url not defined in trustable.json"}
	}
	if cfg.Provider == "ollama" {
		ollamaRoot, _ := resolveOllamaRoot(cfg)
		baseURL = strings.TrimRight(ollamaRoot, "/") + "/v1"
	}

	log.Printf("Testing model %s at %s...", model, baseURL)
	client := &http.Client{Timeout: 60 * time.Second}
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
		},
		"stream": false,
	})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return testModelResult{Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Test model %s error: %s", model, err)
		return testModelResult{Error: err.Error()}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	log.Printf("Test model %s response: %s", model, bodyStr)

	if resp.StatusCode != http.StatusOK || strings.HasPrefix(strings.TrimSpace(bodyStr), `{"error"`) || !strings.Contains(bodyStr, `"choices"`) {
		message := ollamaErrorMessage(bodyStr)
		if cfg.Provider != "trustable" && isOllamaSigninRequired(message) {
			return testModelResult{AuthRequired: true, Warning: message}
		}
		return testModelResult{Warning: message}
	}

	return testModelResult{OK: true}
}

func handleTestModel(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	cfg, err := loadTrustableConfig()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	res := runTestModel(cfg)
	switch {
	case res.Error != "":
		json.NewEncoder(w).Encode(map[string]string{"error": res.Error})
	case res.AuthRequired:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         res.Warning,
			"auth_required": true,
		})
	case res.OK:
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	default:
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "warning",
			"warning": res.Warning,
		})
	}
}

func ollamaErrorMessage(bodyStr string) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(bodyStr), &payload); err == nil && payload.Error != "" {
		return payload.Error
	}
	return strings.TrimSpace(bodyStr)
}

func isOllamaSigninRequired(message string) bool {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "usage limit") || strings.Contains(lower, "quota") || strings.Contains(lower, "upgrade") {
		return false
	}
	for _, token := range []string{
		"not logged in",
		"not signed in",
		"sign in",
		"sign-in",
		"signin",
		"unauthorized",
		"authentication",
		"401",
		"internal service error",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// handleOllamaConnect returns the browser URL needed to connect the Ollama CLI
// identity used by Trustable to Ollama Cloud. The backend always invokes
// `ollama signin` and scrapes its output for the first https://ollama.com/connect
// URL — query strings from the calling page are not forwarded.
func handleOllamaConnect(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if connectURL := runOllamaSignin(); connectURL != "" {
		json.NewEncoder(w).Encode(map[string]string{"url": connectURL})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"error": "Could not obtain an Ollama Cloud sign-in URL — run `ollama signin` in a terminal.",
	})
}

// runOllamaSignin executes `ollama signin` and returns the first
// https://ollama.com/connect URL it finds in the output, or "" if none.
var ollamaConnectURLPattern = regexp.MustCompile(`https://ollama\.com/connect\S*`)

func runOllamaSignin() string {
	cmd := exec.Command("ollama", "signin")
	cmd.Env = ollamaSigninEnv()
	log.Printf("ollama signin: running %s with HOME=%s", strings.Join(cmd.Args, " "), envValue(cmd.Env, "HOME"))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	output := out.String()
	if err != nil {
		log.Printf("ollama signin: exited with error: %s; output:\n%s", err, output)
	} else {
		log.Printf("ollama signin: output:\n%s", output)
	}
	return ollamaConnectURLPattern.FindString(output)
}

func ollamaSigninEnv() []string {
	env := os.Environ()
	if home := strings.TrimSpace(WorkspaceDir); home != "" {
		env = append(env, "HOME="+home)
	}
	if endpoint := strings.TrimSpace(OllamaEndpoint); endpoint != "" {
		env = append(env, "OLLAMA_HOST="+endpoint)
	}
	return env
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}

// handleDiscoverModels proxies GET <base_url>/models against any
// OpenAI-compatible provider so the browser can list available models without
// CORS or mixed-content issues. Body shape: {"base_url": "...", "api_key": "..."}.
// When the saved provider is Ollama the host must be a routable LAN address —
// 127.0.0.1 / localhost are rejected because trustable-app runs inside a k3s VM
// and a loopback there is not the user's loopback.
func handleDiscoverModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON: " + err.Error()})
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "base_url is required"})
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "base_url must be a parseable URL with scheme and host"})
		return
	}

	// Ollama-mode loopback guard: Trustable runs in a VM and cannot reach the
	// host's loopback. Only enforce for Ollama (other providers may legitimately
	// route via localhost from within the VM, e.g. side-cars).
	cfg, cfgErr := loadTrustableConfig()
	if cfgErr == nil && cfg.Provider == "ollama" {
		host := parsed.Hostname()
		if host == "127.0.0.1" || strings.EqualFold(host, "localhost") {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "Use the LAN IP of your machine, not 127.0.0.1 / localhost — Trustable runs inside a VM and cannot reach your loopback.",
			})
			return
		}
	}

	target := baseURL + "/models"
	apiKey := strings.TrimSpace(req.APIKey)
	log.Printf("discover-models: GET %s", target)
	client := &http.Client{Timeout: 5 * time.Second}
	httpReq, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if apiKey != "" && apiKey != "dummy" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("discover-models: %s failed: %s", target, err)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cannot reach " + target + ": " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Printf("discover-models: %s returned %d: %s", target, resp.StatusCode, strings.TrimSpace(string(body)))
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("Provider at %s returned status %d", target, resp.StatusCode),
		})
		return
	}

	// OpenAI-compatible shape: {"object": "list", "data": [{"id": "...", ...}, ...]}
	var parsedBody struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsedBody); err != nil {
		log.Printf("discover-models: parse error: %s; body: %s", err, strings.TrimSpace(string(body)))
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid response: " + err.Error()})
		return
	}
	names := make([]string, 0, len(parsedBody.Data))
	for _, m := range parsedBody.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			names = append(names, id)
		}
	}
	sort.Strings(names)
	log.Printf("discover-models: %s returned %d models", target, len(names))
	json.NewEncoder(w).Encode(map[string]interface{}{"models": names})
}

// handleConfiguration handles GET and POST /api/configuration
func handleConfiguration(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		handleGetConfiguration(w, r)
	case http.MethodPost:
		handlePostConfiguration(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetConfiguration returns the merged configuration (base + workspace overrides)
func handleGetConfiguration(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadTrustableConfig()
	if err != nil {
		http.Error(w, "Failed to read configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := normalizeNotebookConfig(cfg); err != nil {
		http.Error(w, "Invalid notebook configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}
	payload, err := configurationPayload(cfg)
	if err != nil {
		http.Error(w, "Failed to prepare configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload)
}

// handlePostConfiguration saves the provided configuration to workspace trustable.json
func handlePostConfiguration(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var cfg trustableConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	var notebookInput struct {
		Notebook struct {
			GitHubToken string `json:"github_token"`
			ClearToken  bool   `json:"clear_token"`
		} `json:"notebook"`
	}
	if err := json.Unmarshal(body, &notebookInput); err != nil {
		http.Error(w, "Invalid notebook configuration: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := validateNotebookGitHubTokenUpdate(notebookInput.Notebook.GitHubToken); err != nil {
		http.Error(w, "Invalid notebook configuration: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Preserve existing apps and current from workspace config
	wsCfg, wsErr := loadWorkspaceConfig()
	if wsErr == nil {
		if wsCfg.Apps != nil && cfg.Apps == nil {
			cfg.Apps = wsCfg.Apps
		}
		if wsCfg.Current != "" && cfg.Current == "" {
			cfg.Current = wsCfg.Current
		}
		if wsCfg.Notebook != nil && cfg.Notebook == nil {
			cfg.Notebook = wsCfg.Notebook
		}
	}

	if err := normalizeNotebookConfig(&cfg); err != nil {
		http.Error(w, "Invalid notebook configuration: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := validatePiModelSelection(&cfg); err != nil {
		http.Error(w, "Invalid model selection: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := saveWorkspaceConfig(&cfg); err != nil {
		http.Error(w, "Failed to save configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := updateNotebookGitHubToken(
		notebookInput.Notebook.GitHubToken,
		notebookInput.Notebook.ClearToken,
	); err != nil {
		http.Error(w, "Failed to save notebook credentials: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Regenerate per-app .env files.
	regenerateAllAppEnvFiles()

	// Project assets are generated per app at launch. Reload the merged config
	// for the connectivity probe and Pi's global native configuration.
	merged, mergedErr := loadTrustableConfig()
	if mergedErr != nil {
		http.Error(w, "Failed to reload merged configuration: "+mergedErr.Error(), http.StatusInternalServerError)
		return
	}

	// Connectivity probe. Failures are reported as testmodel.ok=false (HTTP 200)
	// and do not overwrite a previously working Pi configuration.
	test := runTestModel(merged)
	if test.OK {
		if err := writePiGlobalConfig(merged); err != nil {
			http.Error(w, "Failed to write Pi configuration: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	result := map[string]interface{}{"ok": test.OK}
	if !test.OK {
		msg := test.Error
		if msg == "" {
			msg = test.Warning
		}
		if msg == "" {
			msg = "connection test failed"
		}
		result["error"] = msg
		if test.AuthRequired {
			result["auth_required"] = true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "saved",
		"testmodel": result,
	})
}

// EnvVar represents a single environment variable with dev and prod values
type EnvVar struct {
	Name      string `json:"name"`
	DevValue  string `json:"dev_value"`
	ProdValue string `json:"prod_value"`
	Readonly  bool   `json:"readonly,omitempty"`
	Fixed     bool   `json:"fixed,omitempty"`
}

// AppEnvConfig represents the full env configuration for an app
type AppEnvConfig struct {
	Vars     []EnvVar `json:"vars"`
	LocalEnv []string `json:"localenv_keys"`
}

// parseEnvFile parses a .env file into a map
func parseEnvFile(path string) map[string]string {
	result := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

// writeEnvFile writes a map of key-value pairs to a .env file
func writeEnvFile(path string, vars map[string]string, order []string) error {
	var lines []string
	written := make(map[string]bool)
	for _, k := range order {
		if v, ok := vars[k]; ok {
			lines = append(lines, fmt.Sprintf("%s=%s", k, v))
			written[k] = true
		}
	}
	for k, v := range vars {
		if !written[k] {
			lines = append(lines, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

func isServiceRuntimeEnvKey(name string) bool {
	return name == "MONGODB_URI"
}

// envDistFixedKeys are the variables the server supplies itself on every launch,
// from the workspace config rather than from anything the user provides. They are
// excluded from .env.dist entirely: listing them would state a requirement the
// user can neither satisfy nor is ever asked to, so they are neither generated
// into the manifest nor treated as required when one already lists them.
var envDistFixedKeys = []string{"OPS_USER", "OPS_PASSWORD", "OPS_APIHOST", "OPS_REPO", "OPS_SKILLS"}

func isEnvDistFixedKey(name string) bool {
	for _, k := range envDistFixedKeys {
		if k == name {
			return true
		}
	}
	return false
}

// appEnvVarNames returns the app's development and production variable names,
// sorted so the committed .env.dist has a stable diff regardless of Go's map
// iteration order. Server-supplied keys are omitted: .env.dist declares only what
// the user must actually provide.
func appEnvVarNames(appCfg *AppConfig) []string {
	seen := make(map[string]bool)
	var names []string
	for _, set := range []map[string]string{appCfg.Development, appCfg.Production} {
		for k := range set {
			if k == "" || seen[k] || isEnvDistFixedKey(k) || isServiceRuntimeEnvKey(k) {
				continue
			}
			seen[k] = true
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return names
}

// writeEnvDistFile writes the committed key manifest: every variable name with
// an empty value. Values are never copied — .env.dist is committed to git, so a
// secret leaking in here would be published. It reports whether the file changed
// so the caller can skip both the rewrite and the commit on a no-op launch.
func writeEnvDistFile(path string, names []string) (bool, error) {
	// An app whose variables are all server-supplied requires nothing of the
	// user, so it declares no contract. Committing a zero-byte manifest would
	// state one anyway; leave the file absent, and remove a stale one left by an
	// earlier config that did have custom variables.
	if len(names) == 0 {
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}

	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString("=\n")
	}
	content := b.String()

	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == content {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, err
	}
	return true, nil
}

// parseEnvDistNames returns the variable names declared by a .env.dist body, in
// file order. Only names are meaningful; any value present is ignored.
func parseEnvDistNames(content string) []string {
	var names []string
	seen := make(map[string]bool)
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:eq])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// readAppEnvDist returns the .env.dist declared by an app, preferring the
// workbench checkout and falling back to the workspace bare repo so an app that
// has never been launched can still be inspected. Missing file → nil, no error.
func readAppEnvDist(appName string) []string {
	workbenchPath := filepath.Join(WorkbenchDir, appName, ".env.dist")
	if data, err := os.ReadFile(workbenchPath); err == nil {
		return parseEnvDistNames(string(data))
	}

	workspacePath := filepath.Join(WorkspaceDir, "workspace", appName)
	if _, err := os.Stat(workspacePath); err != nil {
		return nil
	}
	cmd := exec.Command("git", "show", "HEAD:.env.dist")
	cmd.Dir = workspacePath
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseEnvDistNames(string(output))
}

// missingAppEnvKeys returns the .env.dist keys that have no development value in
// the merged config for the app. These are the variables a cloned repo declares
// but this installation has never been given — launch is blocked until they are
// filled in. Fixed OPS_* keys are excluded: the server supplies them itself.
func missingAppEnvKeys(appName string) ([]string, error) {
	names := readAppEnvDist(appName)
	if len(names) == 0 {
		return nil, nil
	}

	cfg, err := loadTrustableConfig()
	if err != nil {
		return nil, err
	}
	appCfg := cfg.Apps[appName]

	var missing []string
	for _, name := range names {
		if isEnvDistFixedKey(name) || isServiceRuntimeEnvKey(name) {
			continue
		}
		if appCfg != nil && strings.TrimSpace(appCfg.Development[name]) != "" {
			continue
		}
		missing = append(missing, name)
	}
	return missing, nil
}

// seedMissingEnvKeys inserts every .env.dist key the app does not yet configure
// into apps.<name>.development with an empty value. WHY empty entries rather
// than nothing: the env editor renders one row per config entry, so seeding is
// what makes an undeclared-but-required variable appear as an editable, visibly
// blank row instead of being invisible. Returns the keys it added.
func seedMissingEnvKeys(appName string) ([]string, error) {
	names := readAppEnvDist(appName)
	if len(names) == 0 {
		return nil, nil
	}

	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		return nil, err
	}
	if wsCfg.Apps == nil {
		wsCfg.Apps = make(map[string]*AppConfig)
	}
	if wsCfg.Apps[appName] == nil {
		wsCfg.Apps[appName] = &AppConfig{}
	}
	appCfg := wsCfg.Apps[appName]
	if appCfg.Development == nil {
		appCfg.Development = make(map[string]string)
	}

	var seeded []string
	for _, name := range names {
		if isEnvDistFixedKey(name) || isServiceRuntimeEnvKey(name) {
			continue
		}
		if _, ok := appCfg.Development[name]; ok {
			continue
		}
		appCfg.Development[name] = ""
		seeded = append(seeded, name)
	}
	if len(seeded) == 0 {
		return nil, nil
	}
	if err := saveWorkspaceConfig(wsCfg); err != nil {
		return nil, err
	}
	return seeded, nil
}

// generateAppEnvFiles writes .env and .env.production for an app in its workbench directory
func generateAppEnvFiles(appName string) error {
	cfg, err := loadTrustableConfig()
	if err != nil {
		return err
	}

	workbenchPath := filepath.Join(WorkbenchDir, appName)

	// An app can be configured before it has ever been launched: a fresh clone
	// exists in the workspace with no workbench checkout, and the missing-variable
	// flow sends the user to the env editor right then. There is nowhere to write
	// yet, and nothing is lost — launch regenerates all three files immediately
	// after cloning workspace → workbench. Treat it as a no-op rather than an
	// error, so saving config for a not-yet-launched app stays clean.
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		return nil
	}

	appCfg := cfg.Apps[appName]
	if appCfg == nil {
		appCfg = &AppConfig{
			Development: make(map[string]string),
			Production:  make(map[string]string),
		}
	}

	// Build development env
	devVars := make(map[string]string)
	devVars["OPS_USER"] = appName
	devVars["OPS_PASSWORD"] = appCfg.Password
	devVars["OPS_APIHOST"] = developmentAPIHost()
	devVars["OPS_REPO"] = getAppRepo(appName)
	devVars["OPS_SKILLS"] = OpsSkills

	// Per-app development overrides (no global env section). Service runtime
	// credentials from ops ide login are intentionally not written here: they are
	// injected only into the OpenCode/ops process environment at launch time.
	for k, v := range appCfg.Development {
		if isServiceRuntimeEnvKey(k) {
			continue
		}
		devVars[k] = v
	}

	// Build production env
	prodVars := make(map[string]string)
	for k, v := range appCfg.Production {
		if isServiceRuntimeEnvKey(k) {
			continue
		}
		prodVars[k] = v
	}

	order := []string{"OPS_USER", "OPS_PASSWORD", "OPS_APIHOST", "OPS_REPO", "OPS_SKILLS"}

	envPath := filepath.Join(workbenchPath, ".env")
	if err := writeEnvFile(envPath, devVars, order); err != nil {
		return fmt.Errorf("failed to write .env: %w", err)
	}

	if len(prodVars) > 0 {
		prodPath := filepath.Join(workbenchPath, ".env.production")
		if err := writeEnvFile(prodPath, prodVars, order); err != nil {
			return fmt.Errorf("failed to write .env.production: %w", err)
		}
	}

	// .env.dist is the committed counterpart of .env: the variable names a clone
	// needs, never their values. It is written from the same union so it can
	// never drift from what the app actually reads.
	distPath := filepath.Join(workbenchPath, ".env.dist")
	distChanged, err := writeEnvDistFile(distPath, appEnvVarNames(appCfg))
	if err != nil {
		return fmt.Errorf("failed to write .env.dist: %w", err)
	}
	if distChanged {
		commitEnvDist(workbenchPath)
	}

	return nil
}

// regenerateAllAppEnvFiles regenerates .env files for all apps that have a workbench
func regenerateAllAppEnvFiles() {
	cfg, err := loadTrustableConfig()
	if err != nil {
		log.Printf("Warning: failed to load config for env regeneration: %s", err)
		return
	}
	for appName := range cfg.Apps {
		workbenchPath := filepath.Join(WorkbenchDir, appName)
		if _, err := os.Stat(workbenchPath); err == nil {
			if err := generateAppEnvFiles(appName); err != nil {
				log.Printf("Warning: failed to regenerate .env for %s: %s", appName, err)
			}
		}
	}
}

// handleAppConfig handles GET and POST /api/appconfig/<name>
func handleAppConfig(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/appconfig/")
	name = strings.TrimPrefix(name, "/api/appconfig")
	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}

	workspacePath := filepath.Join(WorkspaceDir, "workspace", name)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleGetAppConfig(w, r, name, workspacePath)
	case http.MethodPost:
		handlePostAppConfig(w, r, name, workspacePath)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleGetAppConfig(w http.ResponseWriter, r *http.Request, name, workspacePath string) {
	cfg, err := loadTrustableConfig()
	if err != nil {
		http.Error(w, "Failed to load config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	appCfg := cfg.Apps[name]
	if appCfg == nil {
		appCfg = &AppConfig{
			Development: make(map[string]string),
			Production:  make(map[string]string),
		}
	}
	if appCfg.Development == nil {
		appCfg.Development = make(map[string]string)
	}
	if appCfg.Production == nil {
		appCfg.Production = make(map[string]string)
	}

	fixedKeys := []string{"OPS_APIHOST", "OPS_USER", "OPS_PASSWORD", "OPS_REPO", "OPS_SKILLS"}
	var vars []EnvVar

	// Fixed rows (readonly)
	vars = append(vars, EnvVar{Name: "OPS_USER", DevValue: name, ProdValue: appCfg.Production["OPS_USER"], Readonly: true})
	vars = append(vars, EnvVar{Name: "OPS_PASSWORD", DevValue: appCfg.Password, ProdValue: appCfg.Production["OPS_PASSWORD"], Readonly: true})
	vars = append(vars, EnvVar{Name: "OPS_APIHOST", DevValue: developmentAPIHost(), ProdValue: appCfg.Production["OPS_APIHOST"], Readonly: true})
	vars = append(vars, EnvVar{Name: "OPS_REPO", DevValue: getAppRepo(name), ProdValue: appCfg.Production["OPS_REPO"], Readonly: true})
	vars = append(vars, EnvVar{Name: "OPS_SKILLS", DevValue: OpsSkills, ProdValue: appCfg.Production["OPS_SKILLS"], Readonly: true})

	// Custom vars (per-app development/production only — there is no global env section)
	seen := make(map[string]bool)
	for _, k := range fixedKeys {
		seen[k] = true
	}

	for k := range appCfg.Development {
		if !seen[k] && !isServiceRuntimeEnvKey(k) {
			vars = append(vars, EnvVar{
				Name:      k,
				DevValue:  appCfg.Development[k],
				ProdValue: appCfg.Production[k],
			})
			seen[k] = true
		}
	}
	for k := range appCfg.Production {
		if !seen[k] && !isServiceRuntimeEnvKey(k) {
			vars = append(vars, EnvVar{
				Name:      k,
				DevValue:  appCfg.Development[k],
				ProdValue: appCfg.Production[k],
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AppEnvConfig{
		Vars:     vars,
		LocalEnv: nil,
	})
}

func handlePostAppConfig(w http.ResponseWriter, r *http.Request, name, workspacePath string) {
	var req struct {
		Vars []EnvVar `json:"vars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		http.Error(w, "Failed to load config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if wsCfg.Apps == nil {
		wsCfg.Apps = make(map[string]*AppConfig)
	}
	if wsCfg.Apps[name] == nil {
		wsCfg.Apps[name] = &AppConfig{}
	}

	devVars := make(map[string]string)
	prodVars := make(map[string]string)
	for _, v := range req.Vars {
		if v.Name == "" {
			continue
		}
		if isServiceRuntimeEnvKey(v.Name) {
			continue
		}
		if v.DevValue != "" {
			devVars[v.Name] = v.DevValue
		} else if !isEnvDistFixedKey(v.Name) {
			// Keep the key with an empty value rather than dropping it. A required
			// variable seeded from .env.dist that the user has not filled in yet
			// must survive a partial save, otherwise it vanishes from the editor
			// and stops being reported as missing.
			devVars[v.Name] = ""
		}
		if v.ProdValue != "" {
			prodVars[v.Name] = v.ProdValue
		}
	}

	wsCfg.Apps[name].Development = devVars
	wsCfg.Apps[name].Production = prodVars

	if err := saveWorkspaceConfig(wsCfg); err != nil {
		http.Error(w, "Failed to save: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Regenerate .env files from config
	if err := generateAppEnvFiles(name); err != nil {
		log.Printf("Warning: failed to regenerate .env for %s: %s", name, err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}
