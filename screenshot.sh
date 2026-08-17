#!/bin/bash
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
[[ -f .env ]] || fail ".env not found — run ./setup.sh first"
# shellcheck disable=SC1091
source ./.env
[[ -n "${WORKBENCH_DIR:-}" ]] || fail "WORKBENCH_DIR is not set in .env"
WORKBENCH_DIR="$(eval echo "$WORKBENCH_DIR")"
[[ -d "$WORKBENCH_DIR" ]] || fail "WORKBENCH_DIR does not exist: $WORKBENCH_DIR"

URL="${TRUSTABLE_SCREENSHOT_URL:-http://localhost:5173}"

# The output canvas, baked into the injected plugin so the page and this script
# can never disagree — a blank placeholder must match a real frame exactly, or
# ffmpeg refuses to encode the sequence.
SHOT_WIDTH="${TRUSTABLE_SCREENSHOT_WIDTH:-600}"
SHOT_HEIGHT="${TRUSTABLE_SCREENSHOT_HEIGHT:-800}"

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
REPORTER_BEGIN="// >>> trustable-screenshot shutter — injected by screenshot.sh, removed on quit"
REPORTER_END="// <<< trustable-screenshot shutter"
SHUTTER_ID="__trustable_shutter__"
SHOT_ENDPOINT="/__trustable_shot"
SHOT_URL="${URL%/}$SHOT_ENDPOINT"

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
  config="$(vite_config_path)" || return 1
  grep -qF "$REPORTER_BEGIN" "$config" && return 0

  # A missing plugins array is not a reason to give up: the starter templates
  # generate configs without one (and in the function form,
  # `defineConfig(({ mode }) => ({ ... }))`, where there is no object literal to
  # match either). The rewriter below adds `plugins: []` when it has to, so those
  # apps get a shutter too. It still declines a config whose config object it
  # cannot locate at all — see the failure paths in the Python block.
  unhide_all
  python3 - "$config" "$REPORTER_BEGIN" "$REPORTER_END" "$SHUTTER_ID" \
           "$SHOT_ENDPOINT" "$SHOT_WIDTH" "$SHOT_HEIGHT" <<'PY' || return 1
import re, sys
path, begin, end, marker = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
endpoint, width, height = sys.argv[5], sys.argv[6], sys.argv[7]
source = open(path).read()

# The client script is assembled with single quotes and concatenation, never
# backticks: it lives inside a JS template literal inside this f-string, and a
# stray backtick would end the literal early.
#
# No line inside this payload may start with '#'. screenshot_script_test.go
# strips comment lines before asserting, and would strip such a line too.
plugin = f"""{begin}
const trustableScreenshotShutter = () => {{
  // Newest last, in memory only: the recorder drains this over HTTP, and a dev
  // server restart is a new recording session anyway. Bounded, so a user who
  // clicks ten times while the recorder is not polling cannot grow the heap.
  const frames = [];
  const MAX_FRAMES = 16;

  return {{
    name: 'trustable-screenshot-shutter',
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
          '  if (window.__trustableShutter) return;',
          '  window.__trustableShutter = true;',
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
          '    var rq = indexedDB.open("trustable-shots", 1);',
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
          // Styles are inline, not a stylesheet: an injected <style> loses to
          // the app's own reset, and a Tailwind-preflight rule resetting every
          // button property would erase this button entirely.
          '  var btn = document.createElement("button");',
          '  btn.id = "{marker}";',
          '  btn.setAttribute("data-trustable-shutter", "");',
          '  btn.type = "button";',
          '  btn.title = "Capture a screenshot frame";',
          // A geometric glyph, not an emoji: this renders in the USER's browser,
          // whose font coverage is unknown. The VM's emoji font is irrelevant here.
          '  btn.textContent = "\\u25CF";',
          '  btn.style.cssText = "position:fixed;top:12px;right:12px;width:36px;height:36px;"',
          '    + "border-radius:50%;border:1px solid rgba(0,0,0,.2);background:#ffffff;color:#dd3333;"',
          '    + "font:16px/1 system-ui,sans-serif;cursor:pointer;padding:0;z-index:2147483001;"',
          '    + "box-shadow:0 1px 4px rgba(0,0,0,.3)";',
          '  function mount() {{ if (document.body) document.body.appendChild(btn); }}',
          '  if (document.body) mount(); else addEventListener("DOMContentLoaded", mount);',

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
          '      + "[data-trustable-shutter]{{visibility:hidden !important;}}";',
          '    document.head.appendChild(style);',
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
          // mid-session, so the frame is contain-fit onto a fixed canvas here,
          // at the point of creation.
          //
          // Contain-fit, not crop: the tab is landscape and the canvas portrait,
          // so a cover-fit crop would discard most of the width. White fill, to
          // match the ffmpeg blank placeholder so the animation does not flash.
          //
          // The pixel ratio is deliberately not applied: getDisplayMedia already
          // returns device pixels, so scaling by DPR again would give 1200x1600
          // frames on Retina and reintroduce the variance.
          '  function normalize(bitmap) {{',
          '    var canvas = document.createElement("canvas");',
          '    canvas.width = W;',
          '    canvas.height = H;',
          '    var ctx = canvas.getContext("2d");',
          '    ctx.fillStyle = "#ffffff";',
          '    ctx.fillRect(0, 0, W, H);',
          '    var scale = Math.min(W / bitmap.width, H / bitmap.height);',
          '    var w = Math.round(bitmap.width * scale);',
          '    var h = Math.round(bitmap.height * scale);',
          '    ctx.imageSmoothingEnabled = true;',
          '    ctx.imageSmoothingQuality = "high";',
          '    ctx.drawImage(bitmap, Math.round((W - w) / 2), Math.round((H - h) / 2), w, h);',
          '    return new Promise(function (resolve, reject) {{',
          '      canvas.toBlob(function (b) {{ b ? resolve(b) : reject(new Error("toBlob")); }}, "image/png");',
          '    }});',
          '  }}',

          '  async function capture() {{',
          '    btn.disabled = true;',
          '    var stream = null, style = null;',
          '    try {{',
          // getDisplayMedia MUST be the first statement: transient user
          // activation is consumed across awaits, and any await moved ahead of
          // it makes this fail with NotAllowedError on some machines only.
          '      stream = await navigator.mediaDevices.getDisplayMedia({{',
          '        video: {{ frameRate: 30 }}, audio: false, preferCurrentTab: true,',
          '        selfBrowserSurface: "include", surfaceSwitching: "exclude", systemAudio: "exclude",',
          '      }});',
          // The only privacy control here: a full-screen share would post
          // whatever else is on the user's screen into a git-staged file.
          '      if (stream.getVideoTracks()[0].getSettings().displaySurface !== "browser") {{',
          '        throw new Error("share this tab, not the screen");',
          '      }}',
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
    source = re.sub(r'plugins:\s*\[', 'plugins: [trustableScreenshotShutter(), ', source, count=1)
else:
    # The splice above shifted everything after the insertion point.
    if edit_at >= at:
        edit_at += len(prelude)
    source = source[:edit_at] + '\n' + indent + 'plugins: [trustableScreenshotShutter()],' + source[edit_at:]

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
  wait_for_shutter
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
  warn "the shutter is not in the served page yet — reload the app tab in your browser"
  return 0
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
source = re.sub(r'(?m)^[ \t]*plugins: \[trustableScreenshotShutter\(\)\],\n', '', source)

# The array already existed: take out only our call, leave theirs alone.
source = source.replace('plugins: [trustableScreenshotShutter(), ', 'plugins: [')

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

preview() {
  local source="$APP_DIR/screenshot.png" count="$1"

  # No frames: publish a blank preview rather than leaving a stale one behind.
  if [[ ! -f "$source" ]]; then
    blank_preview
    ok "no frames yet — $ROOT/screenshot.png is blank"
    return 0
  fi

  cp -f "$source" "$ROOT/screenshot.png" 2>/dev/null \
    || warn "could not copy the preview to $ROOT/screenshot.png"

  ok "$count frame(s) — $ROOT/screenshot.png"
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
  if ! curl -fsS --max-time 3 "$URL" 2>/dev/null | grep -qF "$SHUTTER_ID"; then
    # Remove first, so a block already in the file is rewritten rather than
    # skipped: inject_reporter is idempotent and returns early when it finds its
    # own markers, which is precisely the state that needs repairing here.
    remove_reporter
    if inject_reporter; then
      ok "shutter (re)injected — reload the app tab if the ● is still missing"
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
        ok "shutter active — click the ● at the top right of the app to capture"
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
    PROMPT='no app launched — launch one from the Trustable UI  [q quits] '
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
  IFS= read -rsn1 -t 2 key
  status=$?
  if [[ $status -ne 0 ]]; then
    [[ $status -gt 128 ]] && continue
    echo
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
