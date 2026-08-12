# Screenshot recorder

`screenshot.sh` records what the launched application looks like over time. It is
an interactive loop: press Enter to capture a frame, Space to redraw the current
app's animation, Backspace/Delete to drop the last frame, `q` to quit. Every
change rewrites an animated PNG and an animated GIF, and the GIF is drawn inline
in the terminal when it supports it.

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
  screenshot.gif            animated GIF, same frames and timing
```

Frames are individual files named `YYYYMMDD-HHMMSS.png` from `date -u`, so they
sort chronologically as plain text and `sorted()` is the frame order. Two
captures inside the same second get a `-N` suffix rather than overwriting.

Both animations are **regenerated from the directory** after every change rather
than appended in place. That is what makes Delete a one-liner — remove the newest
file and rebuild — instead of APNG chunk surgery with sequence renumbering, and
it keeps the two outputs always consistent with the frames on disk.

Nothing in the managed `.gitignore` block (`trustableGitignoreEntries` in
`gitignore.go`, spec [13-gitignore.md](13-gitignore.md)) matches `*.png`, `*.gif`,
or `screenshot/`, so all of it is tracked and committed normally.

# ImageMagick cannot write APNG — do not "simplify" this back to convert

This is the least obvious constraint in the tool, so it is recorded here.

ImageMagick 6.9 (what Ubuntu 24.04 ships) has **no APNG encoder and no apng
delegate**. `convert … apng:out.png` silently shells out to `ffmpeg`:

- with ffmpeg absent it writes a **0-byte file** and reports success;
- with ffmpeg present it round-trips through a 25fps video encoder, so three
  one-second frames come back as **99 frames of 1/25s each** and `-delay 100` is
  discarded entirely.

Neither can satisfy the one-second-per-frame requirement. Pillow (`python3-pil`)
writes both formats correctly — every APNG `fcTL` delay is exactly `1000/1000`,
and the GIF carries `duration=1000, loop=0` — so **Pillow is the only image
dependency** and ImageMagick is not installed at all.

`screenshot_script_test.go` asserts the string `apng:` never appears in the
script, so a future simplification back to `convert` fails the tests rather than
silently producing a broken file.

# Identical frames must not be merged

Pillow collapses identical consecutive frames into a single frame with a summed
delay. Capturing an app twice before changing anything — an entirely normal thing
to do — would otherwise produce one frame of two seconds instead of two frames of
one, and the animation would disagree with the files on disk.

`disposal=1, blend=0` on the APNG save prevents this, and is therefore
load-bearing rather than cosmetic. Verified: three pixel-identical captures
produce a three-frame APNG with all delays at `1000/1000`.

The GIF encoder merges identical consecutive frames regardless of the options
given (`disposal`, `optimize` and palette conversion all make no difference), so
a run of unchanged captures shows there as one longer frame. `screenshot.png` is
the exact record; `screenshot.gif` is the convenience copy for the terminal
preview.

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
ENTER capture · SPACE show · DEL remove · q quit  [myapp: 3 frames]
```

| Key | Action |
|---|---|
| Enter | capture a frame of the running app |
| Space | redraw the current app's animation — captures nothing |
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
Space, and its own recording is rebuilt and drawn — so it is easy to see what has
already been captured for whichever app is now current. Switching away and back
continues that app's recording where it left off.

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

**The viewport is fixed at 300×400 and `fullPage` is false.** Both matter: every
frame in an animation must share the same dimensions, and `fullPage: true` varies
with page content. Note 300×400 is portrait and phone-width, so responsive apps
render their mobile layout — that is a real rendering mode, not a defect. Getting
a desktop layout at a small size means capturing larger and downscaling, because
the viewport changes what the app renders, not just the image size.

# Installation on demand

Both installers are idempotent and run on every invocation:

- **Pillow** via the `APT_MISSING` array idiom from `setup.sh`: probed with
  `python3 -c 'import PIL'` (so a venv Pillow counts), installed with
  `sudo apt-get install -y python3-pil`. The VM guest has passwordless sudo.
- **Playwright + Chromium** following `tests/e2e_issue98.sh`: `npm install` when
  `node_modules/@playwright/test` is absent, then `npx playwright install
  chromium`, which no-ops when the pinned revision is already cached. Skip with
  `TRUSTABLE_SCREENSHOT_SKIP_BROWSER_INSTALL=1`.

`npx playwright install-deps` is **not** run automatically — it is a large
unattended `sudo apt-get` of system libraries. If Chromium fails to launch on a
bare VM, run `npx playwright install-deps chromium` by hand.

None of this belongs in `setup.sh`: `setup_test.go` fails the Go tests if that
file contains `playwright` or `chromium`.

# Terminal preview

After each regeneration the GIF is emitted with the iTerm2 inline-image protocol:

```
ESC ] 1337 ; File=inline=1;width=20;preserveAspectRatio=1 : <base64> BEL
```

The terminal is probed with `LC_TERMINAL` first, then `TERM_PROGRAM`.
`LC_TERMINAL` is the one that matters here: it is forwarded over SSH, and this
tool is normally reached through `./ssh.sh`, where `TERM_PROGRAM` does not
propagate.

Any other terminal gets the file path and frame count printed instead. Emitting
the escape sequence unconditionally would dump kilobytes of base64 into VS Code,
tmux, and plain Terminal.

# Git

Every change — each capture and each delete — is committed to the app's checkout.
Committing per change rather than at quit is deliberate: uncommitted frames are
destroyed by the `git clean -fd` that Revert performs (see
[13-gitignore.md](13-gitignore.md)), so anything left uncommitted is one Revert
away from being lost.

Identity comes from `GIT_USER`/`GIT_EMAIL` in `.env`, falling back to
`Trustable` / `trustable@localhost`, and is set only when not already configured
— the same contract as `ensureGitIdentity` in `git.go`.

The `add` and `commit` are **pathspec-scoped** to `screenshot`, `screenshot.png`,
and `screenshot.gif`. The workbench is a live user checkout with arbitrary dirty
state; a bare `git commit -a` would sweep the user's work-in-progress into a
screenshot commit. `git add` on the directory also stages a removed frame as a
deletion. A `diff --cached --quiet` guard skips the commit when nothing changed,
which would otherwise abort the script under `set -e`.

The tool never pushes. Publishing goes through the licensed `/api/publish` path.

# Deleting

Backspace/Delete removes the newest file in `screenshot/` and regenerates both
animations. Deleting the last remaining frame removes `screenshot.png` and
`screenshot.gif` entirely rather than writing a zero-frame animation.

To reset a recording completely, `rm -rf screenshot screenshot.png screenshot.gif`
in the app folder and commit.

# Environment

| Variable | Meaning |
|---|---|
| `TRUSTABLE_SCREENSHOT_URL` | capture target, default `http://localhost:5173` |
| `TRUSTABLE_SCREENSHOT_WIDTH` / `_HEIGHT` | viewport, default `300` / `400` |
| `TRUSTABLE_SCREENSHOT_SKIP_BROWSER_INSTALL` | `1` skips `npx playwright install chromium` |
