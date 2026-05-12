You are building a go applications implementing a Lovable like environment based on OpenServerless

This file describes the prefligth.go module with the checks to execute

# Setup Env
When starting, before anything else:

- Read the .env in current dir and set the variables. If `.env` is missing, the app fails to start.

show in the log the variables set

propagate the env to all the subcommands invoked

use the provided environment to set internal variables
expanding them also using environment variables for:

WorkspaceDir
WorkbenchDir
OpenAIBaseUrl
OpenAIApiKey
OllamaEndpoint
OpsSkills (defaults to "trustable-ai/skills", not editable in development)

- Run migration: if the workspace `trustable.json` does not yet have an `apps` section, scan `<WorkspaceDir>/workspace/*/` for existing app directories, retrieve each app's password via `ops util kubeget whiskuser/<name> .spec.password`, build `apps` entries, strip fields that match the base config, and save the updated workspace `trustable.json`.

# cleanup

- if there is a file pgid in WorkspaceDir, read it and terminate the process group and remove the file

- use lsof -i and check for processes occupying in port 8910 4096 and 5173 and kill them

# check ssh key

Ensure `~/.ssh/id_ed25519` exists. If it does, set `sshKeyAvailable` to true (and derive the matching `.pub` via `ssh-keygen -y` when missing). If it does not, generate a passphrase-less ed25519 keypair at that path (`ssh-keygen -t ed25519 -N "" -C "trustable" -f ~/.ssh/id_ed25519`), chmod 600 both files, and set `sshKeyAvailable` to true. Set `sshKeyAvailable` to false only if directory creation or key generation fails.

# web server

the application itself is a web server:
- serving pages
- proxying ports according the host name
- implementing apis described in the spec

The web application requires you always access the application with a full fqdn like trustable.<domain>[:<port>]

- if you detect localhost  redirect to trustable.127.0.0.1.nip.io:<port>,
- if you detect an <ip> redirect to trustable.<ip>.nip.io:<port>

- if detect a fqdn like <host>.<domain> (no '.' in <host>, <domain> can include '.') do the following:
- if <host> is 'trustable', serve the folder `web`
- if <host> is 'opencode', proxy pass to port 4096
- if <host> is 'vite',  proxy pass to port 5173
