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
- the button "Memory" (purple, brain icon)
- the button "Upload" (green, upload arrow icon)

Centered

- the button Redeploy with a rocket icon

Aligned to the right:

- a git status indicator (dot + text)
- the button "Commit" (blue, checkmark icon, disabled when no changes)
- the button "Revert" (orange, undo arrow icon, disabled when no changes)
- the button "Route: /" (teal, home icon) — displays the current value of the ROUTE cookie (defaults to "/")
- the button "Query" (teal, question mark icon)
- the button "Back" (gray, chevron left icon)

All buttons use inline SVG icons (monochrome white, matching the button text).

In the body there are two iframes, 50% width and 90% height (full page except for the top bar), resizable horizontally

Get the URLDIR from the cookie then show the iframe. URLDIR must be the raw or
URL-encoded absolute application directory. For compatibility, if URLDIR is a
base64-url-safe path or is missing while B64DIR is present, decode it first.
Before opening opencode, always regenerate B64DIR from the raw absolute path:

They will show:
- to the left: `<LEFT>/<B64DIR>/session`
- to the right: `<RIGHT><ROUTE>#<ROUTE>` where `<ROUTE>` is the value of the ROUTE cookie (defaults to "/")

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

# Memory

Add a Memory Button with Brain icon to the toolbar.

Clicking it will show the text editor codejar allowing to edit the file AGENTS.md with a popup centered and the buttons save and cancel

Create the file AGENTS.md if it is not there and add to git when creating.

# Upload

Add an Upload Button to the toolbar.

It will allow to select a file to upload
and then invoke the api `/api/upload`
passing the current <name> and the uploaded file

It expects you return a full path name, show the
uploaded file in a popup allowing the user to copy
in the clipboard the filename before closing.

# Query & Route


The Route button in the toolbar displays "Route: <route>" where `<route>` is the current value of the ROUTE cookie (defaults to "/").

When clicked, show a popup asking "Enter new route:" with a text input pre-filled with the current route value and buttons "OK" and "Cancel", set the cookie ROUTE if ok


The Query button shows a query string editor, a sequence of key/values

You can add and remove a couple key var.

Show buttons Ok and Cancel, set the cooke QUERY as url encoded query string of key values set by the editor

If the user confirms:
- Update the button label to show the new route
- Reload the right iframe using
`<RIGHT><new_route>?<query>#<new_route>` as the URL

# Redeploy

The Redeploy button (centered in the toolbar, indigo, rocket icon) triggers a server-side redeploy cycle.

When clicked:

- Replace the right iframe content with a spinner and status message "Redeploying..."
- Open an EventSource to `GET /api/redeploy?name=<NAME>`
- The API streams SSE events (`event: status`) updating the iframe status text as each step progresses:
  - Terminating ops ide devel
  - Waiting for port 5173 to be free
  - Deploying actions (ops ide deploy)
  - Getting action list (ops action list)
  - Starting dev server (ops ide devel --fast)
  - Waiting for dev server to be ready (HTTP HEAD check)
- On `event: done`, show "Redeploy complete", the action list in a code block, and an OK link pointing to `<RIGHT><ROUTE>?<QUERY>#<ROUTE>`
- On `event: error`, stop the spinner and show the error in red
