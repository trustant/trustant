This file describes the interactive guided tutorials, implemented in
`web/js/tutorial.js` and loaded by `app.html` and `applist.html`. The engine is
unit-tested by `tests/tutorial-spotlight.test.mjs` (`npm run test:tutorial`).

# Tutorial pulldown

The application page shows a **Tutorial** pulldown (book icon, label
"Tutorial", chevron-down) as the **second control in the top bar**, immediately
after the sidebar open/close toggle and before **Terminal** (see
[3-app.md](3-app.md)).

It contains, in this order:

1. **Templates** — starts the templates walkthrough. The tour key stays
   `notebook`, and so do the `data-tour` names below: renaming those is a
   cross-repo protocol change with the `trustant-acp` bridge, while the label
   is only what the user reads.
2. **Menus** — starts the walkthrough of the Config and Utils pulldowns.
3. **Toolbar** — starts the walkthrough of the top bar's controls.

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
  one control**, and a spotlit control accepts keystrokes — a form can be
  spotlighted whole so its fields can be typed into.
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
- **A step is never skipped because its state already held.** A step completes
  because the user did something, not because the application happened to
  already be in that state. Entering a step whose `done` is already true shows
  it and *latches* it; it advances on the next real change. Starting the tour
  with the panel left open from an earlier run otherwise satisfied step 1 on
  its first tick and the tour appeared to begin at step 2 or 3. A step marked
  `settled` opts out — "Running" is a pure wait with nothing to read, so
  holding it would strand the user. For a `frame:` step the latch is deferred
  until the bridge reports: before the first report `done` is false only
  because nothing has been heard, and latching on that lets the first report
  skip the step.
- **A step the application completes by itself waits for Next.** Some
  conditions are satisfied without the user doing anything — the template
  catalog finishes its own fetch, so "Load templates" became true on its own
  and the card was gone within one 150 ms tick. Such a step is marked
  `confirm`: it never auto-advances, and once its condition holds it offers
  **Next** and waits for the press. The Next appears only when the condition is
  actually met, so pressing it cannot step over what the user is meant to see.
  This is distinct from the latch above, which is about a condition that was
  already true on arrival.
- **The step counter numbers what the user is shown**, not positions in the
  array. Steps that legitimately auto-complete are never numbered, so the count
  is progressive (it used to read 1, 3, 5, 8, 9). A number already handed out
  is remembered per step id, so going Back — or resuming on the next page —
  shows the same number rather than spending a new one. The total counts the
  steps that cannot be skipped plus the optional ones already shown.
- When the expected control is missing or disabled, the step waits and says
  what it is waiting for instead of advancing or failing.
- **Explanation and choice buttons are never withheld because the step is
  waiting.** `>|` and `>>` are disabled until a notebook step is selected;
  gating their **Next** on the wait left Exit as the only way forward.
- The card ends with **Skip this step** and **Exit tutorial** as two small links
  on one line, below the action buttons. Skip is absent only on a final step;
  Escape exits too.

## Never trapping the user

- **Every step offers "Skip this step"**, a small link at the foot of the card
  next to **Exit tutorial**. It is always there rather than appearing after a
  timeout: a condition the engine cannot observe — an action taken before the
  tutorial started, a control reached some other way — otherwise held the tour
  with no visible way on. The only exception is a final step, which has nothing
  after it to skip to and offers its own **Close**.
- A `frame:` step that has waited 10 s with no fresh report names the bridge in
  its waiting message, rather than leaving a silent TruACP looking like a step
  the user has not completed.
- A step whose target vanishes while its modal is still open falls back to
  spotlighting the whole modal, so error text and the buttons that recover from
  it are never buried under the mask.
- A step may declare a **precondition**: a state it needs, with its own
  spotlight and remedy text, which resolves on its own once the state holds.
  The Templates tutorial uses one for the assistant sidebar (below). A
  precondition may also carry a **`fix`**, which the tutorial runs once on
  entering the step to establish the state itself rather than waiting for the
  user — the Menus tour opens the dropdown it is describing this way, since the
  overlay makes it unopenable by the user.
- A tour may declare a **`cleanup`**, run whenever it ends — finishing, exiting
  or Escape — to put back anything it changed to describe it.
- An unknown `goto` id holds the step and says so rather than ending the
  tutorial silently.

The current tutorial and step — and `seen`, the numbers already handed out —
are kept in `sessionStorage` under
`trustant.tutorial`, so a tutorial that crosses from `app.html` to
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

Steps of the Templates tutorial point at controls inside the left iframe, which
is served from `opencode.<domain>` — a different origin, so the page cannot
read that DOM. The TruACP UI ships a tour bridge (`web/tour-bridge.ts` in the
`trustant-acp` submodule):

- While a tutorial runs, the page posts `{source: "trustant-tour-host", type:
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

# Tutorial: Templates

Runs on `app.html`. Every step that needs the frame carries the **assistant
sidebar precondition**: the sidebar toggle hides `#leftFrame` and the choice is
remembered in `localStorage`, so a user who collapsed it once would otherwise
find every step waiting on a frame that can never report. While it is hidden the
tour spotlights `#sidebarToggleBtn` — "The templates live in the assistant
panel. Click here to show it." — and continues by itself once it is back.

1. **Open Templates** — spotlight the templates icon. "Click here to open your
   templates." Advances when the panel is open.
2. **Load templates** — spotlight **Refresh**. "Refresh the list if your
   templates are not visible." The catalog loads on its own, so this is a
   `confirm` step: **Next** appears once the catalog has entries and the step
   waits for the press instead of advancing by itself.
3. **Configure templates** — point at the panel's source block and explain that
   Configure is where templates are created and the GitHub account connected.
   Nothing has to be configured; **Next** continues.
4. **Select a template** — spotlight the template named **App Suite**, falling
   back to the first entry when this catalog does not carry it. "Select a
   template to start." Advances when the template is loaded and its steps exist.
5. **Close the template list** — spotlight the panel's close button. Advances
   when the panel is closed.
6. **Run the first step** — spotlight the **Run** button of the first template
   step. Advances when the run has started (or the step has already run).
7. **Running** — no target. "Waiting for the model to finish…" Advances by
   itself when the first step has finished running.
8. **Run and move on** — spotlight **>|**: "Use this button to run the current
   step and move to the next one." Then **Run the whole template** — spotlight
   **>>**: "Use this button to run the entire template." Both continue with
   **Next**, which is offered even while those buttons are disabled.
9. **Tutorial complete** — "You now know how to open and run a template."

# Tutorial: Menus

Runs on `app.html` and describes the two pulldowns. Every step continues with
**Next**.

**The tour opens the menus itself.** The overlay swallows clicks, so a dropdown
the user is asked to look at can never be opened by the user: each step declares
a precondition whose `fix` opens the menu it describes and closes the other, so
exactly one dropdown is on screen at a time. The tour's `cleanup` closes both
however it ends — finishing, **Exit tutorial** or Escape — because a menu left
open is a page the user did not put in that state.

Steps 1-4 cover **Config**: the menu as a whole (button and dropdown spotlighted
together), then **Env** (environment variables, development and production),
**Skills** (ready-made abilities for the assistant) and **AGENTS.md** (the
instructions the assistant reads before working on the application).

Steps 5-11 cover **Utils**: the menu as a whole, then **Revert** (throw away
uncommitted changes; disabled when there is nothing to revert), **Reload and
Redeploy** spotlighted together (preview refresh versus a server-side rebuild),
**Clean**, **Debug** (the running application's log), **Files** (read-only
source browser) and **Upload**.

Step 12 closes both menus and ends the tutorial.

# Tutorial: Toolbar

Runs entirely on `app.html`. Every step explains one control of the top bar and
continues with **Next** — nothing waits on application state, so the tour never
changes the application while describing it.

The steps follow the bar from left to right:

1. **The assistant pane** — spotlight the sidebar toggle. It hides and shows the
   assistant pane; hiding it gives the preview the full width and the session
   keeps running either way.
2. **The terminal** — spotlight **Terminal**. It opens a shell inside the
   application, below the preview.
3. **Commit** — spotlight **Commit**. It saves the current state of the
   application to the workspace, stays disabled while there is nothing to save,
   and the pill to its left says whether anything changed.
4. **Desktop, tablet and mobile** — spotlight the device toggle as a whole (the
   three segments are one control). They resize the preview to a desktop, a
   tablet (820x1180) or a phone (390x844).
5. **The route** — spotlight **Route**. It chooses which page of the application
   the preview opens and allows query parameters; the current route is on the
   button.
6. **Reload** — spotlight **Reload**. It refreshes the preview; Shift+click
   redeploys the application instead, rebuilding it before reloading.
7. **Back** — spotlight **Back**. It leaves the workbench and returns to the
   application list.
8. **Tutorial complete** — recaps the seven controls and closes.


# Page requirements

The tutorials address controls by id or marker, which the pages must keep:

- `app.html`: `sidebarToggleBtn`, `terminalBtn`, `saveBtn`, `deviceToggle`,
  `routeBtn`, `reloadBtn`, `backBtn` — the seven controls of the Toolbar tour —
  and `leftFrame`, which the Templates tour reaches TruACP through.
- `app.html`, for the Menus tour: `configBtn`/`configMenu` with `configEnv`,
  `configSkills`, `configAgents`; `utilsBtn`/`utilsMenu` with `utilsRevert`,
  `utilsReload`, `utilsRedeploy`, `utilsClean`, `utilsDebug`, `utilsFiles`,
  `utilsUpload`. A menu is open exactly when its dropdown lacks `hidden`, which
  is the only state the tour drives.
- `applist.html`: `appList`, so a spotlight follows the list when it
  re-renders. The `data-tour-push="<app name>"` markers on the Git Push buttons
  are kept for a future tutorial but are not addressed by any current one.
