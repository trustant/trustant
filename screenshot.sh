#!/bin/bash
# Copyright 2025-2026 Nuvolaris Inc
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.

#
# screenshot.sh — record the launched app INSIDE the trudev VM (spec/16-screenshot.md).
#
# The app captures itself: a shutter button injected into the running app takes
# the frame, and this loop collects it. ENTER collects, SPACE refreshes the
# preview, DEL/BACKSPACE drops the last frame, q quits. Every change rebuilds the
# app's screenshot.png (an animated PNG, one second per frame) from the frames on
# disk and copies it next to this script so it can be previewed in the editor.
#
# Frames live in $WORKBENCH_DIR/<current>/screenshot/<timestamp>.png. The current
# app is re-read every iteration, so launching a different app mid-session moves
# the recording to that app's folder.
#
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

ok()   { echo -e "${GREEN}✓ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; exit 1; }

cd "$(dirname "$0")"
ROOT="$PWD"

# --- 1. Run inside the VM only ---
#
# Same two-stage guard as setup.sh. Deliberately not a Lima-specific probe:
# "the VM" here also covers native Ubuntu development and WSL2, both of which
# start.sh supports, and a hostname or /mnt/lima-* check would break them.
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
[[ "$OS" == "linux" ]] \
  || fail "screenshot.sh runs inside the VM (Ubuntu natively, in Lima, or in WSL), not ${OS} — try ./ssh.sh ./screenshot.sh"
[[ -r /etc/os-release ]] || fail "cannot identify the Linux distribution: /etc/os-release is missing"
# shellcheck disable=SC1091
source /etc/os-release
case " ${ID:-} ${ID_LIKE:-} " in
  *ubuntu*|*debian*) ;;
  *) fail "unsupported Linux distribution: ${PRETTY_NAME:-${ID:-unknown}} (expected Ubuntu/Debian)" ;;
esac

# --- 2. Environment ---
#
# The recorder is a keyboard loop, so it needs a terminal on stdin. Without one
# every read hits EOF immediately and the loop exits after drawing its prompt
# once — which looks like a crash, and is the confusing half of running this
# through a pipe or a non-interactive ssh. Refuse up front, before anything is
# injected into the user's config, and name the fix.
[[ -t 0 ]] || fail "screenshot.sh needs an interactive terminal — run it in a shell (./ssh.sh, then ./screenshot.sh), not piped or through 'ssh <host> ./screenshot.sh'"

[[ -f .env ]] || fail ".env not found — run ./setup.sh first"
# shellcheck disable=SC1091
source ./.env
[[ -n "${WORKBENCH_DIR:-}" ]] || fail "WORKBENCH_DIR is not set in .env"
WORKBENCH_DIR="$(eval echo "$WORKBENCH_DIR")"
[[ -d "$WORKBENCH_DIR" ]] || fail "WORKBENCH_DIR does not exist: $WORKBENCH_DIR"

URL="${TRUSTANT_SCREENSHOT_URL:-http://localhost:5173}"

# The output canvas, baked into the injected plugin so the page and this script
# can never disagree — a blank placeholder must match a real frame exactly, or
# ffmpeg refuses to encode the sequence.
SHOT_WIDTH="${TRUSTANT_SCREENSHOT_WIDTH:-600}"
SHOT_HEIGHT="${TRUSTANT_SCREENSHOT_HEIGHT:-800}"

# --- 3. Install what is missing ---
#
# ffmpeg is the only binary this needs. It is the APNG encoder, and it is
# installed directly rather than reached through ImageMagick: IM 6.9 has no APNG
# encoder of its own and delegates `apng:` to ffmpeg, writing a 0-byte file when
# ffmpeg is absent and re-timing every frame at 25fps when it is present, which
# destroys the one-second delay.
#
# There is deliberately no browser and no font here. Both were needed while the
# VM rasterized the app with headless Chromium; the page now captures itself in
# the user's own browser, so the VM never renders app content and a ~150MB
# Chromium download would gate every screenshot on a code path that is gone.
APT_MISSING=()
command -v ffmpeg &>/dev/null || APT_MISSING+=(ffmpeg)

if [[ ${#APT_MISSING[@]} -gt 0 ]]; then
  warn "installing missing apt packages: ${APT_MISSING[*]}"
  sudo apt-get update -qq || fail "apt-get update failed"
  sudo apt-get install -y "${APT_MISSING[@]}" || fail "apt-get install ${APT_MISSING[*]} failed"
fi

# --- 4. Resolve the current app ---
#
# launch.go's writeCurrentApp rewrites $WORKBENCH_DIR/current on every launch, so
# re-reading it each iteration is what lets the loop follow an app switch.
APP=""
APP_DIR=""
SHOT_DIR=""

resolve_app() {
  local name
  # The redirect is guarded inside the subshell: `< missing-file` fails in the
  # shell itself, so redirecting only tr's stderr lets the error reach the
  # terminal when no app has ever been launched.
  name="$({ tr -d '[:space:]' < "$WORKBENCH_DIR/current"; } 2>/dev/null || true)"
  # An empty `current` is a real state, not a missing one: joining it onto
  # WORKBENCH_DIR would silently address the workbench root.
  [[ -n "$name" ]] || return 1
  [[ -d "$WORKBENCH_DIR/$name" ]] || return 1
  APP="$name"
  APP_DIR="$WORKBENCH_DIR/$APP"
  SHOT_DIR="$APP_DIR/screenshot"
  return 0
}

frame_count() {
  [[ -d "$SHOT_DIR" ]] || { echo 0; return; }
  find "$SHOT_DIR" -maxdepth 1 -name '*.png' -type f | wc -l | tr -d ' '
}

# --- 4b. The shutter the user clicks ---
#
# The recorder cannot reproduce the user's tab. Driving a fresh headless browser
# at the app reproduces the *route* but not the *state*: form input, open modals,
# scroll position, and anything the app keeps in tab storage (auth included) are
# all absent. The recording showed a page the user had never seen.
#
# So the page captures itself. A Vite plugin injected into the app's own
# vite.config.ts renders a button, and the click captures the live tab with
# getDisplayMedia. The plugin uses transformIndexHtml, so the script is added
# when the page is *served* — the app's index.html on disk is never touched,
# which matters because the starter template regenerates that file.
#
# The same plugin mounts an upload endpoint on the dev server; the page POSTs the
# PNG there and this script collects it with plain curl. A dev-server middleware
# rather than the app's MCP server because MCP's get-html-elements truncates its
# reply at 800 characters — an image could never come back that way — and because
# a middleware works for any Vite app, not just @agentic-react ones.
#
# The REPORTER_* names are historical: this began as a route reporter. The
# injection *mechanism* is unchanged, so the names stayed to keep that contract
# (and its guards) stable.
REPORTER_BEGIN="// >>> trustant-screenshot shutter — injected by screenshot.sh, removed on quit"
REPORTER_END="// <<< trustant-screenshot shutter"
SHUTTER_ID="__trustant_shutter__"
SHOT_ENDPOINT="/__trustant_shot"
SHOT_URL="${URL%/}$SHOT_ENDPOINT"

# Set by inject_reporter: 1 once the running dev server actually serves the
# shutter. Declared here so a caller reading it after an injection that bailed
# out early sees "not served" rather than a stale 1 from a previous app.
SHUTTER_SERVED=0

# Files whose changes are hidden while the recorder runs. vite.config.ts carries
# the injection; .gitignore is included because the recorder may add the preview
# copy to it. Both are tracked, and .gitignore has no effect on a tracked file —
# git update-index --skip-worktree is the only mechanism that hides them, and it
# is strictly per-file, so the user's own edits stay visible and committable.
HIDDEN_FILES=(vite.config.ts .gitignore)

vite_config_path() {
  local name
  for name in vite.config.ts vite.config.js; do
    [[ -f "$APP_DIR/$name" ]] && { echo "$APP_DIR/$name"; return 0; }
  done
  return 1
}

hide_file() {
  local relative="$1"
  [[ -d "$APP_DIR/.git" ]] || return 0
  git -C "$APP_DIR" ls-files --error-unmatch "$relative" &>/dev/null || return 0
  git -C "$APP_DIR" update-index --skip-worktree "$relative" 2>/dev/null || true
}

unhide_file() {
  local relative="$1"
  [[ -d "$APP_DIR/.git" ]] || return 0
  git -C "$APP_DIR" ls-files --error-unmatch "$relative" &>/dev/null || return 0
  git -C "$APP_DIR" update-index --no-skip-worktree "$relative" 2>/dev/null || true
}

# Clearing the flags first matters: a previous run killed before its cleanup
# leaves them set, and git then silently refuses to update those files.
unhide_all() {
  local f
  for f in "${HIDDEN_FILES[@]}"; do unhide_file "$f"; done
}

inject_reporter() {
  local config
  # Reset up front, so this always describes the current call. The early return
  # below leaves it 0 rather than a stale 1 from a previous app.
  SHUTTER_SERVED=0
  config="$(vite_config_path)" || return 1
  # Already injected: nothing to write, but still confirm the server serves it,
  # or the caller would be told "not served" without anyone having looked.
  if grep -qF "$REPORTER_BEGIN" "$config"; then
    wait_for_shutter && SHUTTER_SERVED=1
    return 0
  fi

  # A missing plugins array is not a reason to give up: the starter templates
  # generate configs without one (and in the function form,
  # `defineConfig(({ mode }) => ({ ... }))`, where there is no object literal to
  # match either). The rewriter below adds `plugins: []` when it has to, so those
  # apps get a shutter too. It still declines a config whose config object it
  # cannot locate at all — see the failure paths in the Python block.
  unhide_all
  python3 - "$config" "$REPORTER_BEGIN" "$REPORTER_END" "$SHUTTER_ID" \
           "$SHOT_ENDPOINT" "$SHOT_WIDTH" "$SHOT_HEIGHT" "$URL" <<'PY' || return 1
import re, sys
path, begin, end, marker = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
endpoint, width, height = sys.argv[5], sys.argv[6], sys.argv[7]
# The origin the recorder polls, so the redirect target below can never drift
# from the port this script actually collects frames from.
capture_origin = sys.argv[8].rstrip('/')
source = open(path).read()

# The client script is assembled with single quotes and concatenation, never
# backticks: it lives inside a JS template literal inside this f-string, and a
# stray backtick would end the literal early.
#
# No line inside this payload may start with '#'. screenshot_script_test.go
# strips comment lines before asserting, and would strip such a line too.
plugin = f"""{begin}
const trustantScreenshotShutter = () => {{
  // Newest last, in memory only: the recorder drains this over HTTP, and a dev
  // server restart is a new recording session anyway. Bounded, so a user who
  // clicks ten times while the recorder is not polling cannot grow the heap.
  const frames = [];
  const MAX_FRAMES = 16;

  return {{
    name: 'trustant-screenshot-shutter',
    apply: 'serve',

    // Mounted synchronously, NOT by returning a function. A returned function
    // installs the middleware *after* Vite's own, and the SPA fallback would
    // then answer this path with index.html before we ever see the request.
    configureServer(server) {{
      server.middlewares.use('{endpoint}', (req, res) => {{
        if (req.method === 'POST') {{
          const chunks = [];
          let size = 0;
          req.on('data', (c) => {{
            size += c.length;
            if (size > 32 * 1024 * 1024) {{ req.destroy(); return; }}
            chunks.push(c);
          }});
          req.on('end', () => {{
            if (!chunks.length) {{ res.statusCode = 400; res.end('empty'); return; }}
            frames.push(Buffer.concat(chunks));
            while (frames.length > MAX_FRAMES) frames.shift();
            res.statusCode = 204;
            res.end();
          }});
          return;
        }}
        if (req.method === 'GET') {{
          // Destructive read: the recorder saves whatever it retrieves, so a
          // frame left in the queue would be saved again on the next collect —
          // a recording of duplicates.
          const frame = frames.shift();
          if (!frame) {{ res.statusCode = 204; res.end(); return; }}
          res.setHeader('Content-Type', 'image/png');
          res.setHeader('Content-Length', String(frame.length));
          res.end(frame);
          return;
        }}
        res.statusCode = 405;
        res.end();
      }});
    }},

    transformIndexHtml() {{
      return [{{
        tag: 'script',
        injectTo: 'head-prepend',
        attrs: {{ type: 'text/javascript' }},
        children: [
          '(function () {{',
          // A full page reload re-runs this script; the button must not stack.
          '  if (window.__trustantShutter) return;',
          '  window.__trustantShutter = true;',
          '  var W = {width}, H = {height};',
          '  var ENDPOINT = "{endpoint}";',

          // --- IndexedDB queue ---
          //
          // Frames outlive HMR, a reload, and a dev server that is not up yet.
          // Blobs are stored natively, so there is no base64 round-trip. The
          // connection is opened HERE, at load — never inside the click
          // handler, where an await before getDisplayMedia would consume the
          // transient user activation the call requires.
          '  var dbp = new Promise(function (resolve, reject) {{',
          '    var rq = indexedDB.open("trustant-shots", 1);',
          '    rq.onupgradeneeded = function () {{',
          '      rq.result.createObjectStore("frames", {{ keyPath: "id", autoIncrement: true }});',
          '    }};',
          '    rq.onsuccess = function () {{ resolve(rq.result); }};',
          '    rq.onerror = function () {{ reject(rq.error); }};',
          '  }});',
          '  function tx(mode, fn) {{',
          '    return dbp.then(function (db) {{',
          '      return new Promise(function (resolve, reject) {{',
          '        var t = db.transaction("frames", mode);',
          '        var out = fn(t.objectStore("frames"));',
          '        t.oncomplete = function () {{ resolve(out && out.result); }};',
          '        t.onerror = function () {{ reject(t.error); }};',
          '      }});',
          '    }});',
          '  }}',
          '  function dbPut(blob) {{ return tx("readwrite", function (s) {{ return s.add({{ blob: blob, at: Date.now() }}); }}); }}',
          '  function dbAll() {{ return tx("readonly", function (s) {{ return s.getAll(); }}); }}',
          '  function dbDelete(id) {{ return tx("readwrite", function (s) {{ return s.delete(id); }}); }}',

          // Relative path, never absolute: same-origin keeps the POST free of a
          // CORS preflight (image/png is not a simple content type) and keeps
          // working through the vite.<domain> ingress.
          '  async function drain() {{',
          '    var pending = await dbAll();',
          '    for (var i = 0; i < pending.length; i++) {{',
          '      try {{',
          '        var res = await fetch(ENDPOINT, {{ method: "POST", headers: {{ "Content-Type": "image/png" }}, body: pending[i].blob }});',
          '        if (!res.ok) break;',
          '        await dbDelete(pending[i].id);',
          '      }} catch (e) {{ break; }}',
          '    }}',
          '  }}',

          // --- The button ---
          //
          // Top-right, because the @agentic-react launcher sits bottom-right at
          // z-index 2147483000 as a plain body child with no shadow DOM. One
          // above it, not INT_MAX: squatting on the ceiling would make this
          // un-overridable inside someone else's app.
          //
          // z-index alone is not enough. A <dialog open> or any element with
          // popover renders in the browser's TOP LAYER, which paints above the
          // whole z-index stack — no value, 2147483001 or INT_MAX, can climb
          // over it. So the button is a manual popover itself: that puts it in
          // the top layer too, where it stacks above earlier top-layer entries
          // and stays clickable over the app's modals. The z-index still
          // matters for the ordinary-stacking case (a browser without popover
          // support, where showPopover throws and the button remains a plain
          // fixed child).
          //
          // Styles are inline, not a stylesheet: an injected <style> loses to
          // the app's own reset, and a Tailwind-preflight rule resetting every
          // button property would erase this button entirely.
          '  var btn = document.createElement("button");',
          '  btn.id = "{marker}";',
          '  btn.setAttribute("data-trustant-shutter", "");',
          '  btn.type = "button";',
          '  btn.title = "Capture a screenshot frame";',
          // A geometric glyph, not an emoji: this renders in the USER's browser,
          // whose font coverage is unknown. The VM's emoji font is irrelevant here.
          '  btn.textContent = "\\u25CF";',
          // "inset:auto;margin:0" must precede top/right: the UA stylesheet for
          // [popover] sets inset:0 and centering margins, and inset is a
          // shorthand for top/right/bottom/left — declared after them in the
          // same block it would wipe the corner placement.
          '  btn.style.cssText = "position:fixed;inset:auto;margin:0;top:12px;right:12px;"',
          '    + "width:36px;height:36px;overflow:visible;"',
          '    + "border-radius:50%;border:1px solid rgba(0,0,0,.2);background:#ffffff;color:#dd3333;"',
          '    + "font:16px/1 system-ui,sans-serif;cursor:pointer;padding:0;z-index:2147483001;"',
          '    + "box-shadow:0 1px 4px rgba(0,0,0,.3)";',
          // A manual popover, not auto: an auto popover light-dismisses on any
          // outside click, so the first click anywhere in the app would close
          // the shutter. Manual only closes when asked.
          //
          // "margin:0" is set with the inline styles above because the UA
          // stylesheet gives [popover] centering margins that would drag the
          // button out of the corner.
          '  function raise() {{',
          '    try {{',
          '      if (btn.isConnected && btn.popover && !btn.matches(":popover-open")) btn.showPopover();',
          '    }} catch (e) {{ /* no popover support: the z-index path stands */ }}',
          '  }}',
          '  function mount() {{',
          '    if (!document.body) return;',
          '    document.body.appendChild(btn);',
          '    if ("popover" in btn) btn.popover = "manual";',
          '    raise();',
          '  }}',
          '  if (document.body) mount(); else addEventListener("DOMContentLoaded", mount);',
          // Top-layer order is entry order, so a dialog opened AFTER the button
          // paints above it. Re-entering the top layer on each new dialog/popover
          // makes the button the newest entry again. Observing is cheaper and
          // more reliable than guessing which frameworks open modals how.
          '  if (typeof MutationObserver === "function") {{',
          '    new MutationObserver(function () {{',
          '      if (document.querySelector("dialog[open],[popover]:popover-open:not([data-trustant-shutter])")) {{',
          '        try {{ if (btn.matches(":popover-open")) btn.hidePopover(); }} catch (e) {{}}',
          '        raise();',
          '      }}',
          '    }}).observe(document.documentElement, {{',
          '      subtree: true, childList: true, attributes: true, attributeFilter: ["open", "popover"],',
          '    }});',
          '  }}',

          '  function flash(text, good) {{',
          '    btn.title = text;',
          '    btn.style.color = good ? "#22aa22" : "#dd3333";',
          '    setTimeout(function () {{ btn.style.color = "#dd3333"; btn.title = "Capture a screenshot frame"; }}, 1200);',
          '  }}',

          // --- Hiding the chrome, this button included ---
          //
          // The button sits on the page it is capturing, so without hiding
          // itself it lands in every frame. visibility:hidden rather than
          // display:none keeps geometry stable, so nothing reflows mid-capture.
          '  function hideChrome() {{',
          '    var style = document.createElement("style");',
          '    style.textContent = "[data-agentic-react-dim],[data-agentic-react-toolkit],"',
          '      + "[data-agentic-react-launcher],[data-agentic-react-hover],"',
          '      + "[data-agentic-react-hover-label],[data-agentic-react-selected],"',
          '      + "[data-agentic-react-selected-label],[data-agentic-react-selected-actions],"',
          '      + "[data-agentic-react-clear-all],[data-agentic-react-tuning-modal],"',
          '      + "[data-agentic-react-tuning-surface],[data-agentic-react-tuning-panel],"',
          '      + "[data-trustant-shutter]{{visibility:hidden !important;}}";',
          '    document.head.appendChild(style);',
          // The visibility rule above already covers the button, top layer or
          // not. Leaving the top layer as well is belt-and-braces: a popover
          // still in the top layer keeps a compositing surface over the page,
          // and the app's own modal must stay the frontmost thing in the frame.
          '    try {{ if (btn.matches(":popover-open")) btn.hidePopover(); }} catch (e) {{}}',
          '    try {{',
          '      globalThis.__AGENTIC_REACT__?.hideToolkit?.();',
          '      globalThis.__AGENTIC_REACT__?.exitSelectionMode?.();',
          '    }} catch (e) {{ /* app without the plugin: nothing to hide */ }}',
          '    return style;',
          '  }}',
          // Restoring is mandatory, unlike the Playwright version which threw the
          // whole browser away. A capture that throws must not leave the user's
          // toolkit invisible.
          '  function showChrome(style) {{',
          '    if (style && style.parentNode) style.parentNode.removeChild(style);',
          // Back into the top layer, as the newest entry — so the button is
          // clickable again even though the app's modal is still open.
          '    raise();',
          '    try {{ globalThis.__AGENTIC_REACT__?.showToolkit?.(); }} catch (e) {{}}',
          '  }}',

          // requestVideoFrameCallback fires only once a NEW frame has been
          // presented — the only reliable signal that the post-hide paint
          // reached the stream. Without it a buffered pre-hide frame can be
          // grabbed, baking the toolkit into an occasional frame.
          '  async function grabFrame(stream) {{',
          '    var video = document.createElement("video");',
          '    video.srcObject = stream;',
          '    video.muted = true;',
          '    video.playsInline = true;',
          '    await video.play();',
          '    if (video.requestVideoFrameCallback) {{',
          '      await new Promise(function (r) {{ video.requestVideoFrameCallback(function () {{ r(); }}); }});',
          '    }} else {{',
          '      await new Promise(function (r) {{ setTimeout(r, 100); }});',
          '    }}',
          '    var bmp = await createImageBitmap(video);',
          '    video.srcObject = null;',
          '    return bmp;',
          '  }}',

          // --- Fixed output size: the load-bearing invariant ---
          //
          // ffmpeg's APNG encoder needs every frame in the sequence to share
          // dimensions. getDisplayMedia returns the tab's real size, which
          // differs per machine AND changes when the window is resized
          // mid-session, so the frame is fitted onto a fixed canvas here, at
          // the point of creation.
          //
          // The fit is WIDTH-first, cropping the bottom. An earlier version
          // contain-fit the frame, which letterboxed every landscape tab: at
          // 1512x832 onto a 600x800 portrait canvas the image occupies barely a
          // third of the height and the rest is band. Fitting the width instead
          // keeps the layout at full scale and simply ends the frame lower down
          // the page.
          //
          // The fill still matters for the opposite case — a tab WIDER in ratio
          // than the canvas leaves space under the image — and it is the page's
          // own background colour, not #ffffff, so that leftover blends into a
          // dark app instead of glaring. ffmpeg's blank placeholder is white,
          // but that only ever shows before the first frame exists.
          //
          // The pixel ratio is deliberately not applied: getDisplayMedia already
          // returns device pixels, so scaling by DPR again would give 1200x1600
          // frames on Retina and reintroduce the variance.
          '  function pageBackground() {{',
          '    try {{',
          '      var els = [document.body, document.documentElement];',
          '      for (var i = 0; i < els.length; i++) {{',
          '        if (!els[i]) continue;',
          '        var c = getComputedStyle(els[i]).backgroundColor;',
          // Skip transparent: it says nothing about what the user actually sees.
          '        if (c && c !== "transparent" && !/rgba\\(0,\\s*0,\\s*0,\\s*0\\)/.test(c)) return c;',
          '      }}',
          '    }} catch (e) {{}}',
          '    return "#ffffff";',
          '  }}',
          '  function normalize(bitmap) {{',
          '    var canvas = document.createElement("canvas");',
          '    canvas.width = W;',
          '    canvas.height = H;',
          '    var ctx = canvas.getContext("2d");',
          '    ctx.fillStyle = pageBackground();',
          '    ctx.fillRect(0, 0, W, H);',
          // Fit the WIDTH, always. The width is what carries the layout — the
          // left nav, the content column, the right rail — so it is scaled to
          // the canvas exactly and never cropped. Whatever height that implies
          // is then taken from the TOP: a tab that is too tall loses its
          // bottom, which is the part below the fold the user was not looking
          // at anyway.
          //
          // This replaces a contain-fit, which letterboxed every landscape tab
          // with thick bands. Cropping the bottom is not a compromise here: it
          // is what "record the page" means at a portrait canvas.
          '    var scale = W / bitmap.width;',
          '    var w = W;',
          '    var h = Math.round(bitmap.height * scale);',
          '    ctx.imageSmoothingEnabled = true;',
          '    ctx.imageSmoothingQuality = "high";',
          // Anchored top-left, not centred: centring a too-tall frame would cut
          // the header off as well as the footer, and the header is the part
          // that identifies the page.
          '    ctx.drawImage(bitmap, 0, 0, w, h);',
          '    return new Promise(function (resolve, reject) {{',
          '      canvas.toBlob(function (b) {{ b ? resolve(b) : reject(new Error("toBlob")); }}, "image/png");',
          '    }});',
          '  }}',

          '  async function capture() {{',
          '    btn.disabled = true;',
          '    var stream = null, style = null;',
          '    try {{',
          // Screen capture exists only in a secure context. Opening the app
          // through the Trustant UI puts it on http://vite.<ip>.nip.io, which
          // is plain HTTP and not localhost, so navigator.mediaDevices is
          // undefined and the click could only throw a TypeError the catch
          // below turns into a 1.2s tooltip — the "shutter does nothing"
          // report.
          //
          // So move the user instead of just telling them: open the same route
          // on the origin the recorder polls, which is localhost and therefore
          // a secure context. The path, query and hash are carried over so the
          // window lands where the tab already was; tab-local state (form
          // input, modals, scroll) cannot survive an origin change, which is
          // why this is a fallback and not the normal path.
          //
          // A new window rather than this tab, sized so its *content area*
          // matches the capture canvas exactly. That is the whole point of the
          // popup: a viewport already in the canvas ratio is letterboxed edge
          // to edge, so the recording carries no white bars and the app lays
          // itself out at the shape it will be recorded in.
          //
          // This guard is synchronous on purpose: an await here would consume
          // the transient user activation getDisplayMedia needs.
          '      if (!navigator.mediaDevices || !navigator.mediaDevices.getDisplayMedia) {{',
          '        var target = "{capture_origin}" + location.pathname + location.search + location.hash;',
          // Guard against a reopen loop: if we are already on the capture
          // origin, screen capture is missing for some other reason (an
          // unsupported browser), and opening another window achieves nothing.
          '        if (location.origin === "{capture_origin}") {{',
          '          throw new Error("this browser has no screen capture — use Chromium");',
          '        }}',
          // The ratio is derived from the canvas, never written twice: these
          // are the same numbers ffmpeg encodes, so the window cannot drift
          // from the frame size the way a second hardcoded pair would.
          '        var vw = W, vh = H;',
          '        var cap = Math.min((screen.availWidth - 80) / vw, (screen.availHeight - 120) / vh, 1);',
          '        vw = Math.round(vw * cap); vh = Math.round(vh * cap);',
          '        var win = window.open(target, "trustant-capture",',
          '          "width=" + vw + ",height=" + vh + ",menubar=0,toolbar=0,location=0,status=0");',
          '        if (!win) {{',
          '          throw new Error("allow popups for this site, then click again");',
          '        }}',
          // window.open sizes the whole window, chrome included, so the content
          // area comes out short by the height of the toolbars. Correct it once
          // the popup can measure itself: resizeBy works on the outer size, and
          // the difference between requested and actual inner size is exactly
          // the correction needed. Same-origin is not required for resizeBy on
          // a window we opened, but reading innerWidth is, so this runs inside
          // the popup via its own load handler.
          '        var fix = function () {{',
          '          try {{',
          '            var dw = vw - win.innerWidth, dh = vh - win.innerHeight;',
          '            if (dw || dh) win.resizeBy(dw, dh);',
          '          }} catch (e) {{ /* cross-origin or blocked: keep the requested size */ }}',
          '        }};',
          // Two attempts: the load event fires once the popup has a layout, and
          // the timeout covers browsers that report a stale innerHeight there.
          '        win.addEventListener("load", fix);',
          '        setTimeout(fix, 400);',
          '        flash("opened " + vw + "x" + vh, true);',
          '        return;',
          '      }}',
          // getDisplayMedia MUST be the first statement: transient user
          // activation is consumed across awaits, and any await moved ahead of
          // it makes this fail with NotAllowedError on some machines only.
          // The ideal width/height ask the tab-capture scaler for the output
          // shape directly, which is what removes the letterbox in the common
          // case: a viewport whose chrome could not be fully resized away
          // arrives already at the canvas ratio instead of a few pixels short.
          // "ideal", not "exact": an exact constraint the browser cannot meet
          // fails the whole call with OverconstrainedError, and a slightly
          // letterboxed frame is far better than no capture at all.
          '      stream = await navigator.mediaDevices.getDisplayMedia({{',
          '        video: {{ frameRate: 30, width: {{ ideal: W }}, height: {{ ideal: H }} }},',
          '        audio: false, preferCurrentTab: true,',
          '        selfBrowserSurface: "include", surfaceSwitching: "exclude", systemAudio: "exclude",',
          '      }});',
          // The only privacy control here: a full-screen share would post
          // whatever else is on the user's screen into a git-staged file.
          '      if (stream.getVideoTracks()[0].getSettings().displaySurface !== "browser") {{',
          '        throw new Error("share this tab, not the screen");',
          '      }}',
          // Shape the viewport to the canvas BEFORE grabbing, so the frame is
          // already the right ratio and the fit below has nothing to correct.
          // This is the real fix for the white bands: they come from a source
          // whose ratio differs from the canvas, so the source is corrected
          // rather than the result padded.
          //
          // It runs here, after the grant, on purpose. Granting the share makes
          // Chrome push a "Sharing this tab" bar into the window, which steals
          // viewport height; that bar does not exist at window.open time, so
          // the size cannot be got right in advance — only measured and fixed
          // once sharing has actually started.
          //
          // resizeTo/resizeBy only apply to a window this script opened, so a
          // normal tab is left alone and falls through to the width-first fit.
          '      try {{',
          '        if (window.name === "trustant-capture") {{',
          '          for (var attempt = 0; attempt < 3; attempt++) {{',
          '            var dw = W - window.innerWidth, dh = H - window.innerHeight;',
          // A pixel or two of slop is not worth a resize round-trip, and
          // chasing it can oscillate when the browser clamps the size.
          '            if (Math.abs(dw) <= 2 && Math.abs(dh) <= 2) break;',
          '            window.resizeBy(dw, dh);',
          '            await new Promise(function (r) {{ setTimeout(r, 120); }});',
          '          }}',
          '        }}',
          '      }} catch (e) {{ /* resize refused: the width-first fit still applies */ }}',
          // Hide only AFTER the grant. Hiding first would leave the app
          // disfigured and the button invisible when the prompt is denied.
          '      style = hideChrome();',
          '      await new Promise(function (r) {{ requestAnimationFrame(function () {{ requestAnimationFrame(r); }}); }});',
          '      await new Promise(function (r) {{ setTimeout(r, 350); }});',
          '      var bitmap = await grabFrame(stream);',
          '      var blob = await normalize(bitmap);',
          '      if (bitmap.close) bitmap.close();',
          '      await dbPut(blob);',
          '      await drain();',
          '      flash("captured", true);',
          '    }} catch (e) {{',
          '      flash(e && e.name === "NotAllowedError" ? "cancelled" : ("capture failed: " + (e && e.message)), false);',
          '    }} finally {{',
          '      if (stream) stream.getTracks().forEach(function (t) {{ t.stop(); }});',
          '      showChrome(style);',
          '      btn.disabled = false;',
          '    }}',
          '  }}',
          '  btn.addEventListener("click", capture);',
          // Recover anything a reload stranded before it was uploaded.
          '  drain();',
          '}})();',
        ].join('\\n'),
      }}];
    }},
  }};
}};
{end}
"""

# Where the app's own config object is edited is decided BEFORE the factory is
# spliced in. The factory contains a `return {` of its own, and searching the
# combined text would find that one first — the plugin would then declare itself
# as its own plugins array, which parses cleanly and does nothing at all.
if re.search(r'plugins:\s*\[', source):
    # The easy case: an array already exists. First one only — nested arrays
    # belong to other tools (test.deps, storybook, and so on).
    edit_at = None
else:
    # No plugins array: one has to be added. The starter templates generate
    # configs like this, and in the function form —
    #   export default defineConfig(({ mode }) => { ... return { ... }; })
    # — there is not even a top-level object literal to anchor to.
    #
    # The anchor is the opening brace of the config object: the `return {` of a
    # function-form config, or the `defineConfig({` of the object form, or an
    # arrow that returns an object directly. Inserting just after it puts the
    # array at the top level of the config object, the only place Vite reads it.
    # Ordered most-specific first. The arrow-returning-a-literal form,
    #   defineConfig((env) => ({ ... }))
    # has to be tried before the plain `defineConfig({` pattern, because the
    # parameter list may itself contain braces — ({ mode }) => ({ ... }) — and
    # the looser pattern would then anchor on the destructured parameter
    # instead of the config object.
    anchor = (re.search(r'(?m)^\s*return\s*\{', source)
              or re.search(r'defineConfig\s*\((?:[^()]|\([^()]*\))*=>\s*\(\s*\{', source)
              or re.search(r'defineConfig\s*\(\s*\{', source))
    if not anchor:
        raise SystemExit('no config object found to add a plugins array to')
    edit_at = anchor.end()
    # Match the indentation of the anchor's line, so the result still reads like
    # the file it was added to.
    line_start = source.rfind('\n', 0, anchor.start()) + 1
    indent = re.match(r'[ \t]*', source[line_start:]).group(0) + '  '

# After the final top-level import, so the factory is defined before use.
imports = list(re.finditer(r'(?m)^import[^\n]*\n', source))
at = imports[-1].end() if imports else 0
prelude = "\n" + plugin
source = source[:at] + prelude + source[at:]

if edit_at is None:
    source = re.sub(r'plugins:\s*\[', 'plugins: [trustantScreenshotShutter(), ', source, count=1)
else:
    # The splice above shifted everything after the insertion point.
    if edit_at >= at:
        edit_at += len(prelude)
    source = source[:edit_at] + '\n' + indent + 'plugins: [trustantScreenshotShutter()],' + source[edit_at:]

open(path, 'w').write(source)
PY

  local relative="${config#"$APP_DIR"/}"
  hide_file "$relative"
  hide_file ".gitignore"

  # Vite reads its config once at startup and reloads it only when the file
  # changes. Writing the plugin in the same second Vite booted is a real race —
  # observed: the config carried the plugin while the served HTML did not, so
  # every capture silently fell back to "/". Touching the file after the write
  # guarantees a change event Vite has not already consumed.
  sleep 1
  touch "$config"

  # The exit status stays "did the config get written", so a caller can tell a
  # config it cannot inject into from one Vite has not re-read yet. Whether the
  # page actually serves the shutter is reported by wait_for_shutter itself, and
  # recorded here for the caller to consult.
  SHUTTER_SERVED=0
  wait_for_shutter && SHUTTER_SERVED=1
  return 0
}

# Give Vite time to re-read the config and serve the injected plugin. Polling is
# the honest check: the config being right proves nothing if the running server
# has not picked it up.
#
# Both halves are probed because transformIndexHtml and configureServer apply at
# different points in Vite's lifecycle — the HTML can carry the script while the
# middleware is not yet mounted, and a probe of only one would let the loop start
# against a dead endpoint.
wait_for_shutter() {
  local attempt
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    if curl -fsS --max-time 3 "$URL" 2>/dev/null | grep -qF "$SHUTTER_ID"; then
      # 204 is the empty queue, i.e. our middleware answered. Vite's SPA
      # fallback would answer 200 with index.html instead.
      if [[ "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 3 "$SHOT_URL" 2>/dev/null)" == "204" ]]; then
        return 0
      fi
    fi
    sleep 1
  done
  # Report the failure to the caller rather than swallowing it. The injection
  # itself succeeded, so this is not fatal — but the caller must not go on to
  # announce a working shutter over the top of this warning.
  warn "the shutter is not in the served page yet — reload the app tab in your browser"
  return 1
}

remove_reporter() {
  local config
  config="$(vite_config_path)" || return 0
  if grep -qF "$REPORTER_BEGIN" "$config"; then
    python3 - "$config" "$REPORTER_BEGIN" "$REPORTER_END" <<'PY' || true
import re, sys
path, begin, end = sys.argv[1], sys.argv[2], sys.argv[3]
source = open(path).read()
# Consume the blank line the injection added ahead of the block, so removal is
# byte-exact and the file stops showing as modified.
source = re.sub(r'\n?' + re.escape(begin) + r'.*?' + re.escape(end) + r'\n?', '', source, flags=re.S)

# The whole line goes when the array was added by the injection: leaving an
# empty `plugins: [],` behind would not be byte-exact, and the file would keep
# showing as modified in the user's git status. Ordered before the in-place
# case so the more specific pattern wins.
source = re.sub(r'(?m)^[ \t]*plugins: \[trustantScreenshotShutter\(\)\],\n', '', source)

# The array already existed: take out only our call, leave theirs alone.
source = source.replace('plugins: [trustantScreenshotShutter(), ', 'plugins: [')

source = re.sub(r'\n{3,}', '\n\n', source)
open(path, 'w').write(source)
PY
  fi
  unhide_all
}

# Take one frame off the dev server's queue and write it to $1. Returns 1 when
# the queue is empty — the normal state between clicks, not an error.
#
# The GET is destructive server-side, so a frame is handed out exactly once and
# repeated calls drain the queue in click order.
fetch_frame() {
  local out="$1" code
  code="$(curl -sS -o "$out" -w '%{http_code}' --max-time 5 "$SHOT_URL" 2>/dev/null)" || return 1
  [[ "$code" == "200" ]] || return 1
  [[ -s "$out" ]] || return 1
  # A dev server whose plugin is not mounted answers this path with index.html
  # and a 200. Without this check that HTML would be saved as a .png and ffmpeg
  # would fail on a later regenerate, far from the cause.
  [[ "$(head -c 8 "$out" | od -An -tx1 | tr -d ' \n')" == "89504e470d0a1a0a" ]] || return 1
  return 0
}

# --- 5. Rebuild the animation from the frames on disk ---
#
# Regenerating from the directory (rather than appending in place) is what makes
# delete a one-liner and keeps the output consistent with the frames.
#
# ffmpeg is the encoder. `-framerate 1` gives every frame a 1/1 second delay and
# `-plays 0` loops forever. It is called directly and never through ImageMagick:
# `convert ... apng:` delegates to ffmpeg anyway, but re-times everything at
# 25fps on the way, destroying the one-second delay.
#
# Frames are fed as a numbered %05d sequence of symlinks rather than by glob or
# concat, because both of those misbehave here:
#   - `-pattern_type glob` with this directory failed inside the script (exit
#     234, "Nothing was written into output file") while the identical command
#     worked from a shell;
#   - the concat demuxer applies a duration only when another entry follows, so
#     it drops the last frame — and repeating that entry adds a spurious one.
# A %05d sequence has neither problem: N inputs give exactly N frames.
regenerate() {
  local apng="$APP_DIR/screenshot.png" count
  count="$(frame_count)"

  # The last frame was deleted: remove the animation rather than leaving a stale
  # one behind or asking ffmpeg to encode nothing.
  if [[ "$count" == "0" ]]; then
    rm -f "$apng"
    echo 0
    return 0
  fi

  local seqdir tmp errors index=0
  seqdir="$(mktemp -d)"
  tmp="$APP_DIR/.screenshot-$$.png"
  errors="$(mktemp)"

  # Symlinks, so a large recording costs no extra disk. Sorted by filename,
  # which is chronological because the frames are timestamped.
  local frame
  while IFS= read -r frame; do
    ln -s "$frame" "$(printf '%s/%05d.png' "$seqdir" "$index")"
    index=$((index + 1))
  done < <(find "$SHOT_DIR" -maxdepth 1 -name '*.png' -type f | sort)

  # Encode to a temp file first: a failed or interrupted run must not replace a
  # good animation with a truncated one.
  # -nostdin is load-bearing, not tidiness: ffmpeg reads stdin for interactive
  # keys by default, and this runs inside a loop whose own `read` owns stdin.
  # Without it ffmpeg swallows the user's keystrokes and aborts the encode with
  # "at least one of its streams received no packets".
  if ffmpeg -nostdin -y -loglevel error -framerate 1 -i "$seqdir/%05d.png" \
       -plays 0 -f apng "$tmp" 2>"$errors" && [[ -s "$tmp" ]]; then
    mv -f "$tmp" "$apng"
  else
    warn "ffmpeg could not build the animation: $(tail -1 "$errors")"
    rm -f "$tmp"
  fi

  rm -rf "$seqdir" "$errors"
  echo "$count"
}

# --- 6. Publish the animation for preview ---
#
# Nothing is drawn in the terminal. Inline-image protocols only work in some
# terminals, and everything else either sees a base64 dump or an approximation
# in coloured blocks. Copying the animated PNG next to this script instead gives
# a real preview in the editor: open ./screenshot.png in VS Code and it
# refreshes as the file is rewritten, at full fidelity, in any terminal.
#
# The copy is a working preview, not a record — the app's own
# <app>/screenshot.png is the versioned artifact. It is deliberately left
# untracked, see .gitignore below.
# With no frames there is nothing to copy, and the editor would then show either
# a missing file or — worse — the previous app's recording. Write a blank frame
# instead, so the preview always exists and always belongs to the current app.
# ffmpeg is already a dependency, so this costs no new tooling.
blank_preview() {
  ffmpeg -nostdin -y -loglevel error \
    -f lavfi -i "color=c=white:s=${SHOT_WIDTH}x${SHOT_HEIGHT}:d=1" \
    -frames:v 1 "$ROOT/screenshot.png" 2>/dev/null \
    || : > "$ROOT/screenshot.png"
}

# Symlinks next to this script that follow whichever app is current:
#
#   aaa-screenshot.png -> $WORKBENCH_DIR/<current>/screenshot.png
#   aaa-screenshot     -> $WORKBENCH_DIR/<current>/screenshot/
#
# These point INTO the app, unlike ./screenshot.png, which is a copy. That is
# the whole point of having both: the copy is a stable preview that survives an
# app switch, while these always resolve to the live artifact and the frame
# directory, so the frames can be opened without typing the workbench path.
#
# The "aaa-" prefix is deliberate: it sorts them to the top of the file tree,
# which is where they are useful in an editor sidebar.
#
# Removed and recreated rather than replaced in place. `ln -sf` alone is the
# trap: on an existing symlink to a DIRECTORY it follows the link and creates
# the new one *inside* the old target (leaving aaa-screenshot/screenshot behind
# in the previous app) instead of replacing it. `ln -sfn` gets that right, but
# `rm -rf` first is immune either way, and also clears a leftover real directory.
#
# rm -rf, not rm -f, only because the path may be a directory in that leftover
# case. It is never the link's *target* being removed: rm does not follow a
# symlink, so the app's own screenshot/ directory is untouched.

refresh_app_links() {
  local animation="$APP_DIR/screenshot.png" frames="$APP_DIR/screenshot"

  rm -rf "${ROOT}/aaa-screenshot.png" "${ROOT}/aaa-screenshot" 2>/dev/null || true

  # A link to a target that does not exist yet is deliberately still created: the
  # app may have no recording, and the link resolves by itself the moment the
  # first frame is collected. Creating the target to avoid a dangling link was
  # tried and rejected — it would leave an empty screenshot/ directory in the
  # user's checkout for an app they never recorded, and git does not track empty
  # directories anyway, so it bought nothing.
  ln -s "$animation" "$ROOT/aaa-screenshot.png" 2>/dev/null \
    || warn "could not link aaa-screenshot.png -> $animation"
  ln -s "$frames" "$ROOT/aaa-screenshot" 2>/dev/null \
    || warn "could not link aaa-screenshot -> $frames"
}

# Called when no app is current, so the links never point at a stale app.
clear_app_links() {
  rm -rf "${ROOT}/aaa-screenshot.png" "${ROOT}/aaa-screenshot" 2>/dev/null || true
}

preview() {
  local source="$APP_DIR/screenshot.png" count="$1"

  # The links follow the app, not the frame count, so they are refreshed even
  # when there is nothing recorded yet.
  refresh_app_links

  # No frames: publish a blank preview rather than leaving a stale one behind.
  if [[ ! -f "$source" ]]; then
    blank_preview
    ok "no frames yet — $ROOT/screenshot.png is blank"
    return 0
  fi

  cp -f "$source" "$ROOT/screenshot.png" 2>/dev/null \
    || warn "could not copy the preview to $ROOT/screenshot.png"

  # The app name comes from APP_DIR, the same variable the links are built from,
  # rather than from $APP — so the label can never disagree with what the links
  # actually point at.
  ok "$count frame(s) — $ROOT/screenshot.png · aaa-screenshot.png → ${APP_DIR##*/}"
}

# --- 7. Commit ---
#
# Every change is committed because uncommitted files are destroyed by the
# `git clean -fd` that Revert performs (spec/13-gitignore.md).
stage_change() {
  [[ -d "$APP_DIR/.git" ]] || return 0

  # Staged, never committed: the screenshots go out with the user's own commit
  # and push, alongside the app changes they illustrate, instead of arriving as
  # a stream of separate machine-authored commits.
  #
  # Pathspec-scoped: the workbench is a live checkout with arbitrary dirty
  # state, and an unscoped `add` would stage the user's work-in-progress too.
  # `add` on the directory also stages a removed frame as a deletion.
  git -C "$APP_DIR" add -- screenshot screenshot.png 2>/dev/null \
    || warn "could not stage the screenshots"
}

# --- 8. Actions ---
# Collect every frame the page has posted since the last collect. The click is
# the shutter; this is only the pickup, so it never blocks — an empty queue is
# the normal answer between clicks, and blocking would make 'q' unreachable.
capture() {
  # Errors are silenced because the warning below says the same thing more
  # clearly; a raw "curl: (7) Failed to connect" line only adds noise.
  curl -fsS -o /dev/null --max-time 5 "$URL" 2>/dev/null \
    || { warn "the app is not answering at $URL — launch '$APP' first"; return 0; }

  mkdir -p "$SHOT_DIR"
  local staged="$SHOT_DIR/.staging-$$.png"
  local saved=0 stamp target suffix

  # Frames are staged to a dotfile and renamed into place. The rename is atomic
  # and the frame list ignores dotfiles, so a frame becomes visible to the
  # encoder only once it is complete.
  #
  # Bounded by the server's own MAX_FRAMES, so a misbehaving endpoint cannot
  # spin this loop forever.
  while [[ $saved -lt 16 ]] && fetch_frame "$staged"; do
    stamp="$(date -u +%Y%m%d-%H%M%S)"
    target="$SHOT_DIR/$stamp.png"
    suffix=1
    while [[ -e "$target" ]]; do
      target="$SHOT_DIR/$stamp-$suffix.png"
      suffix=$((suffix + 1))
    done
    mv -f "$staged" "$target"
    saved=$((saved + 1))
  done
  rm -f "$staged"

  if [[ $saved -eq 0 ]]; then
    warn "no frame waiting — click the ● button at the top right of the app"
    return 0
  fi

  ok "collected $saved frame(s)"
  local count
  count="$(regenerate | tail -1)"
  stage_change
  preview "$count"
}

# Redraw the current app's animation, and make sure the shutter is really there.
# The point is switching apps: pre-existing frames are preserved, so this shows
# what has already been recorded for whichever app is now current.
#
# SPACE is also the repair key. The injection can be absent for reasons the loop
# cannot see — the app was relaunched over its checkout, the user reverted the
# config, or Vite never picked the change up — and re-running it is cheap and
# idempotent, so this is the one keystroke that gets a missing button back.
show_current() {
  # Re-inject when the served page has no shutter. Checking the served page
  # rather than the file on disk is the honest test: a config carrying the
  # plugin proves nothing if the running server has not read it.
  #
  # Always report the outcome, including the case where nothing needed doing.
  # SPACE is the key you press *because* the ● is missing, so silence is the one
  # useless answer: it leaves you unable to tell "the page has it, your tab is
  # stale" from "the key did nothing".
  if curl -fsS --max-time 3 "$URL" 2>/dev/null | grep -qF "$SHUTTER_ID"; then
    ok "shutter already served by $APP — reload the app tab if the ● is missing"
  else
    # Remove first, so a block already in the file is rewritten rather than
    # skipped: inject_reporter is idempotent and returns early when it finds its
    # own markers, which is precisely the state that needs repairing here.
    remove_reporter
    if inject_reporter; then
      if [[ "$SHUTTER_SERVED" == "1" ]]; then
        ok "shutter (re)injected into $APP — reload the app tab to get the ●"
      else
        warn "shutter written to $APP's vite config but not served yet — is the app still running?"
      fi
    else
      warn "could not add the shutter to $APP's vite config"
    fi
  fi

  local count
  count="$(frame_count)"
  if [[ "$count" == "0" ]]; then
    warn "$APP has no frames yet — click the ● at the top right of the app"
    return 0
  fi
  # Rebuild first: the animations may predate a frame added or removed outside
  # this session.
  count="$(regenerate | tail -1)"
  preview "$count"
}

remove_last() {
  local newest
  newest="$(find "$SHOT_DIR" -maxdepth 1 -name '*.png' -type f 2>/dev/null | sort | tail -1)"
  if [[ -z "$newest" ]]; then
    warn "no frames to remove"
    return 0
  fi
  rm -f "$newest"

  local count
  count="$(regenerate | tail -1)"
  stage_change
  if [[ "$count" == "0" ]]; then
    # The recording is gone. Blank the preview rather than deleting it: an
    # editor tab open on the file keeps working, and it cannot keep showing
    # frames that no longer exist.
    blank_preview
    ok "removed the last frame — $ROOT/screenshot.png is blank"
  else
    preview "$count"
  fi
}

# --- 9. The loop ---
show_help() {
  echo
  echo "  Click the ● button at the TOP RIGHT of the app to capture a frame."
  echo "  Your browser asks which surface to share — pick this tab."
  echo
  echo "  ENTER  collect the frames you have captured"
  echo "  SPACE  refresh the preview — and put the ● back if it went missing"
  echo "  DEL    remove the most recent frame"
  echo "  q      quit"
  echo
  echo "  Frames are kept per app in <app>/screenshot/ and are preserved when you"
  echo "  switch apps — launch another app and press SPACE to load its recording."
  echo
  echo "  The recording is copied to $ROOT/screenshot.png after every change —"
  echo "  open it in the editor to see it."
  echo
}

echo
echo "Recording the launched app from $URL"
echo "Click the ● at the top right of the app to capture, then press ENTER here."
show_help

# The injected reporter and the skip-worktree flags are working state, not
# something to leave behind. Clean up on every exit path, including ^C, so a
# killed session does not strand a modified config or a hidden file. The trap
# runs against whichever app was last current, which is the one that was
# modified.
cleanup_reporter() {
  [[ -n "$APP_DIR" ]] || return 0
  remove_reporter
}
trap cleanup_reporter EXIT INT TERM

LAST_APP=""
LAST_PROMPT=""
PROMPT=""
while true; do
  if resolve_app; then
    if [[ "$APP" != "$LAST_APP" ]]; then
      # Leave the previous app exactly as it was before moving on.
      if [[ -n "$LAST_APP" ]]; then
        PREVIOUS_APP_DIR="$APP_DIR"
        APP_DIR="$WORKBENCH_DIR/$LAST_APP"
        remove_reporter
        APP_DIR="$PREVIOUS_APP_DIR"
        ok "now recording $APP"
      else
        ok "recording $APP"
      fi
      LAST_APP="$APP"

      # Inject into the app that is now current. Failure is not fatal: without
      # a reporter the recorder simply captures "/" as it always did.
      if inject_reporter; then
        if [[ "$SHUTTER_SERVED" == "1" ]]; then
          ok "shutter active in $APP — click the ● at the top right of the app to capture"
        else
          # wait_for_shutter has already said what to do; do not claim success
          # over the top of it.
          warn "shutter injected into $APP but not served yet — press SPACE to retry"
        fi
      else
        warn "no shutter for $APP — its vite.config has no plugins array to inject into"
      fi

      # Publish this app's recording immediately — blank when it has none — so
      # the preview file always exists and always belongs to the app now
      # current, rather than lingering from the previous one.
      preview "$(frame_count)"

      # The lines above were printed over the prompt; force a redraw.
      LAST_PROMPT=""
    fi
    PROMPT="$(printf 'ENTER collect · SPACE refresh · DEL remove · q quit  [%s: %s frames] ' "$APP" "$(frame_count)")"
  else
    # No app is current. Drop the links rather than leaving them aimed at an app
    # that is no longer launched — a link that resolves to the wrong recording is
    # worse than one that is absent.
    if [[ -n "$LAST_APP" ]]; then
      clear_app_links
      LAST_APP=""
    fi
    PROMPT='no app launched — launch one from the Trustant UI  [q quits] '
  fi

  # Only redraw when the line actually changed. The read below times out every
  # couple of seconds, and reprinting on every tick would scroll the terminal
  # continuously while the user is doing nothing.
  if [[ "$PROMPT" != "$LAST_PROMPT" ]]; then
    printf '%s' "$PROMPT"
    LAST_PROMPT="$PROMPT"
  fi

  # The read times out so the loop can notice a launch on its own. Without it
  # the app switch above is only ever evaluated after a keypress, so launching a
  # different app appears to do nothing until the user happens to press a key —
  # and the new app gets no shutter in the meantime.
  #
  # -t distinguishes its two failure modes by exit status: >128 is the timeout
  # (go round again), anything else is EOF, i.e. stdin is not a terminal, where
  # spinning would burn a core forever.
  # `|| status=$?` is load-bearing, not a style choice. Under `set -e` a bare
  # `read` that returns non-zero kills the script outright, so `status=$?` never
  # runs and the timeout branch below is dead code: the loop dies on the first
  # tick with no keypress, the EXIT trap strips the injection, and the app
  # reloads without the shutter. That is a silent exit two seconds after start.
  status=0
  IFS= read -rsn1 -t 2 key || status=$?
  if [[ $status -ne 0 ]]; then
    [[ $status -gt 128 ]] && continue
    # Not a timeout, so stdin is at EOF. Say so before leaving: the loop has
    # just drawn a prompt, and exiting silently underneath it looks like the
    # recorder crashed on its own rather than like it was never given a
    # keyboard. The preflight above catches the usual cause up front; this
    # covers stdin closing mid-session.
    echo
    warn "stdin closed — nothing left to read, so the recorder is stopping"
    break
  fi
  echo
  # The keypress moved the cursor off the prompt line and the action below adds
  # its own output, so the prompt has to be drawn again next time round.
  LAST_PROMPT=""

  case "$key" in
    q|Q) break ;;
    "")            resolve_app && capture      || warn "no app launched" ;;
    $'\177'|$'\b') resolve_app && remove_last  || warn "no app launched" ;;
    " ")           resolve_app && show_current || warn "no app launched" ;;
    *) show_help ;;
  esac
done

echo "Done."
