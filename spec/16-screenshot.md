# Screenshot recorder

`screenshot.sh` records what the launched application looks like over time. It is
an interactive loop: press Enter to capture a frame, Space to refresh the preview
from the current app, Backspace/Delete to drop the last frame, `q` to quit.

Every change rebuilds one artifact — an **animated PNG**, one second per frame,
looping forever — in the app's own repo, then copies it next to `screenshot.sh`
so it can be previewed in the editor.

Code lives in `screenshot.sh` (the loop) and `tests/screenshot.mjs` (the capture).

Runs **inside the VM only** — see "The VM guard" below.

# Why the capture is a headless browser here

An earlier design captured in the browser, from the Trustable UI, using
`getDisplayMedia`. The preview iframe is served from `vite.<domain>` while the UI
is on `trustable.<domain>`, so the page cannot rasterize the frame itself: a
cross-origin document is unreachable from script and drawing it taints the
canvas. `getDisplayMedia` works around that but costs a screen-share picker on
every capture and requires a secure context, which a plain-HTTP `nip.io` dev host
is not — so it did not work in development at all.

Inside the VM none of that applies. Playwright drives headless Chromium straight
at the Vite dev server, with no picker, no origin restriction, and no HTTPS
requirement.

# Storage layout

```
$WORKBENCH_DIR/<current>/
  screenshot/
    20260812-143052.png     one file per capture
    20260812-143119.png
  screenshot.png            animated PNG, all frames, 1s each, loops forever

<repo root>/
  screenshot.png            scratch copy for editor preview, gitignored
```

Frames are individual files named `YYYYMMDD-HHMMSS.png` from `date -u`, so they
sort chronologically as plain text and `sorted()` is the frame order. Two
captures inside the same second get a `-N` suffix rather than overwriting.

The animation is **regenerated from the directory** after every change rather
than appended in place. That is what makes Delete a one-liner — remove the newest
file and rebuild — instead of APNG chunk surgery with sequence renumbering, and
it keeps the output always consistent with the frames on disk.

An animated PNG is the only artifact. A GIF was produced alongside it at one
point and was dropped: it added a second thing to keep in sync, and the encoder
then in use silently merged identical consecutive frames, so it disagreed with
the APNG whenever the app had not changed between captures.

Nothing in the managed `.gitignore` block (`trustableGitignoreEntries` in
`gitignore.go`, spec [13-gitignore.md](13-gitignore.md)) matches `*.png` or
`screenshot/`, so the app's copy is tracked and committed normally. The root
preview copy is excluded by the repo's own `.gitignore`.

# ImageMagick cannot write APNG — do not "simplify" this back to convert

This is the least obvious constraint in the tool, so it is recorded here.

ImageMagick 6.9 (what Ubuntu 24.04 ships) has **no APNG encoder and no apng
delegate**. `convert … apng:out.png` silently shells out to `ffmpeg`:

- with ffmpeg absent it writes a **0-byte file** and reports success;
- with ffmpeg present it round-trips through a 25fps video encoder, so three
  one-second frames come back as **99 frames of 1/25s each** and `-delay 100` is
  discarded entirely.

Neither can satisfy the one-second-per-frame requirement. **ffmpeg is therefore
called directly**, and is the only image dependency:

```
ffmpeg -nostdin -y -framerate 1 -i <seq>/%05d.png -plays 0 -f apng out.png
```

`-framerate 1` writes every `fcTL` delay as `1/1` — exactly one second — and
`-plays 0` loops forever. Verified: N inputs give exactly N frames, identical
consecutive frames included, with no `disposal`/`blend` workaround needed.

`screenshot_script_test.go` asserts the string `apng:` never appears in the
script, so a future simplification back to `convert` fails the tests rather than
silently producing a broken file.

# Three ffmpeg traps, all hit during implementation

**`-nostdin` is mandatory.** ffmpeg reads stdin for interactive keys by default,
and the encode runs inside a loop whose own `read` owns stdin. Without it ffmpeg
swallows the user's keystrokes and the encode dies with *"at least one of its
streams received no packets"* — so the animation is never written at all. This
silently broke every first capture, and the identical command worked when run by
hand, which is what made it hard to see.

**Frames are fed as a `%05d` sequence of symlinks, not a glob.** ffmpeg's
`-pattern_type glob` matched nothing from inside the script (exit 234) while the
same command worked from a shell. A numbered sequence has no such ambiguity.
Symlinks keep it free — no frame data is copied.

**The concat demuxer is not usable here.** It applies a `duration` only when
another entry follows, so it silently drops the final frame; repeating that entry
to compensate adds a spurious one. Measured: 5 inputs gave 4 frames, then 6. The
frame count has to match the files on disk exactly, so concat is out.

Captures are also staged to a dotfile and renamed into place, so a frame becomes
visible to the encoder only once it is complete — the encode runs moments after
the capture writes into the same directory.

# The VM guard

The guard is the same two-stage check as `setup.sh`: the OS must be Linux, and
`/etc/os-release` must identify an Ubuntu/Debian family distribution. On macOS it
fails with a pointer to `./ssh.sh ./screenshot.sh`.

It is deliberately **not** a Lima-specific probe. "The VM" in this repo also
covers native Ubuntu development and WSL2 — `start.sh` supports both, and
`setup.sh` says so explicitly. A hostname or `/mnt/lima-*` check would break
those two environments while adding nothing.

# The loop

```
ENTER capture · SPACE refresh · DEL remove · q quit  [myapp: 3 frames]
```

| Key | Action |
|---|---|
| Enter | capture a frame of the running app |
| Space | refresh the preview from the current app — captures nothing |
| Backspace / Delete | remove the most recent frame |
| `q` | quit |

Keys are read one character at a time with `read -rsn1`, which returns an empty
string for Enter, `$'\177'` for Backspace/Delete, and the literal character
otherwise. Any unrecognized key reprints the options summary, which is also shown
at startup. A read failure (EOF, e.g. stdin is a pipe) exits the loop rather than
spinning.

The **current app is re-read from `$WORKBENCH_DIR/current` on every iteration**,
not once at startup. `writeCurrentApp` in `launch.go` rewrites that file whenever
an app is launched from the UI, so launching a different app mid-session makes
the next capture land in the new app's folder; the switch is announced.

Each app owns its own `screenshot/` directory and **existing frames are never
discarded on a switch**. That is what Space is for: launch a different app, press
Space, and its own recording is rebuilt and copied to the preview — so it is easy
to see what has already been captured for whichever app is now current. Switching
away and back continues that app's recording where it left off.

A missing `current` file and an empty one are reported as distinct, actionable
states. Empty is a real case — it must never be joined onto `$WORKBENCH_DIR/` to
produce a bare trailing slash.

# Capture

`http://localhost:5173` — the in-VM Vite origin, matching the `DevelopmentURL`
that `launch.go` declares. It is reached directly rather than through
`vite.<domain>`, which would add an ingress hop and the auth middleware for no
benefit when the tool already runs in the VM. Override with
`TRUSTABLE_SCREENSHOT_URL`.

A `curl -fsS --max-time 5` liveness check runs first, so an app that is not
serving fails immediately instead of after a browser launch.

`tests/screenshot.mjs` uses the Playwright library API re-exported by
`@playwright/test` — not `playwright test`, which would need a spec file, a
reporter, and a `test-results/` directory for a one-shot capture. Chromium is
launched with `--no-sandbox`, matching `tests/playwright.config.mjs`.

**The viewport is fixed at 600×800 and `fullPage` is false.** Both matter: every
frame in an animation must share the same dimensions, and `fullPage: true` varies
with page content. 600×800 is portrait but wide enough to clear the common mobile
breakpoint (typically 640px or 768px), so most apps render close to their tablet
or desktop layout. Remember the viewport changes *what the app renders*, not just
the image size — a narrower setting will collapse responsive layouts to their
mobile form.

## Hiding the element selector

Apps scaffolded with `@agentic-react/vite` render an element-selector toolbar on
top of the page. It would appear in every frame, so the capture removes it two
ways before the shutter:

1. **`window.__AGENTIC_REACT__.hideToolkit()`** — the plugin's own runtime API,
   and the supported route. `exitSelectionMode()` is called too, so a session
   left in selection mode does not record highlight overlays.
2. **A CSS rule on the `data-agentic-react-*` attributes** — a fallback for a
   build whose API differs, covering every piece the toolkit injects: the
   launcher, dim layers, hover and selection labels, and the tuning modal.

Both calls are optional-chained and the style tag is `.catch()`-guarded, so an
app without the plugin captures normally.

Those attributes are the only stable handle: the toolkit's elements have **no id
and no class**, and their `z-index: 2147482997` comes from a stylesheet rather
than an inline `style` attribute — an earlier `div[style*="2147482997"]` rule
matched nothing at all. Verified against a live app: the toolkit's corner drops
from 1417 distinct colours to 15 (flat background), with each mechanism working
independently of the other.

# Capturing the route the user is on

Without help the recorder always captures `/`. The route lives in a browser
`ROUTE` cookie that no server-side code reads, and the preview iframe is
cross-origin (`vite.<domain>` vs `trustable.<domain>`), so nothing on the
Trustable side can see where the user navigated.

The recorder therefore **injects a reporter into the app's `vite.config.ts`**
when the current app changes, and reads the route back over the app's MCP server.

## The reporter

A small Vite plugin added to the front of the `plugins` array:

```ts
// >>> trustable-screenshot reporter — injected by screenshot.sh, removed on quit
const trustableScreenshotReporter = () => ({
  name: 'trustable-screenshot-reporter',
  apply: 'serve',
  transformIndexHtml() { /* injects the script below */ },
});
// <<< trustable-screenshot reporter
```

It uses `transformIndexHtml`, so the script is added when the page is **served** —
the app's `index.html` on disk is never modified. That matters because the
starter template regenerates that file, and because editing it would trigger a
full Vite reload on every capture.

The injected script keeps `<meta id="__trustable_location__">` in sync with
`location.pathname + location.search + location.hash`, updating on `hashchange`,
`popstate`, `pushState`, and `replaceState`. The hash is included because these
apps use `HashRouter` — the route lives in the hash, so `pathname` alone would
always read `/`.

Injection is idempotent (the begin marker is checked first) and only applies to a
config that actually has a `plugins: [` array. Removal is byte-exact: after an
inject/remove cycle the file's md5 is unchanged.

## Reading it back

`tests/screenshot-route.mjs` calls the app's MCP server at `<app>/mcp` — mounted
by `@agentic-react/vite` — and asks `get-html-elements` for the meta element,
reading the route out of the returned `domPreview`.

MCP is used rather than loading the page ourselves because **MCP reflects the tab
the user actually has open**. Driving our own browser would only ever report the
URL we just requested.

Consequences worth knowing:

- With no browser tab open on the app there is no route to read. That is a normal
  state, not an error: the capture falls back to `/`.
- Every MCP call is bounded by a 5-second timeout, because this runs between the
  keypress and the shutter. A hung or absent server must not stall the recorder.
- An app whose config has no `plugins:` array, or no MCP server, records exactly
  as before. The feature degrades to the old behaviour rather than failing.

`TRUSTABLE_SCREENSHOT_ROUTE` overrides the detected route entirely.

## What the loop prints

The URL is announced **before** the shutter, and the fallback is named:

```
✓ capturing http://localhost:5173/#/dashboard
✓ capturing http://localhost:5173/  (no route reported — using /)
```

Both halves matter for diagnosis. Printing only after a successful capture is
useless when the wrong page was captured, and staying silent when the route is
`/` makes "the route was never detected" indistinguishable from "the route was
detected and is wrong".

## Hiding the injection

The modified `vite.config.ts` must not appear in the user's `git status`.
**`.gitignore` cannot do this** — both `vite.config.ts` and `.gitignore` are
*tracked*, and an ignore rule has no effect on a tracked file. Measured: adding
them to `.gitignore` leaves both showing ` M`; untracking them to make the rule
bite marks them `D`, which on commit **deletes `vite.config.ts` from the app's
repo** and breaks the build for anyone who clones it. This is the same trap
`gitignore.go` documents for its own managed block.

The mechanism that works is:

```
git update-index --skip-worktree vite.config.ts .gitignore
```

It is **strictly per-file**. Verified: with the flag on `vite.config.ts` only,
edits to `App.tsx`, `README.md`, and `src/main.tsx` all still appeared in
`git status` — the user's own work stays visible and committable.

Its cost is that git will not update a flagged file, so a `git pull` touching
`vite.config.ts` fails confusingly. Accepted for this workflow, and mitigated:
the flags are cleared when the recorder quits (`trap … EXIT INT TERM`) **and**
before any injection, so a session killed mid-run cannot strand them. A stale
injection left by such a session is removed on the next run.

Switching apps mid-session removes the injection from the previous app before
injecting into the new one, so only the app being recorded is ever modified.

# Installation on demand

Both installers are idempotent and run on every invocation:

- **ffmpeg** via the `APT_MISSING` array idiom from `setup.sh`: probed with
  `command -v ffmpeg`, installed with `sudo apt-get install -y ffmpeg`. The VM
  guest has passwordless sudo.
- **`fonts-noto-color-emoji`** (~10MB), because headless Chromium renders with
  the system's fonts and a bare VM has none carrying emoji glyphs. Measured: 117
  fonts installed, zero with emoji, and 🎉 ✅ 🚀 captured as empty boxes until
  this package was added. The probe is a `find` over `/usr/share/fonts` rather
  than `fc-list`, because fontconfig is **not** in the runtime image — a missing
  `fc-list` would make the check fail open and silently skip the font.
- **Playwright + Chromium** following `tests/e2e_issue98.sh`: `npm install` when
  `node_modules/@playwright/test` is absent, then `npx playwright install
  chromium`, which no-ops when the pinned revision is already cached. Skip with
  `TRUSTABLE_SCREENSHOT_SKIP_BROWSER_INSTALL=1`.

`npx playwright install-deps` is **not** run automatically — it is a large
unattended `sudo apt-get` of system libraries. If Chromium fails to launch on a
bare VM, run `npx playwright install-deps chromium` by hand.

None of this belongs in `setup.sh`: `setup_test.go` fails the Go tests if that
file contains `playwright` or `chromium`.

# Preview

**Nothing is drawn in the terminal.** After each regeneration the animated PNG is
copied to `screenshot.png` beside `screenshot.sh`, and that file is the preview:
open it in the editor and it animates, refreshing every time the recorder
rewrites it.

This replaced an earlier terminal-rendering approach, and the reason is worth
recording. Inline-image protocols are per-terminal: the iTerm2 escape sequence
renders only in iTerm2, Kitty speaks a different one, and everything else shows
either a wall of base64 or, via a tool like `chafa`, a coloured-block
approximation. Detection is unreliable on top of that — iTerm2 behind a wrapper,
or a shell that does not forward `LC_TERMINAL`, is indistinguishable from a plain
terminal. A file the editor opens sidesteps all of it: full fidelity, no
dependency, works the same in every terminal, and keeps the scrollback clean.

The copy is a scratch preview, not a record. `<app>/screenshot.png` remains the
versioned artifact; the root copy is gitignored (`/screenshot.png`) and is
deleted when the last frame is removed, so it never shows frames that no longer
exist.

## Viewing the animation

The recording is copied next to `screenshot.sh` after every change, and the loop
prints that path:

```
✓ 3 frame(s) — /path/to/trustable-app/screenshot.png
```

Open that file in the editor. Serving it over the app's own dev server was tried
and removed: it tied the recorder's output to the app being up, and cost a
cache-busting query string, for no gain over opening a file.

Note that an APNG animates in a browser but **not** in VS Code's built-in image
preview, which renders the first frame and stops. To watch it play, open the file
in a browser.

A recording whose frames are all identical animates but looks static — that is
not a defect in the file. Capture, change the app, capture again to see motion.

# Git

Every change — each capture and each delete — is **staged, never committed**:

```
git add -- screenshot screenshot.png
```

The screenshots then go out with the user's own commit and push, alongside the
app changes they illustrate, instead of arriving as a stream of separate
machine-authored commits.

The pathspec is **scoped** to `screenshot` and `screenshot.png`. The workbench is
a live user checkout with arbitrary dirty state, and an unscoped `add` would
stage the user's work-in-progress alongside the frames. `add` on the directory
also stages a removed frame as a deletion.

The tool never commits and never pushes. Publishing goes through the licensed
`/api/publish` path.

**Consequence worth knowing:** staged-but-uncommitted files are still destroyed
by the `git clean -fd` that Revert performs (see
[13-gitignore.md](13-gitignore.md)). A recording that has not yet been committed
is one Revert away from being lost — commit it with the app changes it belongs
to.

# Deleting

Backspace/Delete removes the newest file in `screenshot/` and regenerates the
animation. Deleting the last remaining frame removes `screenshot.png` entirely
rather than writing a zero-frame animation, and deletes the root preview copy so
it cannot keep showing frames that no longer exist.

To reset a recording completely, `rm -rf screenshot screenshot.png` in the app
folder and commit.

# Environment

| Variable | Meaning |
|---|---|
| `TRUSTABLE_SCREENSHOT_URL` | capture target, default `http://localhost:5173` |
| `TRUSTABLE_SCREENSHOT_ROUTE` | force a route (e.g. `/#/dashboard`), skipping MCP detection |
| `TRUSTABLE_SCREENSHOT_WIDTH` / `_HEIGHT` | viewport, default `600` / `800` |
| `TRUSTABLE_SCREENSHOT_SKIP_BROWSER_INSTALL` | `1` skips `npx playwright install chromium` |
