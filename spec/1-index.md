This file describes the home page frontend, put the code in file `index.html`

# Version
When the page load invoke the version api

if it expired show a page with only a centered message saying "This version expired. Please get an updated version. For info email: info@nuvolaris.io"

# Configuration

The first time also invoke the configuration api
expecy a steramed answer and show the messages with a modal while it is configuring

once configured set a cookie CONFIGURED=1
repeat the configuration only if the cookie is no more present or has value 0

# Home Page

The home page shows centered the Trustable logo (`trusable-logo.svg`) and the text returned by the version api in large font.

Show also in smaller font ad the end of the page "Expiration date: <date>"

It will list the applications, using the backend api.

For each application lists a <name>, a <repo> , a link "Local" to access the local application, and optinally a "Published" link if the application has a public endpoint

The Local link points to to `http://<name>.miniops.me`,
The public link points to the `<protocol>://<name>.<domain>` of the configured public apihost of the appl;iations

You can
- add applications
- remove applications
- reset applications
- edit applications

## Adding an application

When you add an application it will ask for:

- an application name
- a password
- a github repo in format <org>/<repo>
- an optional apihost url, default is `https://nuvolaris.org`

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

You can click the button `edit` to open an app
- show a launching dialog with the message `Launching `<name`
- invoke GET /api/launch/<name> to start it with a visual indicator you are waiting
- if there is an error, show the error and a button "continue"
- if it is ok, save in cookies:
    - the LEFT and RIGHT urls
    - the NAME in a cookie
    - tue URLDIR in a cookie
    - if the apihost is not empty add a cookie APIHOST with the value
- navigate to the page app.html


# Reset applications

You can click on the button Reset
Ask for confirmation "are you sure"
If ok execute POST /api/git with value

`{
   "name": <current app>.
    "cmd":  "reset --force"
}`

Show ok or error result

