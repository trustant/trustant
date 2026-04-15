This file describes the configuration system and the configure.go module.

# Layered Configuration

Configuration uses a two-layer system based on `trustable.json`:

1. **Base config** (`trustable.json` in the app root directory) — immutable defaults shipped with the application
2. **Workspace config** (`<WorkspaceDir>/trustable.json`) — user overrides, created on first save or migration

Loading merges both layers: workspace fields override base fields. Maps (ollama, env) are merged key-by-key. The workspace file only needs to contain overrides and the `apps` section.

## trustable.json structure

```json
{
    "ollama": {
        "<model-name>": "<context-size>"
    },
    "testmodel": "<model to use for testing>",
    "opencode": {
        "default": "<default opencode model>",
        "small": "<small opencode model>"
    },
    "env": {
        "OPENAI_BASE_URL": "...",
        "OPENAI_API_KEY": "...",
        "OPENAI_CHAT_MODEL": "...",
        "OPENAI_EMBEDDING_MODEL": "..."
    },
    "apps": {
        "<app-name>": {
            "password": "<ops user password>",
            "development": {
                "OPS_USER": "<app-name>",
                "OPS_APIHOST": "<cluster-apihost>",
                "CUSTOM_VAR": "dev-value"
            },
            "production": {
                "OPS_APIHOST": "https://prod.example.com"
            }
        }
    },
    "current": "<currently launched app name or empty>"
}
```

The base config has no `apps` section. The `apps` section lives only in the workspace config.

All fields in the workspace config use `omitempty` — absent fields inherit from the base.

## Config loading functions

- `loadBaseConfig()` — reads app-root `trustable.json`
- `loadWorkspaceConfig()` — reads `<WorkspaceDir>/trustable.json` (returns empty if missing)
- `mergeConfigs(base, override)` — merges workspace overrides onto base
- `loadTrustableConfig()` — returns the merged result (base + workspace)
- `saveWorkspaceConfig(cfg)` — writes only the workspace file

## Per-app .env generation

`.env` and `.env.production` files in the workbench are **generated artifacts**, not sources of truth. They are regenerated from the merged config:

- When an app is launched (`GET /api/launch/<name>`)
- When app config is saved (`POST /api/appconfig/<name>`)
- When global config is saved (`POST /api/configuration`)

The function `generateAppEnvFiles(appName)` builds the workbench `.env` from:
1. Fixed vars: `OPS_USER=<appName>`, `OPS_PASSWORD=<from apps.password>`, `OPS_APIHOST=<cluster apihost from OPS_APIHOST/APIHOST/TRUSTABLE_DEFAULT_APIHOST, defaulting to the Nuvolaris mini apihost http://miniops.me>`
2. Global `env` defaults from the merged config
3. Per-app `development` overrides

Production `.env.production` is written from the per-app `production` values.

`regenerateAllAppEnvFiles()` iterates all apps and regenerates for each that has a workbench directory.

## Current app tracking

The `current` field in the workspace `trustable.json` stores the name of the currently launched app. It is set when an app is launched (`writeCurrentApp`) and cleared when the launch is stopped (`removeCurrentFile`). The `workbench/current` file is also maintained for backward compatibility.

## Password storage

Passwords are stored in `apps.<name>.password` in the workspace `trustable.json` (not in separate `.password` files). They are set when creating an app via `POST /api/repo` and read during .env generation. During migration, passwords are recovered via `ops util kubeget whiskuser/<name> .spec.password` (never from `.password` files).

# Configuration API: GET /api/configure

This endpoint streams progress to the client. The steps are:

## Step 1: Check Ollama connectivity (with retries)

Before anything else, verify that Ollama is reachable. Try up to 12 times (2 minutes total) with 10-second intervals between attempts. Each attempt is streamed to the client:

- On success: `OK: Ollama is running`
- On retry: `Attempt N/12: Cannot reach Ollama at <endpoint> - retrying in 10 seconds...`
- On final failure: `ERROR: Cannot connect to Ollama at <endpoint> after 2 minutes. Please check that Ollama is running and try again.`

If Ollama cannot be reached, stop here — do not proceed to pull models. The frontend shows a **Retry** button so the user can fix the issue and try again.

## Step 2: Pull models

Read the merged config (base + workspace), connect to the Ollama endpoint and pull all the models, returning in streaming mode messages "Pulling model XXX".

# Prepare opencode config

Using information from the merged config and the .env, create the opencode config in
`~/.config/opencode/opencode.json` following the structure:

```
{
  "$schema": "https://opencode.ai/config.json",
  "instructions": ["~/.config/opencode/opencode.md"],
  "enabled_providers": [
    "ollama",
    "vllm"
  ],
  "model": <OpencodeModel>,
  "small_model": <OpencodeSmallModel>,
  "lsp": {
    "typescript": {
      "command": ["typescript-language-server", "--stdio"],
      "extensions": [".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts", ".cjs", ".cts"]
    },
    "python": {
      "command": ["pylsp"],
      "extensions": [".py"]
    }
  },
  "provider": {
    "ollama": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "baseURL": <OpenAIBaseUrl>
        "apiKey": <OpenAIApiKey>
      },
      "models": {
         <models with capabilities>
      }
    },
    "vllm": {
      "name": "vLLM",
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "baseURL": <vllm.base_url or "http://localhost:8910/vllm/v1">
        "apiKey": <vllm.api_key or "dummy">
      },
      "models": {
        <vllm.served_model_name or vllm.model>: {
          "name": <model-name>,
          "tool_call": <vllm.tool_call, default true>,
          "reasoning": false,
          "temperature": true,
          "limit": {
            "context": <vllm.context or 6144>,
            "output": <vllm.output or 2048>
          },
          "options": {
            "maxTokens": <vllm.output or 2048>,
            "temperature": 0
          },
          "variants": {
            "fast": {
              "options": {
                "maxTokens": <vllm.output or 2048>,
                "temperature": 0
              }
            },
            "deep": {
              "options": {
                "maxTokens": <vllm.output or 2048>,
                "temperature": 0
              }
            }
          }
        }
      }
    }
  }
}
```

When vLLM is configured and `disable_heavy_tools` is unset or true, also emit:

```
"tools": {
  "read": true,
  "write": true,
  "edit": true,
  "patch": true,
  "bash": true,
  "grep": true,
  "glob": true,
  "list": true,
  "lsp": true,
  "todoread": true,
  "question": false,
  "task": false,
  "todowrite": false,
  "webfetch": false,
  "skill": false,
  "action-add-s3": false,
  "action-add-postgresql": false,
  "action-add-redis": false,
  "action-add-milvus": false,
  "action-add-secret": false,
  "action-new": false,
  "action-requirements": false,
  "action-invoke": false
},
"agent": {
  "build": {
    "prompt": "Use tools directly to inspect and modify files. For code-change requests, after locating the target file you must call edit, write, or patch; do not answer with a plan or describe edits you have not applied. If edit fails because oldString has multiple matches, do not repeat the same edit; re-read the file and retry with a larger unique oldString, or use write with the complete updated file. After applying the edit, give a short final answer and stop. Keep tool arguments as plain valid JSON strings without extra embedded quotes. Do not expose internal reasoning, channel markers, summaries, or handoff text.",
    "steps": 12,
    "tools": {
      "read": true,
      "write": true,
      "edit": true,
      "patch": true,
      "bash": true,
      "grep": true,
      "glob": true,
      "list": true,
      "lsp": true,
      "todoread": true,
      "question": false,
      "task": false,
      "todowrite": false,
      "webfetch": false,
      "skill": false,
      "action-add-s3": false,
      "action-add-postgresql": false,
      "action-add-redis": false,
      "action-add-milvus": false,
      "action-add-secret": false,
      "action-new": false,
      "action-requirements": false,
      "action-invoke": false
    },
    "permission": {
      "question": "deny",
      "task": "deny",
      "todowrite": "deny",
      "webfetch": "deny",
      "skill": "deny",
      "action-add-s3": "deny",
      "action-add-postgresql": "deny",
      "action-add-redis": "deny",
      "action-add-milvus": "deny",
      "action-add-secret": "deny",
      "action-new": "deny",
      "action-requirements": "deny",
      "action-invoke": "deny"
    }
  },
  "compaction": {
    "disable": true
  }
},
"compaction": {
  "auto": false,
  "prune": true,
  "reserved": 768
}
```

This vLLM profile intentionally keeps only direct file/code tools on the build
agent plus the LSP tool. The Gemma 4/vLLM combination can produce verbose handoff text, malformed
tool argument quoting, and poor compaction behavior when the full OpenCode tool
set is exposed in a small context window.

The TypeScript/JavaScript and Python LSP servers are installed in the Trustable
image and configured as local OpenCode LSP commands. OpenCode launches and
supervises these stdio language server processes when a matching file type is
used. Do not add `typescript-language-server --stdio` or `pylsp` as long-running
`supervisord` programs, because stdio language servers need to be attached to
their OpenCode client process.

For Trustable's default vLLM provider, route OpenCode through the local
compatibility proxy at `http://localhost:8910/vllm/v1` instead of calling
`http://vllm:8000/v1` directly. The proxy forwards to vLLM on the Docker network
and strips vLLM-specific `reasoning` fields plus stray `<channel|>` markers that
OpenCode 1.4.3 otherwise displays as user-visible text.

To get the capabilities of a model use the OllamaEndPoint, list the models then show their capabilities.

To get the context size for a model look in trustable.json -  <value>K mean  <value> * 1024.

Model name is the model id, split in "-" and ":", capitalized, with numbers with extensions in parenthesis

Example: qwen3-coder:480b-cloud => Quen3 Coder (48OB) Cloud

Template:

```
"<model>": {
  "name": <model-name>,
  "tool_call": >true if you find tools in capabilities
  "reasoning": true if you find thinking>
  "temperature": true,
  "limit": {
    "context": <context size for model>
    "output": 32768
  },
  "options": {
    "maxTokens": 8192
  },
  "variants": {
    "fast": {
      "options": { "maxTokens": 2048 }
    },
    "deep": {
      "options": { "maxTokens": 16000 }
    },
    "disabled_variant": {
      "disabled": true
    }
  }
}
```

After generating `opencode.json`, write the embedded `opencode.md` to `~/.config/opencode/opencode.md` (the instructions file referenced by absolute path in the config). Also copy the embedded `tools` folder to `~/.config/opencode/tools`, overwriting existing files.

# Manage configuration: GET /api/configuration

Returns the merged configuration (base + workspace overrides) as JSON.

# POST /api/configuration

Saves the configuration to the workspace `trustable.json`. Preserves the existing `apps` section if not included in the request. After saving, regenerates `.env` files for all apps that have a workbench directory.

Does not execute the configuration — you need to do a `GET /api/configure` for that.

# GET /api/testmodel

Tests the AI model connection using the `testmodel` from the merged config.

# Per-app configuration: GET /api/appconfig/<name>

Returns the environment variable configuration for a specific app, read from the merged config's `apps` section.

Response contains `EnvVar[]` with:
- **Readonly rows**: `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST` (fixed dev values from the current cluster, editable prod values)
- **Fixed rows**: keys from the global `env` section (fixed name, editable values)
- **Custom rows**: any additional per-app development/production variables

# POST /api/appconfig/<name>

Accepts `{ "vars": [{ "name": "...", "dev_value": "...", "prod_value": "..." }] }`.

Saves the development and production values to `apps.<name>.development` and `apps.<name>.production` in the workspace config, then regenerates the workbench `.env` and `.env.production` files.

# Configuration UI (configure.html)

Shows:
- a table of ollama models with context size, you can add and remove them
- opencode models: default and small

The `buildConfig()` function preserves `testmodel`, `env`, and `apps` fields when saving.

Read the configuration with `GET /api/configuration`, save with `POST /api/configuration`, then execute `GET /api/configure` showing the progress downloading models.

# App config UI (appconfig.html)

Shows a table with columns: VARIABLE, Development, Production, Actions.
- Fixed readonly rows for OPS_USER, OPS_PASSWORD, OPS_APIHOST
- Fixed editable rows for global env keys
- Custom variables that can be added/removed
