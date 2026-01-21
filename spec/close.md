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
