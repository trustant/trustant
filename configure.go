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

// opencodeConfig holds the opencode model settings
type opencodeConfig struct {
	Default string `json:"default"`
	Small   string `json:"small"`
}

// experimentalConfig holds disabled-by-default feature experiments controlled
// from the configuration UI.
type experimentalConfig struct {
	Headroom *headroomExperimentConfig `json:"headroom,omitempty"`
}

type headroomExperimentConfig struct {
	Enabled  bool   `json:"enabled"`
	Mode     string `json:"mode,omitempty"`
	Port     int    `json:"port,omitempty"`
	StateDir string `json:"state_dir,omitempty"`
}

// ModelLimits is the per-model hint block from /api/v2/status and what we
// persist in trustable.json under the active provider's `models` map.
// All three fields are optional (omitempty); zero values are dropped.
type ModelLimits struct {
	MaxToken    int      `json:"maxToken,omitempty"`
	MaxInput    int      `json:"maxInput,omitempty"`
	MaxOutput   int      `json:"maxOutput,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
	Recommended bool     `json:"recommended,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	Reason      string   `json:"reason,omitempty"`
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

// trustableConfig represents the structure of trustable.json
type trustableConfig struct {
	Provider string `json:"provider,omitempty"`
	// BaseURL and APIKey are the top-level provider credentials.
	// Ollama: BaseURL="http://localhost:11434/v1", APIKey="dummy".
	// Trustable: posted by the ai-proxy registration iframe.
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	// ModelVersions is keyed by provider name ("ollama", "trustable", ...) and
	// stores the last per-provider `modelsVersion` value seen from /api/v2/status.
	// On every splash boot and every applist load the frontend compares the
	// live value against this map; a mismatch (or differing default/small)
	// routes the user through configure.html?reselect=1.
	ModelVersions map[string]int          `json:"model_versions,omitempty"`
	Models        map[string]*ModelLimits `json:"models,omitempty"`
	Opencode      *opencodeConfig         `json:"opencode,omitempty"`
	Git           *GitConfig              `json:"git,omitempty"`
	Experimental  *experimentalConfig     `json:"experimental,omitempty"`
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

	if override.Opencode != nil {
		result.Opencode = override.Opencode
	}

	if override.Git != nil {
		result.Git = override.Git
	}

	if override.Experimental != nil {
		result.Experimental = mergeExperimentalConfig(base.Experimental, override.Experimental)
	}

	if override.Apps != nil {
		result.Apps = override.Apps
	}

	return &result
}

func mergeExperimentalConfig(base, override *experimentalConfig) *experimentalConfig {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	result := *base
	if override.Headroom != nil {
		result.Headroom = override.Headroom
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

func modelAllowedForOpenCode(provider, modelID string, limits *ModelLimits) (bool, string) {
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
			return false, "not suitable for OpenCode agent work"
		}
	}

	if match := modelParamSizePattern.FindStringSubmatch(name); len(match) >= 3 {
		params, err := strconv.ParseFloat(match[2], 64)
		if err == nil && params > 0 && params < 20 {
			return false, "model is below the recommended 20B minimum for OpenCode agent work"
		}
	}

	return true, ""
}

func validateOpenCodeModelSelection(cfg *trustableConfig) error {
	if cfg == nil || cfg.Opencode == nil {
		return nil
	}
	models := cfg.Models
	defaultModel := strings.TrimSpace(cfg.Opencode.Default)
	smallModel := strings.TrimSpace(cfg.Opencode.Small)

	// Provider choice flows for BestIA / own-host Ollama intentionally persist
	// an empty model set first; configure.html discovers models in the next step.
	if len(models) == 0 && defaultModel == "" && smallModel == "" {
		return nil
	}
	for label, selected := range map[string]string{
		"default": defaultModel,
		"small":   smallModel,
	} {
		if selected == "" {
			return fmt.Errorf("opencode.%s model must be selected", label)
		}
		limits, ok := models[selected]
		if !ok {
			return fmt.Errorf("opencode.%s model %q is not in the configured model list", label, selected)
		}
		if ok, reason := modelAllowedForOpenCode(cfg.Provider, selected, limits); !ok {
			return fmt.Errorf("opencode.%s model %q is not allowed: %s", label, selected, reason)
		}
	}
	return nil
}

// ollamaShowResponse is the response from /api/show
type ollamaShowResponse struct {
	Capabilities []string `json:"capabilities"`
}

// getModelCapabilities queries Ollama for a model's capabilities. The root
// argument is the Ollama HTTP root (e.g. "http://192.168.1.10:11434"); pass
// the empty string to use OllamaEndpoint (the embedded server).
func getModelCapabilities(modelName, root string) ([]string, error) {
	if root == "" {
		root = OllamaEndpoint
	}
	client := &http.Client{Timeout: 30 * time.Second}
	reqBody, _ := json.Marshal(map[string]string{"name": modelName})
	resp, err := client.Post(root+"/api/show", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("model %s: %s", modelName, strings.TrimSpace(string(body)))
	}

	var result ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Capabilities, nil
}

// containsCapability checks if a capability list contains a specific capability
func containsCapability(caps []string, cap string) bool {
	for _, c := range caps {
		if c == cap {
			return true
		}
	}
	return false
}

func providerBaseURL(providerConfig interface{}) string {
	config, ok := providerConfig.(map[string]interface{})
	if !ok {
		return ""
	}
	options, ok := config["options"].(map[string]interface{})
	if !ok {
		return ""
	}
	baseURL, _ := options["baseURL"].(string)
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func isManagedVLLMProvider(providerName string, providerConfig interface{}) bool {
	if providerName != "vllm" {
		return false
	}
	baseURL := providerBaseURL(providerConfig)
	return strings.Contains(baseURL, "localhost:8910/vllm") ||
		strings.Contains(baseURL, "127.0.0.1:8910/vllm") ||
		strings.Contains(baseURL, "http://vllm:8000")
}

func hasGeneratedOllamaModelVariant(providerConfig interface{}) bool {
	config, ok := providerConfig.(map[string]interface{})
	if !ok {
		return false
	}
	models, ok := config["models"].(map[string]interface{})
	if !ok {
		return false
	}
	for _, modelConfig := range models {
		model, ok := modelConfig.(map[string]interface{})
		if !ok {
			continue
		}
		variants, ok := model["variants"].(map[string]interface{})
		if !ok {
			continue
		}
		if _, generated := variants["disabled_variant"]; generated {
			return true
		}
	}
	return false
}

func isTrustableManagedOpenCodeProvider(providerName string, providerConfig interface{}) bool {
	baseURL := providerBaseURL(providerConfig)
	switch providerName {
	case "ollama":
		isLocalManagedURL := baseURL == strings.TrimRight(OpenAIBaseUrl, "/") ||
			baseURL == "http://ollama:11434/v1" ||
			baseURL == "http://localhost:11434/v1" ||
			baseURL == "http://127.0.0.1:11434/v1"
		return isLocalManagedURL && hasGeneratedOllamaModelVariant(providerConfig)
	case "trustable":
		// Always Trustable-managed: any prior `trustable` provider entry
		// must be regenerated when switching providers.
		return true
	case "vllm":
		return isManagedVLLMProvider(providerName, providerConfig)
	default:
		return false
	}
}

func dropGeneratedOpenCodeKey(key string) bool {
	switch key {
	case "enabled_providers", "tools", "agent", "compaction":
		return true
	default:
		return false
	}
}

func defaultOpenCodeLSPConfig() map[string]interface{} {
	return map[string]interface{}{
		"typescript": map[string]interface{}{
			"command":    []string{"typescript-language-server", "--stdio"},
			"extensions": []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts", ".cjs", ".cts"},
		},
		"python": map[string]interface{}{
			"command":    []string{"pylsp"},
			"extensions": []string{".py"},
		},
	}
}

func defaultOpenCodePermissionConfig() map[string]interface{} {
	return map[string]interface{}{
		"edit": map[string]string{
			"*":                       "allow",
			"packages/**/__main__.py": "deny",
			"packages/**/*.zip":       "deny",
		},
		"bash": map[string]string{
			"*":            "allow",
			"ops action":   "deny",
			"ops action *": "deny",
		},
	}
}

func defaultDisabledOpenCodeProviders() []string {
	return []string{
		"302ai",
		"abacus",
		"aihubmix",
		"alibaba",
		"alibaba-cn",
		"alibaba-coding-plan",
		"alibaba-coding-plan-cn",
		"amazon-bedrock",
		"anthropic",
		"azure",
		"azure-cognitive-services",
		"bailing",
		"baseten",
		"berget",
		"cerebras",
		"chutes",
		"clarifai",
		"cloudferro-sherlock",
		"cloudflare-ai-gateway",
		"cloudflare-workers-ai",
		"cohere",
		"cortecs",
		"deepinfra",
		"deepseek",
		"dinference",
		"drun",
		"evroc",
		"fastrouter",
		"firmware",
		"fireworks-ai",
		"friendli",
		"github-copilot",
		"github-models",
		"gitlab",
		"google",
		"google-vertex",
		"google-vertex-anthropic",
		"groq",
		"helicone",
		"hpc-ai",
		"huggingface",
		"iflowcn",
		"inception",
		"inference",
		"io-net",
		"jiekou",
		"kilo",
		"kimi-for-coding",
		"kuae-cloud-coding-plan",
		"llama",
		"llmgateway",
		"lmstudio",
		"lucidquery",
		"meganova",
		"minimax",
		"minimax-cn",
		"minimax-cn-coding-plan",
		"minimax-coding-plan",
		"mistral",
		"mixlayer",
		"moark",
		"modelscope",
		"moonshotai",
		"moonshotai-cn",
		"morph",
		"nano-gpt",
		"nebius",
		"nova",
		"novita-ai",
		"nvidia",
		"ollama",
		"ollama-cloud",
		"ollama2",
		"ollama_docker",
		"opencode",
		"opencode-go",
		"openai",
		"openrouter",
		"ovhcloud",
		"perplexity",
		"perplexity-agent",
		"poe",
		"privatemode-ai",
		"qihang-ai",
		"qiniu-ai",
		"requesty",
		"sap-ai-core",
		"scaleway",
		"siliconflow",
		"siliconflow-cn",
		"stackit",
		"stepfun",
		"submodel",
		"synthetic",
		"tencent-coding-plan",
		"the-grid-ai",
		"togetherai",
		"upstage",
		"v0",
		"venice",
		"vercel",
		"vivgrid",
		"vllm",
		"vultr",
		"wandb",
		"xiaomi",
		"xiaomi-token-plan-ams",
		"xiaomi-token-plan-cn",
		"xiaomi-token-plan-sgp",
		"xai",
		"zai",
		"zai-coding-plan",
		"zenmux",
		"zhipuai",
		"zhipuai-coding-plan",
	}
}

func mergeDisabledOpenCodeProviders(existing interface{}, defaults []string) []string {
	seen := make(map[string]bool, len(defaults))
	merged := make([]string, 0, len(defaults))
	appendProvider := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		merged = append(merged, value)
	}
	for _, provider := range defaults {
		appendProvider(provider)
	}
	if existingList, ok := existing.([]interface{}); ok {
		for _, value := range existingList {
			if provider, ok := value.(string); ok {
				appendProvider(provider)
			}
		}
	}
	return merged
}

func disabledProvidersForCustomConfig(disabled []string, providers map[string]interface{}) []string {
	if len(providers) == 0 {
		return disabled
	}
	filtered := make([]string, 0, len(disabled))
	for _, provider := range disabled {
		if _, custom := providers[provider]; custom {
			continue
		}
		filtered = append(filtered, provider)
	}
	return filtered
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

// handleConfigure handles GET /api/configure - pulls models and generates opencode config, streaming progress
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

	// Configure git user (provider-independent)
	if cfg.Git != nil {
		if cfg.Git.User != "" {
			if err := exec.Command("git", "config", "--global", "user.name", cfg.Git.User).Run(); err != nil {
				sendMsg("ERROR: failed to set git user.name: " + err.Error())
			} else {
				sendMsg("OK: git user.name set to " + cfg.Git.User)
			}
		}
		if cfg.Git.Email != "" {
			if err := exec.Command("git", "config", "--global", "user.email", cfg.Git.Email).Run(); err != nil {
				sendMsg("ERROR: failed to set git user.email: " + err.Error())
			} else {
				sendMsg("OK: git user.email set to " + cfg.Git.Email)
			}
		}
	}

	if cfg.Provider == "trustable" || cfg.Provider == "bestia" {
		if cfg.Provider == "bestia" {
			sendMsg("OK: Skipping Ollama setup (BestIA)")
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

	// The opencode.json is no longer generated here: it is a single,
	// self-contained file written into each app's workbench project folder at
	// launch time (see generateOpencodeConfigForApp / spec/4-launch.md). The
	// global configure flow only verifies connectivity and pulls models.
	sendMsg("OK: opencode.json is generated per-app at launch")

	sendMsg("DONE")
}

// buildModelProvider constructs the Trustable-managed OpenCode provider entry
// for the active provider: one model per cfg.Models key, baseURL/apiKey from
// the top-level cfg.BaseURL / cfg.APIKey. In Ollama mode capabilities
// (tool_call, reasoning) are discovered via Ollama's /api/show; in Trustable
// mode they default to {tool_call: true, reasoning: false} (the user can
// override later via the OpenCode UI).
func buildModelProvider(cfg *trustableConfig) map[string]interface{} {
	ollamaRoot, _ := resolveOllamaRoot(cfg)
	models := make(map[string]interface{})
	for modelID, limits := range cfg.Models {
		if ok, reason := modelAllowedForOpenCode(cfg.Provider, modelID, limits); !ok {
			log.Printf("  - Skipping OpenCode model %s: %s", modelID, reason)
			continue
		}
		ctx, out := 0, 0
		if limits != nil {
			if limits.MaxToken > 0 {
				ctx = limits.MaxToken
			} else if limits.MaxInput > 0 {
				ctx = limits.MaxInput
			}
			if limits.MaxOutput > 0 {
				out = limits.MaxOutput
			}
		}
		if ctx == 0 {
			ctx = 32768
		}
		if out == 0 {
			out = 32768
		}

		toolCall := false
		reasoning := false
		if cfg.Provider == "ollama" {
			if caps, err := getModelCapabilities(modelID, ollamaRoot); err == nil {
				toolCall = containsCapability(caps, "tools")
				reasoning = containsCapability(caps, "thinking")
			} else {
				log.Printf("  - Warning: could not fetch capabilities for %s: %s", modelID, err)
			}
		} else {
			toolCall = true
		}

		models[modelID] = map[string]interface{}{
			"name":        modelDisplayName(modelID),
			"tool_call":   toolCall,
			"reasoning":   reasoning,
			"temperature": true,
			"limit": map[string]interface{}{
				"context": ctx,
				"output":  out,
			},
			"options": map[string]interface{}{
				"maxTokens": 8192,
			},
			"variants": map[string]interface{}{
				"fast": map[string]interface{}{
					"options": map[string]interface{}{"maxTokens": 2048},
				},
				"deep": map[string]interface{}{
					"options": map[string]interface{}{"maxTokens": 16000},
				},
				"disabled_variant": map[string]interface{}{
					"disabled": true,
				},
			},
		}
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = "dummy"
	}

	return map[string]interface{}{
		"options": map[string]interface{}{
			"baseURL": baseURL,
			"apiKey":  apiKey,
		},
		"models": models,
	}
}

// generateOpencodeConfigForApp generates the complete OpenCode config for an app
// directly in its workbench project folder. Per spec/4-launch.md there is a
// single, self-contained <workbench>/<app>/opencode.json — there is no global
// ~/.config/opencode/opencode.json. It holds the provider, model defaults,
// disabled_providers, instructions, lsp, and the mcp servers built from
// ~/.ops/config.json. The OpenServerless contract and opencode.md are written
// alongside it in the project folder and referenced by absolute path where
// OpenCode expects instruction files. The checker is installed once in the
// user's local bin and invoked with the project path.
func generateOpencodeConfigForApp(cfg *trustableConfig, appName string) error {
	projectDir := filepath.Join(WorkbenchDir, appName)
	mcp := buildLaunchMCPConfig()
	return generateOpencodeConfigInDir(cfg, projectDir, mcp)
}

const (
	trustableAgentsBegin = "<!-- TRUSTABLE-MANAGED-AGENTS-BEGIN -->"
	trustableAgentsEnd   = "<!-- TRUSTABLE-MANAGED-AGENTS-END -->"
)

func managedAppAgentsContent() string {
	return trustableAgentsBegin + "\n" + strings.TrimSpace(appAgentsMd) + "\n" + trustableAgentsEnd + "\n"
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

func writeManagedAppAgents(projectDir string) error {
	agentsPath := filepath.Join(projectDir, "AGENTS.md")
	existingBytes, err := os.ReadFile(agentsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read %s: %w", agentsPath, err)
	}
	content := mergeManagedAppAgents(string(existingBytes))
	if err := os.WriteFile(agentsPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", agentsPath, err)
	}
	return nil
}

// generateOpencodeConfigInDir writes the full opencode.json plus generated
// OpenServerless guidance into projectDir. The file is fully regenerated on
// every launch; custom provider/lsp/mcp entries from a previous opencode.json
// are not preserved. The action tools are provided by the openserverless MCP
// server wired into the mcp section, not copied as plugins.
func generateOpencodeConfigInDir(cfg *trustableConfig, projectDir string, mcp map[string]interface{}) error {
	providers := make(map[string]interface{})

	// The OpenCode provider key tracks the active trustable provider:
	// "ollama", "trustable", or "bestia". Default to "ollama" if unset.
	providerKey := cfg.Provider
	if providerKey != "ollama" && providerKey != "trustable" && providerKey != "bestia" {
		providerKey = "ollama"
	}

	// Always generate the Trustable-managed provider entry.
	providers[providerKey] = buildModelProvider(cfg)

	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return fmt.Errorf("failed to create project directory: %w", err)
	}

	contractPath := filepath.Join(projectDir, ".openserverless-contract.md")
	mdPath := filepath.Join(projectDir, "opencode.md")
	guardrailPluginPath, err := ensureOpenCodeGuardrailPluginInstalled()
	if err != nil {
		return err
	}

	config := map[string]interface{}{
		"$schema":            "https://opencode.ai/config.json",
		"disabled_providers": defaultDisabledOpenCodeProviders(),
		"instructions":       []string{contractPath, mdPath},
		"provider":           providers,
		"lsp":                defaultOpenCodeLSPConfig(),
		"permission":         defaultOpenCodePermissionConfig(),
	}
	// The mcp section is built from ~/.ops/config.json (nil when no service
	// blocks are configured); custom mcp entries from an existing opencode.json
	// are preserved by the merge below. The openserverless MCP server is always
	// added: it replaces the old embedded tools/ plugins, exposing the
	// OpenServerless action tools (action_new/invoke/requirements + connectors)
	// over MCP.
	if mcp == nil {
		mcp = make(map[string]interface{})
	}
	mcp["openserverless"] = map[string]interface{}{
		"type":    "local",
		"command": []string{"openserverless-mcp"},
		"enabled": true,
	}
	mcp["browser"] = browserMCPConfig(projectDir)
	// When the app's Vite config uses AgentiReact(), the running dev server
	// (opsdevel on :5173) exposes an MCP endpoint over HTTP; wire it in as a
	// remote server (see spec/4-launch.md).
	if appUsesAgentiReact(projectDir) {
		mcp["agentireact"] = map[string]interface{}{
			"type":    "remote",
			"url":     "http://localhost:5173/mcp",
			"enabled": true,
		}
	}
	config["mcp"] = mcp

	// Always set top-level model/small_model from trustable.json opencode config.
	if cfg.Opencode != nil {
		if cfg.Opencode.Default != "" {
			config["model"] = providerKey + "/" + cfg.Opencode.Default
		}
		if cfg.Opencode.Small != "" {
			config["small_model"] = providerKey + "/" + cfg.Opencode.Small
		}
	}

	// opencode.json and its mcp servers are fully regenerated from trustable.json
	// and ~/.ops/config.json on every launch — no merge with any existing file.
	// This guarantees the managed providers/lsp/mcp (e.g. the postgres MCP's
	// DATABASE_URI) always reflect the current config and never carry forward a
	// stale value. Any hand-edits to the project opencode.json are discarded.
	configPath := filepath.Join(projectDir, "opencode.json")
	if disabledProviders, ok := config["disabled_providers"].([]string); ok {
		config["disabled_providers"] = disabledProvidersForCustomConfig(disabledProviders, providers)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", configPath, err)
	}

	log.Printf("  - Written to %s", configPath)

	// Write the app-local AGENTS.md guard, short OpenServerless contract, and
	// longer opencode.md instructions alongside the config in the project dir.
	if err := writeManagedAppAgents(projectDir); err != nil {
		return err
	}
	log.Printf("  - Written to %s", filepath.Join(projectDir, "AGENTS.md"))

	if err := os.WriteFile(contractPath, []byte(openserverlessContractMd), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", contractPath, err)
	}
	log.Printf("  - Written to %s", contractPath)

	if err := os.WriteFile(mdPath, []byte(opencodeMd), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", mdPath, err)
	}
	log.Printf("  - Written to %s", mdPath)

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
	log.Printf("  - OpenCode Trustable guardrail plugin available at %s", guardrailPluginPath)

	// The action tools are no longer copied into the project dir as embedded
	// @opencode-ai/plugin scripts; they are now provided by the openserverless
	// MCP server wired into the mcp section above.

	// Also emit a Claude-format .mcp.json carrying the same MCP servers, so
	// Claude-format clients see the same tools (see spec/4-launch.md).
	if finalMCP, ok := config["mcp"].(map[string]interface{}); ok {
		if err := writeClaudeMCPConfig(projectDir, finalMCP); err != nil {
			log.Printf("  - Warning: failed to write .mcp.json: %s", err)
		} else {
			log.Printf("  - Written to %s", filepath.Join(projectDir, ".mcp.json"))
		}
	}

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

func browserMCPConfig(projectDir string) map[string]interface{} {
	environment := map[string]string{
		"TRUSTABLE_BROWSER_ARTIFACT_DIR": filepath.Join(WorkspaceDir, ".trustable", "browser", filepath.Base(projectDir)),
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
var guardrailPluginInstallPathOverride string

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

func guardrailPluginInstallPath() (string, error) {
	if guardrailPluginInstallPathOverride != "" {
		return guardrailPluginInstallPathOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home dir for OpenCode guardrail plugin: %w", err)
	}
	return filepath.Join(home, ".config", "opencode", "plugins", "trustable-guardrails.js"), nil
}

func ensureOpenCodeGuardrailPluginInstalled() (string, error) {
	path, err := guardrailPluginInstallPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("failed to create OpenCode guardrail plugin directory: %w", err)
	}
	data := []byte(opencodeTrustableGuardrailsJS)
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return path, nil
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write OpenCode guardrail plugin: %w", err)
	}
	return path, nil
}

// appUsesAgentiReact reports whether the app's Vite config opts into AgentiReact.
// It checks vite.config.js and vite.config.ts in projectDir for a call to
// AgentiReact(); a missing or unreadable config means false.
func appUsesAgentiReact(projectDir string) bool {
	for _, name := range []string{"vite.config.js", "vite.config.ts"} {
		data, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "AgentiReact()") {
			return true
		}
	}
	return false
}

// writeClaudeMCPConfig writes <projectDir>/.mcp.json in Claude Code's mcpServers
// format, translated from the OpenCode mcp section (see spec/4-launch.md):
//   - type "local" (command array + optional environment) -> stdio (command
//     string + args + env)
//   - type "remote" (url) -> http (url)
//
// OpenCode-only fields (enabled, timeout) are dropped.
func writeClaudeMCPConfig(projectDir string, mcp map[string]interface{}) error {
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
			servers[name] = map[string]interface{}{"type": "http", "url": url}
		default: // "local" (or unset) -> stdio
			// command may be []string (servers we generate) or []interface{}
			// (custom servers carried over from a parsed opencode.json).
			cmd := toStringSlice(server["command"])
			if len(cmd) == 0 {
				continue
			}
			entry := map[string]interface{}{
				"type":    "stdio",
				"command": cmd[0],
				"args":    cmd[1:],
			}
			if env := toStringMap(server["environment"]); len(env) > 0 {
				entry["env"] = env
			}
			servers[name] = entry
		}
	}

	data, err := json.MarshalIndent(map[string]interface{}{"mcpServers": servers}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal .mcp.json: %w", err)
	}
	return os.WriteFile(filepath.Join(projectDir, ".mcp.json"), data, 0644)
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

// runTestModel sends a "hello" prompt to cfg.Opencode.Small using the resolved
// provider URL and cfg.APIKey, then classifies the response. Shared by
// GET /api/testmodel and the testmodel step of POST /api/configuration.
func runTestModel(cfg *trustableConfig) testModelResult {
	if cfg == nil {
		return testModelResult{Error: "configuration not loaded"}
	}
	if cfg.Opencode == nil || strings.TrimSpace(cfg.Opencode.Small) == "" {
		return testModelResult{Error: "opencode.small not defined in trustable.json"}
	}
	model := cfg.Opencode.Small

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

// handleBestiaCheck handles GET /api/bestia-check. It probes the fixed BestIA
// inference host (http://bestia:11434) server-side — the browser cannot reach
// it because the GPU box is only routable from inside the VM. Reachability,
// not authorization, is what we test: any HTTP response (even 401/403, since
// we have no api_key yet) means a BestIA is running; only a connection /
// timeout error means the user is not running a BestIA.
func handleBestiaCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	const target = "http://bestia:11434/v1/models"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(target)
	if err != nil {
		log.Printf("bestia-check: %s unreachable: %s", target, err)
		json.NewEncoder(w).Encode(map[string]interface{}{"available": false})
		return
	}
	resp.Body.Close()
	log.Printf("bestia-check: %s answered %d", target, resp.StatusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{"available": true})
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
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

	// Preserve existing apps and current from workspace config
	wsCfg, wsErr := loadWorkspaceConfig()
	if wsErr == nil {
		if wsCfg.Apps != nil && cfg.Apps == nil {
			cfg.Apps = wsCfg.Apps
		}
		if wsCfg.Current != "" && cfg.Current == "" {
			cfg.Current = wsCfg.Current
		}
		if wsCfg.Experimental != nil && cfg.Experimental == nil {
			cfg.Experimental = wsCfg.Experimental
		}
	}

	if err := validateOpenCodeModelSelection(&cfg); err != nil {
		http.Error(w, "Invalid model selection: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := saveWorkspaceConfig(&cfg); err != nil {
		http.Error(w, "Failed to save configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Regenerate per-app .env files.
	regenerateAllAppEnvFiles()

	// opencode.json is generated per-app in the workbench project folder at
	// launch time (see generateOpencodeConfigForApp / spec/4-launch.md), so a
	// global config save does not write it here — the new model defaults take
	// effect on the next launch. We still reload the merged config for the
	// connectivity probe below.
	merged, mergedErr := loadTrustableConfig()
	if mergedErr != nil {
		http.Error(w, "Failed to reload merged configuration: "+mergedErr.Error(), http.StatusInternalServerError)
		return
	}

	// Connectivity probe with opencode.small. Failures here are reported
	// as testmodel.ok=false (HTTP 200) — the save itself succeeded.
	test := runTestModel(merged)
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

// generateAppEnvFiles writes .env and .env.production for an app in its workbench directory
func generateAppEnvFiles(appName string) error {
	cfg, err := loadTrustableConfig()
	if err != nil {
		return err
	}

	workbenchPath := filepath.Join(WorkbenchDir, appName)

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
