This file describes the interactive guided tutorials, implemented in
`web/js/tutorial.js` and loaded by `app.html` and `applist.html`. The engine is
unit-tested by `tests/tutorial-spotlight.test.mjs` (`npm run test:tutorial`).

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
  tutorial card are swallowed. The overlay swallows the press outright, so a
  click on it does not reach the document handlers that close the pages'
  dropdowns either.
- Holes are cut around the controls the step is about. They stay fully visible
  and clickable, each with a subtle outline. **A step may spotlight more than
  one control** — the Git Push form is spotlighted whole, so its repository
  field can be typed into and its Push button pressed.
- The hole is cut in the *viewport*, not in the target: the masks tile around a
  set of rectangles and nothing about the target is touched. That is what makes
  modals, arbitrary stacking contexts and the cross-origin TruACP iframe all
  work without trying to raise an element out of a frame it cannot leave. The
  viewport is banded at every hole edge and the uncovered spans of each band
  become masks, so any number of holes can be open at once.
- Mask geometry is **not** animated. A transition there means the hole arrives
  after the card has already invited the click.
- A card next to the holes says what to do. It is placed below the spotlight,
  else above, else beside it, and never covers it. With no target — a waiting
  step or a final message — the overlay covers everything and the card is
  centred; it then always says what is being waited for, so the overlay is
  never a blank white wall.
- **A click never advances a step.** A step advances only when the application
  state shows the action completed. Long operations show a spinner and a
  waiting message, and continue on their own when the operation finishes.
- When the expected control is missing or disabled, the step waits and says
  what it is waiting for instead of advancing or failing.
- **Explanation and choice buttons are never withheld because the step is
  waiting.** `>|` and `>>` are disabled until a notebook step is selected;
  gating their **Next** on the wait left Exit as the only way forward.
- The card always offers **Exit tutorial**; Escape does the same.

## Never trapping the user

- A step that has waited longer than 20 s — or 10 s with a `frame:` target and
  no fresh report — offers **Skip this step**.
- A step whose target vanishes while its modal is still open falls back to
  spotlighting the whole modal. A failed commit hides the confirm buttons and a
  failed push hides the whole form, so without this the error text and the
  buttons that recover from it are buried under the mask.
- A step may declare a **precondition**: a state it needs, with its own
  spotlight and remedy text, which resolves on its own once the state holds.
  The Notebook tutorial uses one for the assistant sidebar (below).
- An unknown `goto` id holds the step and says so rather than ending the
  tutorial silently.

The current tutorial and step are kept in `sessionStorage` under
`trustable.tutorial`, so a tutorial that crosses from `app.html` to
`applist.html` resumes on the next page. Arriving on the page a later step
belongs to counts as completing the navigation steps in between. If the user
goes somewhere else, the overlay stays up and says where to return to.

## Following what moves

The spotlight is polled every 150 ms and additionally repainted on `resize`,
capture-phase `scroll`, a `ResizeObserver` on `#leftFrame` and `#appList`, and a
`MutationObserver` on the document body (class/style/disabled). Repaints are
coalesced onto an animation frame, and mutations inside the overlay are ignored
so the observer does not feed itself.

# Reaching controls inside TruACP

Steps of the Notebook tutorial point at controls inside the left iframe, which
is served from `opencode.<domain>` — a different origin, so the page cannot
read that DOM. The TruACP UI ships a tour bridge (`web/tour-bridge.ts` in the
`trustable-acp` submodule):

- While a tutorial runs, the page posts `{source: "trustable-tour-host", type:
  "start"}` to the iframe, and `"stop"` when it ends. The request is repeated
  whenever reports dry up, so the bridge reconnects after the frame reloads.
- The frame answers every 200 ms with `{version, targets, state}`. `targets`
  maps each `data-tour` name to **every** visible match, in document order, as
  `{x, y, width, height, disabled, label}`. `state` carries `panelOpen`,
  `entries`, `nodes`, `firstNodeRunState` and `running`.
- Reports older than 1.5 s are ignored. **Missing state is unknown, never
  false** — treating it as false made `panelOpen === true` and
  `panelOpen === false` both permanently untrue and hung the tour.
- Reports are dropped unless the origin matches the `LEFT` cookie's. With no
  parsable cookie **every** report is dropped: the right-hand preview iframe
  runs user application code and must never be able to steer the spotlight.
- `version` is the protocol revision, currently 2. A bridge reporting an older
  revision is named in the card ("running an older version… rebuild TruACP and
  relaunch"), because an out-of-date TruACP is otherwise indistinguishable from
  a step the user has not completed.
- Rects are shifted by the iframe's position and **clipped to the iframe box**.
  Without the clip a scrolled notebook list punches the hole over the preview
  pane or the top bar. A target clipped away is off the frame's fold, and the
  host asks the frame to scroll it into view (`{type: "scroll", target, index}`)
  — wheel events over the overlay scroll the host document, never the frame.
- A target is addressed as `frame:<name>`, or `frame:<name>@<index>` /
  `frame:<name>@<label>` to pick one match among several.

Target names: `notebook-toggle`, `notebook-refresh`, `notebook-source`,
`notebook-entry`, `notebook-close`, `notebook-node-run`, `run-next`, `run-all`.

# Tutorial: Notebook

Runs on `app.html`. Every step that needs the frame carries the **assistant
sidebar precondition**: the sidebar toggle hides `#leftFrame` and the choice is
remembered in `localStorage`, so a user who collapsed it once would otherwise
find every step waiting on a frame that can never report. While it is hidden the
tour spotlights `#sidebarToggleBtn` — "The notebooks live in the assistant
panel. Click here to show it." — and continues by itself once it is back.

1. **Open Notebook** — spotlight the notebook icon. "Click here to open your
   notebooks." Advances when the panel is open.
2. **Load notebooks** — spotlight **Refresh**. "Refresh the list if your
   notebooks are not visible." Advances when the catalog has entries, so an
   already-loaded catalog passes straight through.
3. **Configure notebooks** — point at the panel's source block and explain that
   Configure is where notebooks are created and the GitHub account connected.
   Nothing has to be configured; **Next** continues.
4. **Select a notebook** — spotlight the template named **App Suite**, falling
   back to the first entry when this catalog does not carry it. "Select a
   notebook to start." Advances when the template is loaded and its steps exist.
5. **Close the notebook list** — spotlight the panel's close button. Advances
   when the panel is closed.
6. **Run the first step** — spotlight the **Run** button of the first notebook
   step. Advances when the run has started (or the step has already run).
7. **Running** — no target. "Waiting for the model to finish…" Advances by
   itself when the first step has finished running.
8. **Run and move on** — spotlight **>|**: "Use this button to run the current
   step and move to the next one." Then **Run the whole notebook** — spotlight
   **>>**: "Use this button to run the entire notebook." Both continue with
   **Next**, which is offered even while those buttons are disabled.
9. **Tutorial complete** — "You now know how to open and run a notebook."

# Tutorial: Commit and Push

Starts on `app.html` and finishes on `applist.html`. It remembers the launched
application name (the `NAME` cookie) so it can find that application in the
list.

1. **Commit** — spotlight the **Commit** button. "Click Commit to save your
   application to the workspace." While the button is disabled the step says
   there is nothing to commit and that the tutorial has to be started again
   after changing something, because the overlay covers the editor. Advances
   when the commit modal opens.
2. **Confirm the commit** — spotlight the modal's **Commit** button, show
   "Committing…" while it runs, and advance only on a successful commit. A
   failed commit spotlights the modal instead, so the message and **Continue**
   stay readable and clickable.
3. **Committed** — spotlight **Continue**; advances when the modal closes.
4. **Back** — spotlight the **Back** button. "Go back to continue." Leaving the
   page is what completes it.
5. **Choose what to do** (application list) — spotlight the **Git Push** button
   of that application; the card offers **Cancel** as the only other action.
   Everything else stays disabled. Clicking Git Push continues the push branch,
   Cancel goes to the cancelled ending. When the application is not in the list
   the step says so and suggests clearing the search box.
6. **Push to GitHub** — while the push runs, show "Pushing to GitHub…". When
   the repository is not configured yet, the **whole push form** is spotlighted
   so the repository can be typed and the SSH deploy key read. A failed push
   does not complete the tutorial; it says so and the user can retry. A license
   failure swaps in the license modal, which the step reports rather than
   waiting on a modal it cannot reach.
7. **Endings** — after a push: "Your application has been committed and pushed
   to GitHub." After Cancel: "Your application has been committed to the
   workspace. You can push it to GitHub later."

# Page requirements

The tutorials address controls by id or marker, which the pages must keep:

- `app.html`: `saveBtn`, `saveConfirmBtn`, `saveContinueBtn`, `saveModal`,
  `saveResult`, `saveResultText`, `saveSpinner`, `backBtn`, `leftFrame`,
  `sidebarToggleBtn`.
- `applist.html`: `data-tour-push="<app name>"` on every Git Push button (grid
  and list view), `appList`, `gitPushModal`, `gitPushForm`, `gitPushResult`,
  `gitPushResultText`, `licenseModal`.

A successful commit is recognised by the `text-green-700` class on the commit
result and a failed one by `text-red-700`; a successful push by
`nu-feedback-success` on the push result.
