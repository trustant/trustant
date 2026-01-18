This file describes the home page frontend, put the code in file `index.html`
# Home Page

The home page shows centered the Trustable logo (`trusable-logo.svg`) and the text Trustable in large font

It will list the applications, using the backend api.

For each application lists a <name>, a <repo> and optionally a public api host.

The public apihost if not empty should have a link to `https://<name>.<apihost>`

You can add, remove and reset the applications using the backend api.

When you add an application it will ask for:

- an application name
- a password
- a github repo in format <org>/<repo>
- an optional public apihost (an hostname)

put as default `nuvolaris.org`
and create with the backend api.

You can select an application and delete it (ask for confirmation)

You can click the button `lanuch` to open an app
- show a launching dialog with the message `Launching `<name`
- invoke the /api/app/<name> to start it with a visual indicator you are waiting
- if there is an error, show the error and a button "continue"
- if it is ok, save in cookies:
    - the LEFT and RIGHT ports 
    - the NAME in a cookie
    - the DIRECTORY 
    - if the apihost is not empty add a cookie APIHOST with the value
- navigate to the page app.html


You can click on the button Reset
Ask for confirmation "are you sure"
If ok execute POST /api/git with value

`{
   "name": <current app>.
    "cmd":  "reset --force"
}`

Show ok or error result

