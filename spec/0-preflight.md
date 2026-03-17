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

# web server

the application itself is a web server serving pages or proxying ports according the host name

requires you always access the application with a full fqdn like trustable.<domain>[:<port>]

- if you detect localhost  redirect to trustable.127.0.0.1.nip.io:<port>,
- if you detect an <ip> redirect to trustable.<ip>.nip.io:<port>

- if detect a fqdn like <host>.<domain> (no '.' in <host>, <domain> can include '.') do the following:
- if <host> is 'trustable', serve the folder `web`
- if <host> is 'opencode', proxy pass to port 4096
- if <host> is 'vite',  proxy pass to port 5173


Invoke an api /info in all the pages at the start that retuns the message if not expired:

`{"info": "Trustable v<version> expiring <date>"}

if the api returns expired just show in every "a centered message: "This version expired, please download an updated version"


