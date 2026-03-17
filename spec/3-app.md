This file describe the code for the application page, put the code in `app.html`

# check the version

Invoke the version api at the end of the page and if it expired show a page with only a centered message saying "This version expired. Please get an updated version. For info email: info@nuvolaris.io"

# Application

This page shows the current application
reading it the cookie LEFT, RIGHT, URLDIR and NAME

It shows a full page, with a top bar with 10% high.

In the bar there is

- the trustable logo 90% height
- the text returned by the api version
- the button (aligned to right) to go back

In the body there are two iframes, 50% width and 90% height (full page except for the top bar), resizable horizontally

Get the URLDIR from the coookie then show the iframe:

They will show:
- to the left: `<LEFT>/session/?directory=<URLDIR>`
- to the right `<RIGHT>`

Write in console.log the values of the cookies B64DIR and URLDIR


Clicking on the button back will
- remove the cookie B64DIR
- invoke the DELETE /api/launch to stop running subprocess

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

# Upload

Add an Upload  Button to the toolbar.

It will allow to select a file to upload
and then invoke the api `/api/upload`
passing the current <name> and the uploaded file

It expects you return a full path name, show the
uploaded file in a popup allowing the user to copy
in the clipboard the filename before closing.