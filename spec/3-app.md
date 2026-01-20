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


# Add a button Close to the toolbar

when clicking close will invoke the underlying close in opencode 
in the left ifrane

To click the "Close" button from JavaScript when OpenCode UI is inside an iframe:

// 1. Get the iframe
const iframe = document.querySelector('left')

// 2. Access the iframe document (only works if same-origin)
const iframeDoc = iframe.contentDocument || iframe.contentWindow.document

// 3. Click the menu button (the three dots "...")
const menuButton = iframeDoc.querySelector('.group\\/project button')
menuButton.click()

// 4. Wait for the menu to open, then click "Close"
setTimeout(() => {
  const closeItem = Array.from(iframeDoc.querySelectorAll('[role="menuitem"]'))
    .find(el => el.textContent.includes('Close'))
  
  if (closeItem) closeItem.click()
}, 100)
