#!/bin/bash
#
# screenshot.sh — record the launched app INSIDE the trudev VM (spec/16-screenshot.md).
#
# An interactive loop: ENTER captures a frame, SPACE redraws the current app's
# animation, DEL/BACKSPACE drops the last frame, q quits. Every change rewrites
# screenshot.png (animated PNG) and screenshot.gif from the frames on disk, and
# draws the GIF inline when the terminal can show it.
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
# WHY Pillow and not ImageMagick: IM 6.9 has no APNG encoder and no apng
# delegate, so `convert ... apng:out.png` shells out to ffmpeg — a 0-byte file
# when ffmpeg is absent, and 25fps re-timing when it is present, which discards
# the one-second frame delay entirely. Pillow writes both formats correctly.
APT_MISSING=()
python3 -c 'import PIL' &>/dev/null || APT_MISSING+=(python3-pil)
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

# --- 5. Rebuild both animations from the frames on disk ---
#
# Regenerating from the directory (rather than appending in place) is what makes
# delete a one-liner and keeps both outputs consistent with the frames.
regenerate() {
  python3 - "$SHOT_DIR" "$APP_DIR/screenshot.png" "$APP_DIR/screenshot.gif" <<'PY' || fail "failed to build the animations"
import glob, os, sys
from PIL import Image

shot_dir, apng, gif = sys.argv[1], sys.argv[2], sys.argv[3]
files = sorted(glob.glob(os.path.join(shot_dir, "*.png")))

# The last frame was deleted: remove the animations rather than writing an
# empty one.
if not files:
    for path in (apng, gif):
        if os.path.exists(path):
            os.remove(path)
    print(0)
    sys.exit(0)

frames = [Image.open(f).convert("RGBA") for f in files]

# duration=1000 writes every APNG fcTL delay as 1000/1000 — exactly one second.
# loop=0 means loop forever. save_all on a single frame is valid, so the first
# capture needs no special case.
#
# disposal=1/blend=0 is load-bearing, not cosmetic: without it Pillow collapses
# identical consecutive frames into a single frame with a summed delay. Two
# captures of a screen that has not changed yet would silently become one frame
# of two seconds instead of two frames of one.
frames[0].save(apng, format="PNG", save_all=True, append_images=frames[1:],
               duration=1000, loop=0, disposal=1, blend=0)

# The GIF is the terminal preview. Pillow's GIF encoder merges identical
# consecutive frames whatever options it is given, so a run of unchanged
# captures shows as one longer frame here. screenshot.png above is the exact
# record; this is the convenience copy.
rgb = [f.convert("RGB") for f in frames]
rgb[0].save(gif, format="GIF", save_all=True, append_images=rgb[1:],
            duration=1000, loop=0, disposal=2, optimize=False)

print(len(frames))
PY
}

# --- 6. Show the GIF in the terminal ---
#
# LC_TERMINAL is probed before TERM_PROGRAM because it survives SSH, and this
# tool is normally reached through ./ssh.sh. Anything else gets a path: emitting
# the escape sequence blindly dumps kilobytes of base64 into VS Code or tmux.
preview() {
  local gif="$APP_DIR/screenshot.gif" count="$1"
  [[ -f "$gif" ]] || return 0
  if [[ "${LC_TERMINAL:-}" == "iTerm2" || "${TERM_PROGRAM:-}" == "iTerm.app" ]]; then
    printf '\033]1337;File=inline=1;width=20;preserveAspectRatio=1:%s\a\n' "$(base64 -w0 "$gif")"
  else
    ok "$count frame(s) — $gif"
  fi
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
  git -C "$APP_DIR" add -- screenshot screenshot.png screenshot.gif 2>/dev/null || true
  if git -C "$APP_DIR" diff --cached --quiet -- screenshot screenshot.png screenshot.gif 2>/dev/null; then
    return 0
  fi
  git -C "$APP_DIR" commit -q -m "$message" -- screenshot screenshot.png screenshot.gif \
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

  node "$ROOT/tests/screenshot.mjs" "$URL" "$target" \
    || { warn "capture failed"; rm -f "$target"; return 0; }

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
    ok "removed the last frame — no animation left"
  else
    preview "$count"
  fi
}

# --- 9. The loop ---
show_help() {
  echo
  echo "  ENTER  capture a frame of the running app"
  echo "  SPACE  redraw the current app's animation (nothing is captured)"
  echo "  DEL    remove the most recent frame"
  echo "  q      quit"
  echo
  echo "  Frames are kept per app in <app>/screenshot/ and are preserved when you"
  echo "  switch apps — launch another app and press SPACE to see its recording."
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
    printf 'ENTER capture · SPACE show · DEL remove · q quit  [%s: %s frames] ' "$APP" "$(frame_count)"
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
