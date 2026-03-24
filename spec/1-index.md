This file describes the home page frontend, put the code in file `index.html`

# Version

When the page load invoke the version api

if it expired show a page with only a centered message saying "This version expired. Please get an updated version. For info email: info@nuvolaris.io"

# Configuration

Every time the index page is opened, invoke the configuration api.
Expect a streamed answer and show the messages with a modal while it is configuring.

Then invoke the openai ai api using informations in trustable.json, env.OPENAI_BASE_URL and env.OPENAI_API_KEY and the opencode.default model, asking hello.

If you get "error", show popup saying "you are not logged in ollama cloud.\nPlease execute `ops trustable signin` and a button Retry

Repeat until the test succeeded

# Home Page

The home page shows centered the Trustable logo (`trustable-logo.svg`) and the text returned by the version api in large font.

Show also in smaller font ad the end of the page "Expiration date: <date>"

It will list the applications, using the backend api.

For each application lists a <name>, a <repo> , a link "Development" to access the local application, and optional a "Production" link if the application has a public endpoint defined in a `.env.production`

The Local link points to to `http://<name>.miniops.me`,
The public link points to the `<protocol>://<name>.<domain>` where `<protocol>://<domanin>` is defined in `.env.production` as the value of OPS_APIHOST

You can
- configure (general)
- add applications
- remove applications
- reset applications
- edit applications
- configure application

## Adding an application

When you add an application it will ask for:

- an application name
- a password
- a github repo in format <org>/<repo>

with a button "Create" and "Cancel"

show also a message:

"To read private GitHub repositories and write back your changes, you need to add our ssh public key to your GitHub account." and a button "Show key".

If you click a button a popup showing the ~/.ssh/id_trustable.pub will be shown, with a button to copy on clipboard and a button to close the popup.

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

# Reset applications

You can click on the button Reset
Ask for confirmation "are you sure"
If ok execute POST /api/git with value

`{
   "name": <current app>,
    "cmd":  "reset --hard"
}`

Show ok or error result

# Configure Application

The configurator allows to edit `.env` and `.env.production`

It is a table with 3 columts: "VARIABLE", "Development", "Production"

Each row shows a text filed to edit the variable and the value for development and production

Read the .env and the .env.production allowing to add and remove variables.
If a file is missing or a value is missing default to empty string.

The first 3 rows are:

- OPS_APIHOST
- OPS_USER
- OPS_PASSWORD

those 3 cannot be changed or removed
the development value cannot be changed
the pruduction value can be changed

Then there are the env vars listed in `trustable.json` in section `env`
there cannot be added or removed but both the development and pruduction value can be changed

then you can add and remove other variables set both development and production values.

There are the buttons:

- Save
- Close

The Save button will save both `.env` and `.env.production`
The Close will come back, warning if there are unsaved changes.
