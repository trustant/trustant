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

  # The animation lives in the app root, which Vite already serves, so it can be
  # opened by URL. That matters: an APNG only animates in a browser — VS Code's
  # built-in image preview renders the first frame and stops, so the copied file
  # on its own looks like a still. The query string defeats the browser cache,
  # which would otherwise keep showing the previous capture.
  ok "$count frame(s) — $URL/screenshot.png?v=$count"
}

# --- 7. Commit ---
#
# Every change is committed because uncommitted files are destroyed by the
# `git clean -fd` that Revert performs (spec/13-gitignore.md).
commit_change() {
  local message="$1"
  [[ -d "$APP_DIR/.git" ]] || return 0

  git -C "$APP_DIR" config --get user.name  >/dev/null 2>&1 \
    || git -C "$APP_DIR" config user.name  "${GIT_USER:-Trustable}"
  git -C "$APP_DIR" config --get user.email >/dev/null 2>&1 \
    || git -C "$APP_DIR" config user.email "${GIT_EMAIL:-trustable@localhost}"

  # Pathspec-scoped: the workbench is a live checkout with arbitrary dirty
  # state, and a bare commit would sweep the user's work into this one.
  git -C "$APP_DIR" add -- screenshot screenshot.png 2>/dev/null || true
  if git -C "$APP_DIR" diff --cached --quiet -- screenshot screenshot.png 2>/dev/null; then
    return 0
  fi
  git -C "$APP_DIR" commit -q -m "$message" -- screenshot screenshot.png \
    || warn "git commit failed"
}

# --- 8. Actions ---
capture() {
  curl -fsS -o /dev/null --max-time 5 "$URL" \
    || { warn "the app is not answering at $URL — launch '$APP' first"; return 0; }

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
  node "$ROOT/tests/screenshot.mjs" "$URL" "$staged" \
    || { warn "capture failed"; rm -f "$staged"; return 0; }
  mv -f "$staged" "$target"

  local count
  count="$(regenerate | tail -1)"
  commit_change "screenshot: add frame $count"
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
  commit_change "screenshot: remove frame $((count + 1))"
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
  echo "  To watch it animate, open this in a browser — in VS Code use"
  echo "  'Simple Browser: Show' from the Command Palette:"
  echo
  echo "      $URL/screenshot.png"
  echo
  echo "  The editor's own image preview shows only the first frame; an animated"
  echo "  PNG needs a browser. Reload after each capture, or use the ?v= URL"
  echo "  printed below, to get past the browser cache."
  echo
}

echo
echo "Recording the launched app from $URL"
show_help

LAST_APP=""
while true; do
  if resolve_app; then
    if [[ "$APP" != "$LAST_APP" ]]; then
      [[ -n "$LAST_APP" ]] && ok "now recording $APP" || ok "recording $APP"
      LAST_APP="$APP"
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
