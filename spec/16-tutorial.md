This file describes the interactive guided tutorials, implemented in
`web/js/tutorial.js` and loaded by `app.html` and `applist.html`.

# Tutorial pulldown

The application page shows a **Tutorial** pulldown (book icon, label
"Tutorial", chevron-down) in the left group of the top bar, immediately to the
right of the **Utils** pulldown (see [3-app.md](3-app.md)).

It contains, in this order:

1. **Notebook** — starts the notebook walkthrough.
2. **Commit and Push** — starts the commit-and-push walkthrough.

The pulldown closes after an item is selected and when the user clicks outside
it, exactly like Config and Utils.

# Interaction model

A tutorial guides one action at a time:

- A semi-transparent white overlay covers the whole application. It is what
  disables the rest of the interface: clicks land on the overlay and never
  reach what is underneath, and keystrokes outside the spotlight and the
  tutorial card are swallowed.
- A hole is cut around the single control the step is about. The control stays
  fully visible and clickable, with a subtle outline drawn around it.
- A card next to the hole says what to do. It is placed below the target, or
  above it when there is no room, and never covers it. The spotlight and the
  card move smoothly when a step changes.
- With no target — a waiting step or a final message — the overlay covers
  everything and the card is centred.
- **A click never advances a step.** A step advances only when the application
  state shows the action completed. Long operations show a spinner and a
  waiting message, and continue on their own when the operation finishes.
- When the expected control is missing or disabled, the step waits and says
  what it is waiting for instead of advancing or failing.
- The card always offers **Exit tutorial**; Escape does the same.
- Explanation-only steps ("this button does X") offer a **Next** button,
  because there is no state change to wait for.

The current tutorial and step are kept in `sessionStorage` under
`trustable.tutorial`, so a tutorial that crosses from `app.html` to
`applist.html` resumes on the next page. Arriving on the page a later step
belongs to counts as completing the navigation steps in between. If the user
goes somewhere else, the overlay stays up and says where to return to.

# Reaching controls inside TruACP

Steps of the Notebook tutorial point at controls inside the left iframe, which
is served from `opencode.<domain>` — a different origin, so the page cannot
read that DOM. The TruACP UI ships a tour bridge (`web/tour-bridge.ts` in the
`trustable-acp` submodule):

- While a tutorial runs, the page posts `{source: "trustable-tour-host", type:
  "start"}` to the iframe, and `"stop"` when it ends. The request is repeated
  whenever reports dry up, so the bridge reconnects after the frame reloads.
- The frame answers every 200 ms with the viewport rect of each `data-tour`
  control and the notebook state: `panelOpen`, `entries`, `nodes`,
  `firstNodeRunState`, `running`. Reports older than 1.5 s are ignored, and
  reports from any origin other than the `LEFT` cookie's are dropped.
- Rects are shifted by the iframe's own position and spotlighted like any other
  target. The hole passes the click through to the real control in the frame.

Target names: `notebook-toggle`, `notebook-refresh`, `notebook-source`,
`notebook-entry`, `notebook-close`, `notebook-node-run`, `run-next`, `run-all`.

# Tutorial: Notebook

Runs on `app.html`.

1. **Open Notebook** — spotlight the notebook icon. "Click here to open your
   notebooks." Advances when the panel is open.
2. **Load notebooks** — spotlight **Refresh**. "Refresh the list if your
   notebooks are not visible." Advances when the catalog has entries, so an
   already-loaded catalog passes straight through.
3. **Configure notebooks** — point at the panel's source block and explain that
   Configure is where notebooks are created and the GitHub account connected.
   Nothing has to be configured; **Next** continues.
4. **Select a notebook** — spotlight the first template in the list (for
   example App Suite). "Select a notebook to start." Advances when the template
   is loaded and its steps exist.
5. **Close the notebook list** — spotlight the panel's close button. Advances
   when the panel is closed.
6. **Run the first step** — spotlight the **Run** button of the first notebook
   step. Advances when the run has started (or the step has already run).
7. **Running** — no target. "Waiting for the model to finish…" Advances by
   itself when the first step has finished running.
8. **Run and move on** — spotlight **>|**: "Use this button to run the current
   step and move to the next one." Then **Run the whole notebook** — spotlight
   **>>**: "Use this button to run the entire notebook." Both continue with
   **Next**.
9. **Tutorial complete** — "You now know how to open and run a notebook."

# Tutorial: Commit and Push

Starts on `app.html` and finishes on `applist.html`. It remembers the launched
application name (the `NAME` cookie) so it can find that application in the
list.

1. **Commit** — spotlight the **Commit** button. "Click Commit to save your
   application to the workspace." While the button is disabled the step waits:
   "There is nothing to commit yet — change something in your application
   first." Advances when the commit modal opens.
2. **Confirm the commit** — spotlight the modal's **Commit** button, show
   "Committing…" while it runs, and advance only on a successful commit. A
   failed commit leaves the step in place so the message can be read.
3. **Committed** — spotlight **Continue**; advances when the modal closes.
4. **Back** — spotlight the **Back** button. "Go back to continue." Leaving the
   page is what completes it.
5. **Choose what to do** (application list) — spotlight the **Git Push** button
   of that application; the card offers **Cancel** as the only other action.
   Everything else stays disabled. Clicking Git Push continues the push branch,
   Cancel goes to the cancelled ending.
6. **Push to GitHub** — while the push runs, show "Pushing to GitHub…". When
   the repository is not configured yet, the push form appears and the
   spotlight moves to its **Push** button ("Enter the GitHub repository and
   press Push"). A failed push does not complete the tutorial; it says so and
   the user can retry.
7. **Endings** — after a push: "Your application has been committed and pushed
   to GitHub." After Cancel: "Your application has been committed to the
   workspace. You can push it to GitHub later."

# Page requirements

The tutorials address controls by id or marker, which the pages must keep:

- `app.html`: `saveBtn`, `saveConfirmBtn`, `saveContinueBtn`, `saveModal`,
  `saveResult`, `saveResultText`, `saveSpinner`, `backBtn`, `leftFrame`.
- `applist.html`: `data-tour-push="<app name>"` on every Git Push button (grid
  and list view), `gitPushModal`, `gitPushForm`, `gitPushConfirmBtn`,
  `gitPushResult`, `gitPushResultText`.

A successful commit is recognised by the `text-green-700` class on the commit
result, a successful push by `nu-feedback-success` on the push result.
