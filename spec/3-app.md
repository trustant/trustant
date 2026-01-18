This file describe the code for the application page, put the code in `app.html`

# Application

This page shows the current application
reading it the cookie LEFT, RIGHT, DIRECTORY, and NAME

It shows a full page, with a top bar with 10% high.

In the bar there is

- the trustable logo 90% height
- the application name
- the button (aligned to right) to go back

In the body there are two iframes, 50% width and 90% height (full page except for the top bar)

The will show:
- to the left: `<current-site>:<left-port>?directory=<DIRECTORY>` 
- to the right `<current-site>:<right-port>` 


Clicking on the button back will invoke the DELETE /api/app to stop running subprocess

# Publish

Add a Publish Button to the toolbar.
If the APIHOST is not defined it is disabled.
If you click on it:
- Ask for confirmation: "Are you sure you want to publish to <APIHOST>"
If the user confirms invoke

/api/publish with

`{
    "name": <current app>
}
`

return the result if ok or fail and an Continue button
