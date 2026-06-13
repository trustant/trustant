package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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

// ModelLimits is the per-model hint block from /api/v2/status and what we
// persist in trustable.json under the active provider's `models` map.
// All three fields are optional (omitempty); zero values are dropped.
type ModelLimits struct {
	MaxToken  int `json:"maxToken,omitempty"`
	MaxInput  int `json:"maxInput,omitempty"`
	MaxOutput int `json:"maxOutput,omitempty"`
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
		if *av != *bv {
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
// ~/.ops/config.json. opencode.md and the tools/ folder are written alongside it
// in the project folder and referenced by project-relative path.
func generateOpencodeConfigForApp(cfg *trustableConfig, appName string) error {
	projectDir := filepath.Join(WorkbenchDir, appName)
	mcp := buildLaunchMCPConfig()
	return generateOpencodeConfigInDir(cfg, projectDir, mcp)
}

// generateOpencodeConfigInDir writes the full opencode.json (plus opencode.md and
// the embedded tools/ folder) into projectDir, merging against any existing
// opencode.json there to preserve custom providers/lsp/mcp entries.
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

	config := map[string]interface{}{
		"$schema":            "https://opencode.ai/config.json",
		"disabled_providers": defaultDisabledOpenCodeProviders(),
		"instructions":       []string{filepath.Join(projectDir, "opencode.md")},
		"provider":           providers,
		"lsp":                defaultOpenCodeLSPConfig(),
	}
	// The mcp section is built from ~/.ops/config.json (nil when no service
	// blocks are configured); custom mcp entries from an existing opencode.json
	// are preserved by the merge below.
	if mcp != nil {
		config["mcp"] = mcp
	}

	// Always set top-level model/small_model from trustable.json opencode config.
	if cfg.Opencode != nil {
		if cfg.Opencode.Default != "" {
			config["model"] = providerKey + "/" + cfg.Opencode.Default
		}
		if cfg.Opencode.Small != "" {
			config["small_model"] = providerKey + "/" + cfg.Opencode.Small
		}
	}

	configPath := filepath.Join(projectDir, "opencode.json")
	if existingData, readErr := os.ReadFile(configPath); readErr == nil {
		var existing map[string]interface{}
		if err := json.Unmarshal(existingData, &existing); err != nil {
			log.Printf("  - Warning: could not parse existing opencode.json for provider merge: %s", err)
		} else {
			if existingProviders, ok := existing["provider"].(map[string]interface{}); ok {
				for providerName, providerConfig := range existingProviders {
					if providerName == providerKey {
						continue // always regenerated above
					}
					if isTrustableManagedOpenCodeProvider(providerName, providerConfig) {
						log.Printf("  - Removed Trustable-managed OpenCode provider %s", providerName)
						continue
					}
					providers[providerName] = providerConfig
					log.Printf("  - Preserved custom OpenCode provider %s", providerName)
				}
			}
			if existingLSP, ok := existing["lsp"].(map[string]interface{}); ok {
				if generatedLSP, ok := config["lsp"].(map[string]interface{}); ok {
					for serverName, serverConfig := range existingLSP {
						if _, generated := generatedLSP[serverName]; !generated {
							generatedLSP[serverName] = serverConfig
							log.Printf("  - Preserved custom OpenCode LSP %s", serverName)
						}
					}
				}
			}
			if existingMCP, ok := existing["mcp"].(map[string]interface{}); ok {
				generatedMCP, _ := config["mcp"].(map[string]interface{})
				if generatedMCP == nil {
					generatedMCP = make(map[string]interface{})
				}
				for serverName, serverConfig := range existingMCP {
					// The Trustable-managed servers (s3/postgres/redis/milvus)
					// are regenerated above from ~/.ops/config.json; only carry
					// forward genuinely custom servers.
					if isTrustableManagedMCPServer(serverName) {
						continue
					}
					if _, generated := generatedMCP[serverName]; !generated {
						generatedMCP[serverName] = serverConfig
						log.Printf("  - Preserved custom OpenCode MCP server %s", serverName)
					}
				}
				if len(generatedMCP) > 0 {
					config["mcp"] = generatedMCP
				}
			}
			if existingDisabledProviders, ok := existing["disabled_providers"]; ok {
				config["disabled_providers"] = mergeDisabledOpenCodeProviders(existingDisabledProviders, defaultDisabledOpenCodeProviders())
			}
			for key, value := range existing {
				if _, generated := config[key]; generated {
					continue
				}
				if dropGeneratedOpenCodeKey(key) {
					log.Printf("  - Removed generated OpenCode %s config to restore the default flow", key)
					continue
				}
				config[key] = value
			}
			config["provider"] = providers
		}
	}
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

	// Write opencode.md instructions file alongside the config in the project dir
	mdPath := filepath.Join(projectDir, "opencode.md")
	if err := os.WriteFile(mdPath, []byte(opencodeMd), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", mdPath, err)
	}
	log.Printf("  - Written to %s", mdPath)

	// Copy the embedded tools/ folder into the project dir, overwriting.
	toolsDst := filepath.Join(projectDir, "tools")
	if err := os.MkdirAll(toolsDst, 0755); err != nil {
		log.Printf("  - Warning: failed to create tools directory: %s", err)
	} else if entries, err := fs.ReadDir(embeddedTools, "tools"); err != nil {
		log.Printf("  - Warning: failed to read embedded tools: %s", err)
	} else {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			toolData, err := fs.ReadFile(embeddedTools, "tools/"+entry.Name())
			if err != nil {
				log.Printf("  - Warning: failed to read embedded tool %s: %s", entry.Name(), err)
				continue
			}
			if err := os.WriteFile(filepath.Join(toolsDst, entry.Name()), toolData, 0644); err != nil {
				log.Printf("  - Warning: failed to write tool %s: %s", entry.Name(), err)
			}
		}
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

// runTestModel sends a "hello" prompt to cfg.Opencode.Small using cfg.BaseURL
// / cfg.APIKey and classifies the response. Shared by GET /api/testmodel and
// the testmodel step of POST /api/configuration.
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
	log.Printf("ollama signin: running %s", strings.Join(cmd.Args, " "))
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

	// Per-app development overrides (no global env section)
	for k, v := range appCfg.Development {
		devVars[k] = v
	}

	// Build production env
	prodVars := make(map[string]string)
	for k, v := range appCfg.Production {
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
		if !seen[k] {
			vars = append(vars, EnvVar{
				Name:      k,
				DevValue:  appCfg.Development[k],
				ProdValue: appCfg.Production[k],
			})
			seen[k] = true
		}
	}
	for k := range appCfg.Production {
		if !seen[k] {
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
