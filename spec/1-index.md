This file describes the index splash screen page, put the code in file `index.html`

# Version

When the page loads invoke the version api.

If it expired show a page with only a centered message saying "This version expired. Please get an updated version. For info email: info@nuvolaris.io"

# Splash Screen

The page shows centered the Trustable logo (`trustable-logo.svg`) in large size and the text "Trustable" with the version in large font.

Show also in smaller font "Expiration date: <date>"

# Configuration

Every time the index page is opened, invoke the configuration api.
Expect a streamed answer and show the messages with a modal while it is configuring.

Then invoke the openai ai api using informations in trustable.json, env.OPENAI_BASE_URL and env.OPENAI_API_KEY and the opencode.default model, asking hello.

If you get "error", show popup saying "you are not logged in ollama cloud.\nPlease execute `ops trustable signin` and a button Retry

Repeat until the test succeeded.

Once configuration completes successfully, navigate to `applist.html`.
