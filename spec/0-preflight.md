You are building a go applications implementing a Lovable like environment based on OpenServerless

This file describes the prefligth.go module with the checks to execute

# Setup Env
When starting, before anything else:

- Read the .env  in current dir and the provided execution environment then set variables expanding them (for nested varaibels) for:

WorkspaceDir
OpenAIBaseUrl
OpenAIApiKey
OllamaEndpoint

- if there is not a file <WorkspaceDir>/trustable.json copy over the file  trustable.json in current dir as the default.

# cleanup

- if there is a file pgid in WorkspaceDir, read it and terminate the process group and remove the file

- use lsof -i and check for processes occupying in port 8910 4096 and 5173 and kill them

- check the health of OllamaEndPoint

# generate an ssh key

if not found, generate an ssh key in format ED25519 in ~/.ssh/id_trustable and his public key ~/.ssh/id_trustable.pub

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


