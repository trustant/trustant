This file describes the index splash screen page, put the code in file `index.html`

# Version

When the page loads invoke the version api.

If it expired show a page with only a centered message saying "This version expired. Please get an updated version. For info email: info@nuvolaris.io"

# Splash Screen

The page shows centered the Trustable logo (`trustable-logo.svg`) in large size, the text "Trustable" in large font, and below it the version (e.g. "v1.2.3") in large font.

Show also in smaller font "Expiration date: <date>"

# Configuration

Every time the index page is opened, invoke the configuration api.
Expect a streamed answer and show the messages with a modal while it is configuring.

Then invoke the openai ai api using informations in trustable.json, env.OPENAI_BASE_URL and env.OPENAI_API_KEY and the opencode.default model, asking hello.

If you get "error", show a sign-in required popup:

- If the page URL has a query string, show "Click here to login to Ollama Cloud" as a link pointing to `https://ollama.com/connect?<query_string>` and a Retry button.

- If no query string, try execute `ollama signin` and parse the output, looking for a string starting with `https://ollama.com/connect`, and show  "Click here to login to Ollama Cloud" with a link pointing to the found url

- if  there is not a login message, show "you are not logged in ollama cloud.\nPlease execute `ollama signin` and click Retry button.

Repeat until the test succeeded.

Once configuration completes successfully, navigate to `applist.html`.
