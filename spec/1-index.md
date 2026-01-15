# Home Page (`index.html`)

The home page shows centered the Trustable logo (`trusable-logo.svg`) and the text Trustable

It will list the applications, using the backend api.

For each application lists a <name>, a <repo> and optionally a public api host.

You can add, and remove the applications using the backend api.

When you add an application it will ask for:

- an application name
- a password
- a github repo in format <org>/<repo>
- an optional public apihost (an hostname)

and create with the backend api.

You can select an application and delete it (ask for confirmation)

You can open an app, this will save the current application in a cookie and navigate to the apps.page

