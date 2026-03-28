This file describe the code for the application page, put the code in `app.html`

# check the version

Invoke the version api at the end of the page and if it expired show a page with only a centered message saying "This version expired. Please get an updated version. For info email: info@nuvolaris.io"

# Application

This page shows the current application
reading it the cookie LEFT, RIGHT, URLDIR and NAME

It shows a full page, with a top bar with 10% high.

In the bar, aligned to the left:

- the trustable logo (80% height)
- the app name in bold
- the button "Env" (purple, gear icon)
- the button "Skills" (purple, book icon)

Aligned to the right:

- a git status indicator (dot + text)
- the button "Commit" (blue, checkmark icon, disabled when no changes)
- the button "Revert" (orange, undo arrow icon, disabled when no changes)
- the button "Upload" (green, upload arrow icon)
- the button "Back" (gray, chevron left icon)

All buttons use inline SVG icons (monochrome white, matching the button text).

In the body there are two iframes, 50% width and 90% height (full page except for the top bar), resizable horizontally

Get the URLDIR from the coookie then show the iframe:

They will show:
- to the left: `<LEFT>/session/?directory=<URLDIR>`
- to the right `<RIGHT>`

Write in console.log the values of the cookies B64DIR and URLDIR

# Git Status

On page load and every 10 seconds, invoke `GET /api/git/status/<name>` where `<name>` is read from the NAME cookie.

The API returns:
```
{ "changed": N, "added": N, "deleted": N, "clean": true/false }
```

Display in the toolbar a compact status indicator:
- If clean is true, show a green dot and "No changes"
- If clean is false, show an orange dot and a summary like "3 changed, 1 added, 2 deleted"

# Save

Add a Save button to the toolbar.
If the git status is clean (no changes), the button is disabled.
If you click on it:
- Ask for confirmation: "Save all changes to workspace?"
If the user confirms invoke

POST /api/git/save with

`{
    "name": <current app>
}`

Show a waiting indicator until the API responds.
Return the result if ok or fail and a Continue button.
After a successful save, refresh the git status indicator.

# Back

Clicking on the button back will:
- First, invoke `GET /api/git/status/<name>` to check for uncommitted changes
- If the status is not clean, show a warning modal:
  "You have unsaved changes. If you go back, changes will be lost."
  with buttons "Go back anyway" and "Cancel"
- If the user clicks "Cancel", do nothing (stay on page)
- If the user clicks "Go back anyway" or the status was clean:
  - remove the cookie B64DIR
  - invoke the DELETE /api/launch to stop running subprocess
  - navigate to applist.html

# Env (Read-only)

Add an Env button to the toolbar. Clicking it opens a modal showing environment variables (development and production) in a read-only table.

The modal fetches data from `GET /api/appconfig/<name>` and displays a table with columns: VARIABLE, Development, Production.

All values are displayed as plain text (not editable). A note at the bottom says: "To edit environment variables, use the Env button from the app list."

The modal can be closed with the X button, Escape key, or clicking the backdrop.

# Upload

Add an Upload Button to the toolbar.

It will allow to select a file to upload
and then invoke the api `/api/upload`
passing the current <name> and the uploaded file

It expects you return a full path name, show the
uploaded file in a popup allowing the user to copy
in the clipboard the filename before closing.
