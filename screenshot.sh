#!/bin/bash
#
# screenshot.sh — record the launched app INSIDE the trudev VM (spec/16-screenshot.md).
#
# An interactive loop: ENTER captures a frame, SPACE refreshes the preview,
# DEL/BACKSPACE drops the last frame, q quits. Every change rebuilds the app's
# screenshot.png (an animated PNG, one second per frame) from the frames on disk
# and copies it next to this script so it can be previewed in the editor.
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

# --- 3. Install what is missing ---
#
# ffmpeg is the APNG encoder. It is installed directly rather than reached
# through ImageMagick: IM 6.9 has no APNG encoder of its own and delegates
# `apng:` to ffmpeg, writing a 0-byte file when ffmpeg is absent and re-timing
# every frame at 25fps when it is present, which destroys the one-second delay.
APT_MISSING=()
command -v ffmpeg &>/dev/null || APT_MISSING+=(ffmpeg)

# Headless Chromium renders whatever fonts the system has, and a bare VM has no
# emoji font at all — every emoji comes out as an empty box. Verified: with 117
# fonts installed but none carrying emoji glyphs, 🎉 ✅ 🚀 rendered as tofu.
# fonts-noto-color-emoji (~10MB) is what makes them appear in captures.
#
# The package directory is checked directly rather than through fc-list, because
# fontconfig is not in the runtime image — a missing fc-list would make the probe
# silently succeed and skip the font.
if ! find /usr/share/fonts -iname '*emoji*' -print -quit 2>/dev/null | grep -q .; then
  APT_MISSING+=(fonts-noto-color-emoji)
fi
if [[ ${#APT_MISSING[@]} -gt 0 ]]; then
  warn "installing missing apt packages: ${APT_MISSING[*]}"
  sudo apt-get update -qq || fail "apt-get update failed"
  sudo apt-get install -y "${APT_MISSING[@]}" || fail "apt-get install ${APT_MISSING[*]} failed"
fi

command -v npm &>/dev/null || fail "npm not found — run ./setup.sh first"
if [[ ! -d "$ROOT/node_modules/@playwright/test" ]]; then
  warn "installing @playwright/test..."
  ( cd "$ROOT" && npm install --no-audit --no-fund ) || fail "npm install failed"
fi
if [[ "${TRUSTABLE_SCREENSHOT_SKIP_BROWSER_INSTALL:-}" != "1" ]]; then
  # Idempotent: no-ops when the pinned Chromium revision is already cached.
  # install-deps is deliberately NOT run here — it is a large unattended sudo
  # install. spec/16-screenshot.md documents it as the manual fallback.
  ( cd "$ROOT" && npx playwright install chromium ) || fail "playwright install chromium failed"
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
  name="$(tr -d '[:space:]' < "$WORKBENCH_DIR/current" 2>/dev/null || true)"
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

# --- 4b. Report the route the user is actually on ---
#
# Without this the recorder always captures "/". The route lives in a browser
# cookie no server code reads, and the preview iframe is cross-origin, so
# nothing on the Trustable side can see where the user navigated.
#
# The fix is a Vite plugin injected into the app's own vite.config.ts. It uses
# transformIndexHtml, so the script is added when the page is *served* — the
# app's index.html on disk is never touched, which matters because the starter
# template regenerates that file.
#
# The injected script keeps a meta tag in sync with location, and the recorder
# reads that tag back over the app's MCP server. HashRouter apps keep the route
# in the hash, so pathname alone would not be enough.
REPORTER_BEGIN="// >>> trustable-screenshot reporter — injected by screenshot.sh, removed on quit"
REPORTER_END="// <<< trustable-screenshot reporter"
LOCATION_ID="__trustable_location__"

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

  # Only touch a config that actually uses the Vite plugin array. Anything else
  # is a shape this cannot safely edit.
  grep -qE 'plugins:[[:space:]]*\[' "$config" || return 1

  unhide_all
  python3 - "$config" "$REPORTER_BEGIN" "$REPORTER_END" "$LOCATION_ID" <<'PY' || return 1
import re, sys
path, begin, end, marker = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
source = open(path).read()

plugin = f"""{begin}
const trustableScreenshotReporter = () => ({{
  name: 'trustable-screenshot-reporter',
  apply: 'serve',
  transformIndexHtml() {{
    return [{{
      tag: 'script',
      injectTo: 'head-prepend',
      attrs: {{ type: 'text/javascript' }},
      children: `(function () {{
        function report() {{
          var el = document.getElementById('{marker}');
          if (!el) {{
            el = document.createElement('meta');
            el.id = '{marker}';
            document.head.appendChild(el);
          }}
          el.setAttribute('content', location.pathname + location.search + location.hash);
        }}
        report();
        addEventListener('hashchange', report);
        addEventListener('popstate', report);
        var push = history.pushState;
        history.pushState = function () {{ push.apply(this, arguments); report(); }};
        var replace = history.replaceState;
        history.replaceState = function () {{ replace.apply(this, arguments); report(); }};
      }})();`,
    }}];
  }},
}});
{end}
"""

# After the final top-level import, so the factory is defined before use.
imports = list(re.finditer(r'(?m)^import[^\n]*\n', source))
at = imports[-1].end() if imports else 0
source = source[:at] + "\n" + plugin + source[at:]

# First plugins array only: nested ones belong to other tools.
source = re.sub(r'plugins:\s*\[', 'plugins: [trustableScreenshotReporter(), ', source, count=1)

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
  wait_for_reporter
  return 0
}

# Give Vite time to re-read the config and serve the injected script. Polling
# the served HTML is the honest check: the config being right proves nothing if
# the running server has not picked it up.
wait_for_reporter() {
  local attempt
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    if curl -fsS --max-time 3 "$URL" 2>/dev/null | grep -qF "$LOCATION_ID"; then
      return 0
    fi
    sleep 1
  done
  warn "the route reporter is not in the served page yet — captures may use /"
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
source = source.replace('plugins: [trustableScreenshotReporter(), ', 'plugins: [')
source = re.sub(r'\n{3,}', '\n\n', source)
open(path, 'w').write(source)
PY
  fi
  unhide_all
}

# Ask the app's MCP server for the meta tag the reporter maintains. Returns the
# route on stdout, or nothing when it cannot be determined — the caller then
# falls back to "/", so an app without the reporter still records.
#
# MCP reflects a live browser tab: with no tab open on the app there is no route
# to read. That is a normal state, not an error.
read_current_route() {
  [[ -n "${TRUSTABLE_SCREENSHOT_ROUTE:-}" ]] && { echo "$TRUSTABLE_SCREENSHOT_ROUTE"; return 0; }
  command -v node &>/dev/null || return 0
  node "$ROOT/tests/screenshot-route.mjs" "$URL" "$LOCATION_ID" 2>/dev/null || true
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
preview() {
  local source="$APP_DIR/screenshot.png" count="$1"
  [[ -f "$source" ]] || return 0

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
capture() {
  curl -fsS -o /dev/null --max-time 5 "$URL" \
    || { warn "the app is not answering at $URL — launch '$APP' first"; return 0; }

  # Capture the page the user is on, not the app root. An empty route is the
  # normal answer when no browser tab is open on the app, or the app has no
  # reporter — "/" is then correct rather than a failure.
  local route target_url detected
  detected="$(read_current_route)"
  route="${detected:-/}"
  target_url="${URL%/}$route"

  # Announce the URL before the shutter, not after. When the captured page is
  # not the one expected, this line is what tells you whether the route was
  # never detected (falling back to /) or detected and wrong.
  if [[ -n "$detected" ]]; then
    ok "capturing $target_url"
  else
    ok "capturing $target_url  (no route reported — using /)"
  fi

  mkdir -p "$SHOT_DIR"
  local stamp target suffix
  stamp="$(date -u +%Y%m%d-%H%M%S)"
  target="$SHOT_DIR/$stamp.png"
  suffix=1
  while [[ -e "$target" ]]; do
    target="$SHOT_DIR/$stamp-$suffix.png"
    suffix=$((suffix + 1))
  done

  # Capture to a dotfile and rename into place. The rename is atomic and the
  # frame list ignores dotfiles, so a frame becomes visible to the encoder only
  # once it is complete.
  local staged="$SHOT_DIR/.staging-$$.png"
  node "$ROOT/tests/screenshot.mjs" "$target_url" "$staged" \
    || { warn "capture failed"; rm -f "$staged"; return 0; }
  mv -f "$staged" "$target"

  local count
  count="$(regenerate | tail -1)"
  stage_change
  preview "$count"
}

# Redraw the current app's animation without changing anything. The point is
# switching apps: pre-existing frames are preserved, so this shows what has
# already been recorded for whichever app is now current.
show_current() {
  local count
  count="$(frame_count)"
  if [[ "$count" == "0" ]]; then
    warn "$APP has no frames yet — press ENTER to capture one"
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
    # The recording is gone, so the preview copy must go too rather than sit
    # there showing frames that no longer exist.
    rm -f "$ROOT/screenshot.png"
    ok "removed the last frame — no animation left"
  else
    preview "$count"
  fi
}

# --- 9. The loop ---
show_help() {
  echo
  echo "  ENTER  capture a frame of the running app"
  echo "  SPACE  refresh the preview from the current app (nothing is captured)"
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
        ok "route reporter active — captures follow the page you are on"
      else
        warn "no route reporter for $APP — captures will use /"
      fi
    fi
    printf 'ENTER capture · SPACE refresh · DEL remove · q quit  [%s: %s frames] ' "$APP" "$(frame_count)"
  else
    printf 'no app launched — launch one from the Trustable UI  [q quits] '
  fi

  # A failed read means EOF (stdin is not a terminal): leave, do not spin.
  IFS= read -rsn1 key || { echo; break; }
  echo

  case "$key" in
    q|Q) break ;;
    "")            resolve_app && capture      || warn "no app launched" ;;
    $'\177'|$'\b') resolve_app && remove_last  || warn "no app launched" ;;
    " ")           resolve_app && show_current || warn "no app launched" ;;
    *) show_help ;;
  esac
done

echo "Done."
