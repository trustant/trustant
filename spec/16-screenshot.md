# Screenshot recorder

`screenshot.sh` records what the launched application looks like over time.

**The app captures itself.** A shutter button injected into the running app takes
the frame; the recorder only collects it. The loop is interactive: press Enter to
collect the frames you have captured, Space to refresh the preview from the
current app, Backspace/Delete to drop the last frame, `q` to quit.

Every change rebuilds one artifact — an **animated PNG**, one second per frame,
looping forever — in the app's own repo, then copies it next to `screenshot.sh`
so it can be previewed in the editor.

All the code lives in `screenshot.sh`: the loop, and the Vite plugin it injects
into the app (the button, the capture, and the upload endpoint).

Runs **inside the VM only** — see "The VM guard" below.

# Why the page captures itself

The recorder used to drive headless Chromium at the app's URL. That reproduces
the *route* but not the *tab*: form input, open modals, scroll position, and
anything the app keeps in tab storage — auth included — are all absent from a
fresh browser. The recording showed a page the user had never seen.

An earlier design, in the Trustable UI, rejected `getDisplayMedia` for reasons
that were correct **there** and do not apply **here**. That reasoning is easy to
re-derive and get wrong, so both halves are recorded:

- *In the Trustable UI*: the preview iframe is served from `vite.<domain>` while
  the UI is on `trustable.<domain>`, so the page cannot rasterize the frame
  itself — a cross-origin document is unreachable from script and drawing it
  taints the canvas. `getDisplayMedia` works around that, but requires a secure
  context, and a plain-HTTP `nip.io` dev host is not one.
- *In the app itself, inside the VM*: neither holds. A page capturing **itself**
  is same-origin by definition, and `http://localhost:5173` **is** a secure
  context under the localhost exception.

A `MediaStream` from `getDisplayMedia` also does **not** taint the canvas, unlike
a cross-origin `<img>` — so `toBlob` works. That is precisely the property the
original UI design was reaching for and could not have.

The cost is a screen-share picker on every click. That is accepted: it buys a
pixel-perfect capture of the real tab, with no bundled dependency and no change
to the app's `package.json`. Chromium's `preferCurrentTab` reduces it to a single
confirm; Firefox and Safari show a full picker, so **Chromium is the supported
browser for recording**.

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

The **● button at the top right of the app is the shutter**; the keys below only
manage what it has already captured.

```
ENTER collect · SPACE refresh · DEL remove · q quit  [myapp: 3 frames]
```

| Key | Action |
|---|---|
| Enter | collect the frames you have captured |
| Space | refresh the preview from the current app — collects nothing |
| Backspace / Delete | remove the most recent frame |
| `q` | quit |

Enter **never blocks**. An empty queue is the normal state between clicks, not a
wait condition, and a blocking collect would make `q` unreachable — leaving `^C`
as the only way out. Collecting drains the whole queue, so three clicks followed
by one Enter yields three frames.

A background poller was rejected: its output would land mid-line on the prompt
the foreground loop has already written, and fixing that needs cursor
save/restore escapes — exactly the per-terminal fragility this tool avoids
elsewhere. It would also race `regenerate` and `git add` against a foreground
Delete.

Keys are read one character at a time with `read -rsn1`, which returns an empty
string for Enter, `$'\177'` for Backspace/Delete, and the literal character
otherwise. Any unrecognized key reprints the options summary, which is also shown
at startup.

**The read times out (`-t 2`), so the loop polls rather than blocking.** This is
load-bearing, not a nicety: the app-switch check sits *above* the read, so with a
blocking read a newly launched app is only noticed when the user next happens to
press a key — and gets no shutter until then. Launching an app and seeing no
button is exactly the symptom.

A timeout and EOF are both non-zero exits from `read`, and they must be told
apart by status: **`>128` is the timeout** and loops again, while anything else
means stdin is not a terminal (a pipe), where looping would spin forever.

Because the loop now ticks on its own, the prompt line is only redrawn when its
text actually changes (`$PROMPT` vs `$LAST_PROMPT`). Reprinting on every tick
would scroll the terminal continuously while the user does nothing. Any action
that prints above the prompt clears `LAST_PROMPT` to force one redraw.

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
serving fails immediately instead of after a fruitless retrieval.

## 600×800 is the output canvas, not a viewport

The numbers are unchanged from the Playwright design but **their meaning has
inverted**, which makes this the paragraph most likely to mislead.

It no longer controls what the app renders — the app renders at whatever size the
user's window happens to be. The grabbed frame is contain-fit onto a fixed
600×800 canvas, filled white and centred, so the whole tab is always visible and
every frame is always the same size. Override with `TRUSTABLE_SCREENSHOT_WIDTH` /
`_HEIGHT`.

**The fixed size is load-bearing.** ffmpeg's APNG encoder requires identical
dimensions across the `%05d` sequence, and `getDisplayMedia` returns the tab's
real dimensions — which differ per machine *and change when the user resizes the
window mid-session*. A stray size fails the encode several captures after the
resize that caused it, so it surfaces as a random ffmpeg fault far from its cause.

Normalization therefore happens **in the page, before the POST**, where the frame
is created and the invariant cannot be forgotten. The shell bakes its
`SHOT_WIDTH`/`SHOT_HEIGHT` into the injected plugin, so the page and the recorder
can never disagree — which is also what keeps `blank_preview`'s placeholder the
same size as a real frame.

**Contain-fit, not crop.** The tab is landscape and the canvas is portrait, so a
cover-fit crop would discard roughly 60% of the width, the app's left nav
included. The user clicked the button on the page they wanted recorded; silently
throwing most of it away is the worst available outcome. Stretching is worse
still — it squashes text by about 2.4×.

Letterbox bars are **white**, matching the `color=c=white` placeholder
`blank_preview` writes with ffmpeg, so the animation does not flash between
backgrounds.

The device pixel ratio is deliberately **not** applied: `getDisplayMedia` already
returns device pixels, so scaling by DPR again would yield 1200×1600 frames on
Retina and reintroduce the very variance this prevents.

600×800 remains the default. Now that it is an output canvas rather than a
viewport a landscape default would suit better, but changing it is a separate
decision with a migration cost: `blank_preview` and every recording already on
disk are 600×800, and mixing sizes re-breaks the invariant.

## Hiding the chrome — the shutter included

Apps scaffolded with `@agentic-react/vite` render an element-selector toolbar on
top of the page. It would appear in every frame, so the capture removes it two
ways before the shutter:

1. **`window.__AGENTIC_REACT__.hideToolkit()`** — the plugin's own runtime API,
   and the supported route. `exitSelectionMode()` is called too, so a session
   left in selection mode does not record highlight overlays.
2. **A CSS rule on the `data-agentic-react-*` attributes** — a fallback for a
   build whose API differs, covering every piece the toolkit injects: the
   launcher, dim layers, hover and selection labels, and the tuning modal.

Both calls are optional-chained and wrapped in `try`, so an app without the
plugin captures normally.

Those attributes are the only stable handle: the toolkit's elements have **no id
and no class**, and their `z-index: 2147482997` comes from a stylesheet rather
than an inline `style` attribute — an earlier `div[style*="2147482997"]` rule
matched nothing at all. Verified against a live app: the toolkit's corner drops
from 1417 distinct colours to 15 (flat background), with each mechanism working
independently of the other.

**The same rule carries `[data-trustable-shutter]`, so the button hides itself.**
It sits on the very page it is capturing; without this it is in every frame. The
rule uses `visibility:hidden` rather than `display:none` so geometry stays stable
and nothing reflows mid-capture.

**Everything hidden is restored in a `finally`.** Playwright never needed this —
it threw the whole browser away. Leaving a user's toolkit permanently invisible
because a capture threw is a bad failure mode.

### Waiting for the paint

Hiding is not instant, and the capture is reading a live video stream:

- a double `requestAnimationFrame` guarantees the style change has been through
  at least one composited frame;
- a further 350 ms covers the toolkit's own hide *transition* — it animates out
  rather than snapping. (The Playwright version used 300 ms for the same reason.)
  Do not shorten it: the failure it prevents, a half-faded toolkit baked into a
  frame, is silent.
- the frame is then taken via **`requestVideoFrameCallback`**, which fires only
  once a *new* frame has actually been presented. Without it a buffered pre-hide
  frame can be grabbed — an intermittent race that bakes the toolkit into an
  occasional frame and is very hard to diagnose. `ImageCapture.grabFrame()` is
  deliberately not used: it is Chromium-only and inconsistent on display tracks.

The order matters and is not arbitrary: **the chrome is hidden only after the
share is granted.** Hiding first would mean a denied prompt leaves the app
disfigured and the shutter itself invisible — unrecoverable without a reload.

# The shutter

The recorder **injects a Vite plugin into the app's `vite.config.ts`** when the
current app changes. The plugin does two things: it renders the button, and it
mounts the endpoint the button uploads to.

```ts
// >>> trustable-screenshot shutter — injected by screenshot.sh, removed on quit
const trustableScreenshotShutter = () => ({
  name: 'trustable-screenshot-shutter',
  apply: 'serve',
  configureServer(server) { /* mounts /__trustable_shot */ },
  transformIndexHtml() { /* injects the client script */ },
});
// <<< trustable-screenshot shutter
```

`transformIndexHtml` means the script is added when the page is **served** — the
app's `index.html` on disk is never modified. That matters because the starter
template regenerates that file, and because editing it would trigger a full Vite
reload on every capture.

Injection is idempotent (the begin marker is checked first) and only applies to a
config that actually has a `plugins: [` array. Removal is byte-exact: after an
inject/remove cycle the file's md5 is unchanged.

> The shell functions are still named `inject_reporter` / `remove_reporter` /
> `cleanup_reporter`, and the markers still use `REPORTER_BEGIN`/`REPORTER_END`.
> Those names are historical — this began as a route reporter — and were kept
> because the injection *mechanism* is unchanged. The factory name
> `trustableScreenshotShutter` appears in **both** the injection and
> `remove_reporter`'s replace string; if they ever drift, removal silently
> no-ops and leaves a call to an undefined plugin in the user's config. A guard
> in `screenshot_script_test.go` counts the occurrences for exactly this reason.

## The button

A `<button>` appended to `document.body`, `position:fixed`, **top right**, 36px,
carrying `data-trustable-shutter` and the id `__trustable_shutter__`.

- **Top right**, because the `@agentic-react` launcher is bottom-right and 58px —
  no geometric overlap.
- **`z-index: 2147483001`**, exactly one above that launcher's `2147483000`, so
  it can never be occluded. Deliberately *not* `2147483647`: squatting on
  `INT_MAX`, where browsers clamp, would make it un-overridable inside someone
  else's app.
- **Inline styles, never a stylesheet.** An injected `<style>` loses to the app's
  own CSS reset — a Tailwind-preflight rule resetting every button property would
  erase the button outright. Inline styles beat any selector short of
  `!important`.
- **`●` (U+25CF), not an emoji.** This renders in the *user's* browser, whose font
  coverage is unknown. The VM's emoji font is irrelevant now that the VM does not
  render app content.
- **No `data-agentic-react-*` attribute.** That namespace belongs to the toolkit;
  borrowing it would make the button vanish under hide logic we do not control.

The button is disabled for the duration of a capture, so a double-click cannot
start two `getDisplayMedia` calls — the second would throw `InvalidStateError`
and could fire its picker while the chrome is hidden. It also flashes its colour
and `title` for ~1.2 s after each attempt (`captured` / `cancelled` /
`capture failed`), because the terminal is not necessarily visible.

## Refusing a non-tab share

After the grant, the capture checks
`stream.getVideoTracks()[0].getSettings().displaySurface === 'browser'` and
refuses anything else.

**This is the only privacy control in the design.** The picker still allows
choosing a window or the whole screen; a full-screen share would capture whatever
else is on the user's display and POST it into a file the recorder then `git
add`s. The check cannot verify it is *this* tab, but it filters the case that
matters.

## The queue: IndexedDB, then the endpoint

The capture is stored **durably first and uploaded second**:

1. the normalized PNG is `put` into IndexedDB (db `trustable-shots`, store
   `frames`, `autoIncrement`);
2. `drain()` POSTs each pending record to the endpoint and deletes it on a 204.

A failed POST leaves the record in place for the next `drain()`, which also runs
at script load — so a frame captured moments before a reload, or while the dev
server was down, is recovered rather than lost. IndexedDB also holds `Blob`s
natively, avoiding a base64 round-trip that would inflate a 2 MB PNG to 2.7 MB of
string on the main thread.

**The IndexedDB connection is opened at load, never inside the click handler.**
`getDisplayMedia` requires transient user activation, and an `await` consumes it;
any await placed ahead of that call makes captures fail with `NotAllowedError` on
some machines and not others. For the same reason `getDisplayMedia` is the
literal first statement of the handler.

## The endpoint

`configureServer` mounts `/__trustable_shot` on the dev server:

| Method | Behaviour |
|---|---|
| `POST` | append the body to an in-memory queue; `204`. Bodies over 32 MB are dropped. |
| `GET` | **shift** the oldest frame and return it as `image/png`; `204` when empty. |
| other | `405` |

**The GET is destructive.** The recorder saves whatever it retrieves, so a frame
left in the queue would be saved again on the next collect — a recording of
duplicates. The queue is bounded at `MAX_FRAMES` (16), so clicking while the
recorder is not polling cannot grow the dev server's heap; the shell's drain loop
is capped to the same number so it cannot spin.

The middleware is mounted **synchronously in the `configureServer` body, not by
returning a function.** A returned function installs after Vite's own
middlewares, and the SPA fallback would then answer `/__trustable_shot` with
`index.html`. That is the difference between a working endpoint and one that
returns HTML to `curl` — and it is why `fetch_frame` verifies the PNG magic
number `89504e470d0a1a0a` before accepting a frame. Without that check the HTML
would be saved as a `.png` and ffmpeg would fail on a later `regenerate`, far
from the cause.

The POST is **same-origin and uses a relative path**. Both matter: a `POST` with
`Content-Type: image/png` is not a CORS-simple request, so cross-origin it would
trigger an `OPTIONS` preflight the middleware does not answer. A relative path
also keeps working when the app is reached through the `vite.<domain>` ingress,
which a hard-coded absolute URL would not.

## Why not MCP

The obvious alternative was to read the frame back over the app's MCP server,
which the route reporter already used. It does not work:

- **`get-html-elements` truncates `domPreview` at 800 characters** (silently, with
  no ellipsis). A PNG is 100 KB–1 MB, so it could never come back that way.
- The only untruncated channel is a **custom tool**, which would require adding a
  `zod` import and a `customTools` entry to the user's `vite.config`, works only
  on `@agentic-react` apps, is bounded by a 10-second bridge timeout, and
  broadcasts to every open tab — picking the winner by *connection order*, not
  by which tab is focused.

A dev-server middleware has none of those limits and works for **any** Vite app.

## Readiness

`wait_for_shutter` polls both halves, because `transformIndexHtml` and
`configureServer` apply at different points in Vite's lifecycle: the served HTML
can carry the client script while the middleware is not yet mounted. It checks
that the HTML contains the button id **and** that the endpoint answers `204`.

A missed config reload now means *no button at all*, where it previously degraded
to capturing `/` — so the warning tells the user to **reload the app tab**. Even
after Vite re-reads the config, an already-open tab still holds the old HTML.
This is also why the `sleep 1; touch "$config"` reload nudge matters more than it
used to.

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

**ffmpeg is the only binary this needs**, installed via the `APT_MISSING` array
idiom from `setup.sh`: probed with `command -v ffmpeg`, installed with
`sudo apt-get install -y ffmpeg`. The VM guest has passwordless sudo. The
installer is idempotent and runs on every invocation.

Two dependencies were **removed** when the page took over capturing, and should
not come back:

- **Playwright + Chromium.** The VM no longer rasterizes app content at all, so a
  browser install here would gate every screenshot behind a ~150 MB download for
  a code path that no longer exists. (`@playwright/test` stays in the repo's
  `package.json` — the e2e suite under `tests/` still uses it. It is only the
  *recorder* that no longer needs it.)
- **`fonts-noto-color-emoji`.** It existed because headless Chromium rendered the
  app with the VM's fonts, and a bare VM has none carrying emoji glyphs
  (measured: 117 fonts installed, zero with emoji, and 🎉 ✅ 🚀 captured as empty
  boxes). Rendering now happens in the user's own browser on the host, which
  brings its own fonts.

`TestScreenshotScriptNeedsNoBrowser` fails the build if either creeps back in.
Neither ever belonged in `setup.sh` either: `setup_test.go` fails the Go tests if
that file contains `playwright` or `chromium`.

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

**The preview file always exists.** It is published as soon as an app becomes
current — before any capture — and is blank (a white frame at the capture size,
generated with the ffmpeg already required) when that app has no frames yet.
Without this the editor would show a missing file, or worse, keep displaying the
*previous* app's recording after a switch.

Deleting the last frame blanks the preview rather than removing it, so an editor
tab open on the file keeps working instead of breaking.

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
animation. Deleting the last remaining frame removes the app's `screenshot.png`
rather than writing a zero-frame animation, and **blanks** the root preview copy
so it cannot keep showing frames that no longer exist.

To reset a recording completely, `rm -rf screenshot screenshot.png` in the app
folder and commit.

# Environment

| Variable | Meaning |
|---|---|
| `TRUSTABLE_SCREENSHOT_URL` | app origin, default `http://localhost:5173` |
| `TRUSTABLE_SCREENSHOT_WIDTH` / `_HEIGHT` | output canvas, default `600` / `800` |

# What can go wrong

**The picker appears on every click.** Two extra interactions per frame, so a
five-frame recording costs ten. `preferCurrentTab: true` reduces it to a single
confirm in Chromium and must not be dropped.

**The prompt is denied.** `getDisplayMedia` rejects with `NotAllowedError`, the
button flashes `cancelled`, and the `finally` restores everything. Because the
chrome is hidden only *after* the grant, a denial leaves nothing to repair.

**The wrong surface is shared.** Refused by the `displaySurface` check above.

**Several tabs are open on the app.** Each gets its own button and each POSTs to
the same queue, so frames arrive in click order regardless of which tab produced
them. A stale background tab is harmless unless clicked — and it cannot be
clicked without being focused first.

**The dev server restarts.** The in-memory queue is lost by design. Frames still
in IndexedDB (not yet acknowledged with a 204) survive and are re-POSTed on the
next `drain()`; a frame already acknowledged but not yet collected is gone.
Pressing Enter promptly after clicking is the practical mitigation.

**HMR runs.** `transformIndexHtml` is not re-run for a hot update, and the button
— a `document.body` child — survives it. A *full* reload does re-run the script,
which is why it guards on `window.__trustableShutter` before mounting a second
button, and why `drain()` runs at load.

**The app has no `plugins: [` array.** No injection, so no button. The recorder
says so on startup and the loop still manages existing frames.
