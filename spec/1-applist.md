This file describes the application list page, put the code in file `applist.html`

# Version

When the page loads invoke the version api.

If it expired show a page with only a centered message saying "This version expired. Please get an updated version. For info email: info@nuvolaris.io"

# App List Page

The page shows centered the Trustable logo (`trustable-logo.svg`) and the text returned by the version api in large font.

Show also in smaller font at the end of the page "Expiration date: <date>"

It will list the applications, using the backend api.

For each application lists a <name>, a <repo> , a link "Development" to access the local application, and optionally a "Production" link and a "Repository" link.

The Local link points to `<protocol>://<name>.<domain>` by replacing the first
label of the current Trustable host. For example, from
`http://trustable.192.168.1.124.nip.io` it points to
`http://<name>.192.168.1.124.nip.io`, and from
`https://trustable.bestia.opsv.xyz` it points to
`https://<name>.bestia.opsv.xyz`.
The Production link is shown when both `OPS_APIHOST` and `OPS_USER` are defined in `.env.production`. It points to `<protocol>://<opsuser>.<domain>` where `<protocol>://<domain>` comes from OPS_APIHOST and `<opsuser>` comes from OPS_USER.
The Repository link is shown when `OPS_REPO` is defined in `.env.production`. It points to `https://github.com/<opsrepo>` where `<opsrepo>` is the value of OPS_REPO.

You can
- configure (general)
- add applications
- remove applications
- edit applications
- env (configure application environment variables)
- git push (push code to a production GitHub repository)
- publish (deploy to a production OpenServerless environment)

## Adding an application

When you add an application it will ask for:

- an application name
- a password
- a github repo in format <org>/<repo>

with a button "Create" and "Cancel"

Show "Use our starter:" followed by a clickable link: trureact.
Clicking the link fills the form: Name with "trureact", Password with "trureact", and Repo with "trustable-ai/trureact".

If the SSH key is available (GET /api/sshkey returns 200), show a message:

"To read private GitHub repositories and write back your changes, you need to add our ssh public key to your GitHub account." and a button "Show key".

If you click the button, a popup showing the `~/.ssh/id_ed25519.pub` content (fetched from /api/sshkey) will be shown, with a message "A local copy of the private key is in ~/.ssh/id_trustable", a button to copy on clipboard and a button to close the popup.

If the SSH key is not available, do not show this message.

## SSH Key link

If the SSH key is available, show an "SSH Key" link below the applications list. Clicking it opens the same SSH key popup described above.

If you cancel, go back

If you confirm, create with the backend api /api/repo

Show a waiting modal until the api call completes. Once completed the modal should show the result and ask for the user to say ok.

# Edit application

Calculate LEFT and RIGHT url from the location.
Expect a domain in format `<protocol>://trustable.<domain>[:<port>]`,
show an error if it is not in this format. Let:
- LEFT is `<protocol>://opencode.<domain>:<port>`
- RIGHT is `<protocol>://vite.<domain>:<port>`
- URLDIR is the url encoded full path for the directory of the application

You can click the button `edit` to open an app
- show a launching dialog with the message `Launching `<name`
- invoke GET /api/launch/<name> to start it with a visual indicator you are waiting
- if there is an error, show the error and a button "continue"
- if it is ok, save in cookies:
  - the LEFT and RIGHT urls
  - the NAME in a cookie
  - the URLDIR in a cookie
  navigate to the page app.html

## Revert (in app.html)

In the application screen (app.html), next to the Commit button, show a Revert button. The button is disabled when there are no uncommitted changes (same condition as Commit).

When clicked, ask for confirmation "Are you sure you want to revert all uncommitted changes?"
If ok execute POST /api/git with value

`{
   "name": <current app>,
    "cmd":  "checkout ."
}`

The backend handles `checkout .` by also running `git clean -fd` to remove untracked files.

Show ok or error result. After a successful revert, refresh the git status.

# Git Push

Each app card has a "Git Push" button. Clicking it calls `POST /api/publish/push` with the app name.

If the backend returns `{"needs_config": true}`, show a popup asking for:
- The production repository in org/repo format
- The SSH key notice (same as in app creation: yellow box with "Show Key" button, only if SSH key is available)
- A note: "The SSH public key must be added to this repository's deploy keys or your GitHub account."

Once the repo is set, the backend saves it as `OPS_REPO` in production config, adds a "production" git remote, and runs `git push production main` using the SSH key.

If `needs_config` was not returned (already configured), skip the form and show the result directly.

Show spinner during the operation and result on completion.

# Publish

Each app card has a "Publish" button. Clicking it calls `POST /api/publish/remote` with the app name.

If the backend returns `{"needs_config": true}`, show a popup asking for:
- OPS_APIHOST (placeholder: "https://your-openserverless-host.com")
- OPS_USER
- OPS_PASSWORD
- A note: "You need an OpenServerless environment for publishing. Contact info@nuvolaris.io or check https://openserverless.apache.org"

Once configured, the backend ensures the workbench exists (clones from workspace if needed), generates env files including `.env.production`, runs `ops ide login --mode=production`, then `ops ide deploy`.

If `needs_config` was not returned (already configured), skip the form and show the result directly.

Show spinner during the operation and result on completion.

# Env (Configure Application Environment)

Each app card has an "Env" button that opens the environment configurator (`appconfig.html`).

The configurator allows to edit `.env` and `.env.production`

It is a table with 3 columts: "VARIABLE", "Development", "Production"

Each row shows a text filed to edit the variable and the value for development and production

Read the .env and the .env.production allowing to add and remove variables.
If a file is missing or a value is missing default to empty string.

The first 4 rows are:

- OPS_APIHOST
- OPS_USER
- OPS_PASSWORD
- OPS_REPO

those 4 cannot be changed or removed
the development value cannot be changed
the pruduction value can be changed

OPS_REPO is initialized to the git remote origin (org/repo) of the application.

Then there are the env vars listed in `trustable.json` in section `env`
there cannot be added or removed but both the development and pruduction value can be changed

then you can add and remove other variables set both development and production values.

There are the buttons:

- Save
- Close

The Save button will save both `.env` and `.env.production`
The Close will come back, warning if there are unsaved changes.
