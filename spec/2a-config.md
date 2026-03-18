Thisi file describes the configuration

# Configuration API: GET /configure

When invoked, read the <WorkspaceDir>/trustable.json and the .env,

connect to the endppoint and pull all the models,

returning in streaming mode messages "Pulling model XXX"


# prepare opencode config

- Using information in <WorkspaceDir>/trustable.json and in the .env create the opencode config in

`~/.config/opencode/opencode.json` following the structure:

```
{
  "$schema": "https://opencode.ai/config.json",
  "instructions": ["opencode.md"],
  "enabled_providers": [
    "ollama"
  ],
  "model": <OpencodeModel>,
  "small_model": <OpencodeSmallModel>,
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
    }
  }
}
```

To get the capabilites of a model use the OllamaEndPoint, list the models then show their capabilities.

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

# Manage configuration: GET /configuration
returns the current <WorkspaceDir>/trustable.json from the workspace

# POST /configuration
save the configuration - it does not execute the configuration
you need to do a GET /configuure to to that

# configuration UI

create a configure.html to configure

It shows
- a table of ollama models with context size,
you can add and remove them

- opencode models: default and small
also plan, build, explore

read the configuration with GET /configuration
save with POST /configuration then execute the GET /configure
showing the progresses downloading models



