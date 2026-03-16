You are building a go applications implementing a Lovable like environment based on OpenServerless

This file describes the prefligth.go module with the checks to execute

# Setup Env
When starting, before anything else:

- Read the .env  in current dir and the provided execution environment then set variables expanding them (for nested varaibels) for:

WorkspaceDir
OpenAIBaseUrl
OpenAIApiKey
OllamaEndpoint
OpencodeModel
OpencodeSmallModel

# cleanup

- if there is a file pgid in WorspaceDir, read it and terminate the process group and remove the file

- use lsof -i and check for processes occopying in port 8910 4096 and 5173 and kill them

- check the health of OllamaEndPoint

# prepare the models

read the file model.lst, each line in format:
<model> <context-size>

invoke the ollama api to pull them

# prepare opencode config

- List the models in OllamaEndPoint and its capabilites and create the opencode endpoint in
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

To get the context size for a model look in model.lst -  <value>K mean  <value> * 1024.
Model name is the model id, split in "-" and ":", capitalized.

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

# generate an ssh key

if not found, generate an ssh key in format ED25519 in ~/.ssh/id_trustable and his public key ~/.ssh/id_trustable.pub

# ensure the trustable domain in the url

require you always access the application with a full fqdn like trustable.<domain>; if you detect a plain hostname like localhost or a plain ip, redirect to trustable.<ip>.nip.io, using 127.0.0.1 for localhost

