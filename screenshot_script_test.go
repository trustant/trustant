package main

import (
	"os"
	"strings"
	"testing"
)

func readScreenshotScript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("screenshot.sh")
	if err != nil {
		t.Fatalf("read screenshot.sh: %s", err)
	}
	return string(data)
}

// scriptCode strips comment lines so assertions match what the script actually
// runs. The comments here deliberately name the things that must not be used
// (`apng:`), which would otherwise trip the very checks that guard against them.
func scriptCode(script string) string {
	var code []string
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	return strings.Join(code, "\n")
}

// WHY: ImageMagick 6 has no APNG encoder and no apng delegate, so
// `convert ... apng:out.png` silently shells out to ffmpeg — a 0-byte file when
// ffmpeg is absent, and a 25fps re-encode when it is present, which throws away
// the one-second frame delay entirely. ffmpeg is called directly instead, so a
// "simplification" back to ImageMagick must fail the tests rather than quietly
// produce a broken animation.
func TestScreenshotScriptDoesNotUseImageMagickForAPNG(t *testing.T) {
	script := readScreenshotScript(t)
	code := scriptCode(script)
	if strings.Contains(code, "apng:") {
		t.Fatal("screenshot.sh must not build APNGs with ImageMagick: IM6 delegates apng: to ffmpeg, which re-times frames at 25fps")
	}
	if strings.Contains(code, "convert -delay") {
		t.Fatal("screenshot.sh must not build animations with ImageMagick convert")
	}
	if !strings.Contains(code, "APT_MISSING+=(ffmpeg)") {
		t.Fatal("screenshot.sh must install ffmpeg, which is what encodes the animation")
	}
}

// WHY: the page captures itself in the user's own browser now, so the VM never
// rasterizes app content. A browser install here would gate every screenshot
// behind a ~150MB download for a code path that no longer exists, and the emoji
// font existed only because headless Chromium rendered the app with the VM's
// fonts — the user's browser brings its own.
func TestScreenshotScriptNeedsNoBrowser(t *testing.T) {
	script := readScreenshotScript(t)
	code := scriptCode(script)

	if strings.Contains(code, "playwright install") {
		t.Error("the recorder no longer drives a browser: the page captures itself")
	}
	if strings.Contains(code, "@playwright/test") {
		t.Error("the recorder must not depend on Playwright")
	}
	if strings.Contains(code, "fonts-noto-color-emoji") {
		t.Error("the emoji font was for headless Chromium in the VM, which no longer renders anything")
	}
	// ffmpeg is the one binary that is still genuinely needed.
	if !strings.Contains(code, "APT_MISSING+=(ffmpeg)") {
		t.Error("ffmpeg is still the encoder and must still be installed")
	}
}

// The tool only makes sense where the app and the workbench live. On macOS the
// Vite server, $WORKBENCH_DIR, and apt-get are all absent.
func TestScreenshotScriptGuardsVMOnly(t *testing.T) {
	script := readScreenshotScript(t)
	if !strings.Contains(script, `[[ "$OS" == "linux" ]]`) {
		t.Error("screenshot.sh must refuse to run outside Linux")
	}
	if !strings.Contains(script, "/etc/os-release") {
		t.Error("screenshot.sh must verify the distribution is Ubuntu/Debian")
	}
	if !strings.Contains(script, "./ssh.sh ./screenshot.sh") {
		t.Error("the macOS failure must point at ./ssh.sh, which is how the VM is reached")
	}
}

// One second per frame, looping forever.
func TestScreenshotScriptWritesOneSecondLoopingFrames(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))
	if !strings.Contains(code, "-framerate 1") {
		t.Error("the animation must be encoded at -framerate 1 (one second per frame)")
	}
	if !strings.Contains(code, "-plays 0") {
		t.Error("the animation must be encoded with -plays 0 (loop forever)")
	}
}

// WHY: ffmpeg reads stdin for interactive keys by default, and the encode runs
// inside a loop whose own `read` owns stdin. Without -nostdin ffmpeg swallows
// the user's keystrokes and the encode dies with "at least one of its streams
// received no packets" — the animation is then never written at all. Observed,
// not theorised: this silently broke every first capture.
func TestScreenshotScriptKeepsFFmpegOffStdin(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))
	if !strings.Contains(code, "ffmpeg -nostdin") {
		t.Fatal("ffmpeg must run with -nostdin, or it consumes the interactive loop's keystrokes and the encode fails")
	}
}

// WHY the %05d sequence rather than a glob or the concat demuxer: ffmpeg's
// glob matched nothing from inside the script, and concat applies a duration
// only when another entry follows, so it drops the final frame (and repeating
// that entry adds a spurious one). A numbered sequence gives exactly N frames
// for N inputs.
func TestScreenshotScriptFeedsFramesAsANumberedSequence(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))
	if !strings.Contains(code, `-i "$seqdir/%05d.png"`) {
		t.Error("frames must be fed to ffmpeg as a numbered %05d sequence")
	}
	if strings.Contains(code, "-pattern_type glob") {
		t.Error("ffmpeg's glob demuxer is unreliable here — use the numbered sequence")
	}
	if strings.Contains(code, "-f concat") {
		t.Error("the concat demuxer miscounts the final frame — use the numbered sequence")
	}
}

// Deleting the last remaining frame must remove the animation, not leave a
// stale one behind.
func TestScreenshotScriptHandlesTheEmptyCase(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))
	if !strings.Contains(code, `if [[ "$count" == "0" ]]; then`) {
		t.Error("screenshot.sh must handle the no-frames-left case explicitly")
	}
	if !strings.Contains(code, `rm -f "$apng"`) {
		t.Error("the last delete must remove screenshot.png")
	}
}

// A frame must become visible to the encoder only once it is complete: ffmpeg
// globs the directory moments after the capture writes into it.
func TestScreenshotScriptStagesFramesAtomically(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))
	if !strings.Contains(code, `staged="$SHOT_DIR/.staging-$$.png"`) {
		t.Error("captures must be staged to a dotfile and renamed into place")
	}
	if !strings.Contains(code, `mv -f "$staged" "$target"`) {
		t.Error("the staged frame must be renamed into place atomically")
	}
}

// Screenshots are staged, never committed: they go out with the user's own
// commit and push, alongside the app changes they illustrate, instead of
// arriving as a stream of separate machine-authored commits.
//
// The workbench is a live user checkout with arbitrary dirty state, so the
// pathspec is scoped — an unscoped `add` would stage work-in-progress too.
func TestScreenshotScriptStagesWithoutCommitting(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "add -- screenshot screenshot.png") {
		t.Error("git add must be scoped to the screenshot paths")
	}
	if strings.Contains(code, "git -C \"$APP_DIR\" commit") {
		t.Fatal("screenshot.sh must stage the screenshots, not commit them")
	}
	if strings.Contains(code, "commit -a") {
		t.Fatal("screenshot.sh must never commit the whole workbench")
	}
}

// All four keys, plus the EOF guard that stops a piped stdin from spinning.
func TestScreenshotScriptHandlesAllKeys(t *testing.T) {
	script := readScreenshotScript(t)
	for _, want := range []string{
		`$'\177'`, // delete / backspace
		`q|Q)`,    // quit
		`" ")`,    // space redraws the current app
	} {
		if !strings.Contains(script, want) {
			t.Errorf("screenshot.sh must handle the %s key", want)
		}
	}
	// The read must time out, or the app-switch check above it is only ever
	// evaluated after a keypress — launching a different app would then appear to
	// do nothing, and the new app would get no shutter, until a key is pressed.
	if !strings.Contains(script, "read -rsn1 -t") {
		t.Error("the read must time out so the loop notices a new current app on its own")
	}
	// A timeout and EOF are both non-zero exits from read. They must be told
	// apart by status: >128 is the timeout and loops again, anything else is a
	// non-terminal stdin, where looping would spin forever on a burnt core.
	if !strings.Contains(script, "$status -gt 128") {
		t.Error("a failed read (EOF) must exit the loop instead of spinning; a timeout must not")
	}
	// WHY: the script runs under `set -e`, where a bare `read` returning
	// non-zero terminates it before `status=$?` can run — making the timeout
	// branch above dead code. Observed: the recorder drew its prompt, exited two
	// seconds later with no keypress, and the EXIT trap stripped the injection,
	// so the app reloaded without the shutter. The failure must stay attached to
	// the read itself.
	if !strings.Contains(script, "read -rsn1 -t 2 key || status=$?") {
		t.Error("the timed read must capture its status with `|| status=$?`, or set -e kills the loop on the first timeout")
	}
}

// WHY: the recorder is a keyboard loop, so a stdin that is not a terminal makes
// every read hit EOF at once and the loop exit right after drawing its prompt.
// That looks exactly like a crash — the reported symptom was "why does the
// screenshot exit?" — and by then a plugin has already been injected into the
// user's vite.config. The guard must run before any of that, and must name the
// interactive invocation, or the next person debugs the injection instead of
// the pipe.
func TestScreenshotScriptRequiresATerminal(t *testing.T) {
	script := readScreenshotScript(t)
	if !strings.Contains(script, "[[ -t 0 ]]") {
		t.Error("screenshot.sh must refuse to run without a terminal on stdin")
	}
	// Ahead of the injection: the check is worth little if the config has
	// already been rewritten by the time it fires.
	tty := strings.Index(script, "[[ -t 0 ]]")
	inject := strings.Index(script, "inject_reporter()")
	if tty < 0 || inject < 0 || tty > inject {
		t.Error("the terminal check must come before the injection machinery")
	}
}

// The app can change while the loop runs, so `current` is read per iteration
// rather than once at startup.
func TestScreenshotScriptRereadsCurrentApp(t *testing.T) {
	script := readScreenshotScript(t)
	if !strings.Contains(script, `< "$WORKBENCH_DIR/current"`) {
		t.Error("screenshot.sh must resolve the app from $WORKBENCH_DIR/current")
	}
	if !strings.Contains(script, "resolve_app()") {
		t.Error("app resolution must be a function so it can run every iteration")
	}
	if strings.Count(script, "resolve_app") < 3 {
		t.Error("resolve_app must be called inside the loop, not only once at startup")
	}
	// Re-reading per iteration is worth nothing if the iteration only advances on
	// a keypress: the user launches an app, and the shutter is injected only
	// whenever they next happen to press a key. The timeout is what makes the
	// loop follow a launch on its own.
	if !strings.Contains(script, "read -rsn1 -t") {
		t.Error("the loop must poll, or a newly launched app gets no shutter until a key is pressed")
	}
	// The prompt is reprinted on every tick otherwise, scrolling the terminal
	// while the user does nothing.
	if !strings.Contains(script, `"$PROMPT" != "$LAST_PROMPT"`) {
		t.Error("the prompt must only redraw when it changes, or polling scrolls the terminal")
	}
}

// WHY no terminal rendering: inline-image protocols only work in some
// terminals, and everywhere else the user gets either a base64 dump or a
// coloured-block approximation. Copying the PNG next to the script gives a real,
// full-fidelity preview in the editor regardless of terminal.
func TestScreenshotScriptPreviewsByCopyingNotByDrawing(t *testing.T) {
	script := readScreenshotScript(t)
	code := scriptCode(script)

	for _, forbidden := range []string{`]1337;File=inline`, "chafa", "base64 -w0"} {
		if strings.Contains(code, forbidden) {
			t.Errorf("screenshot.sh must not render in the terminal, found %q", forbidden)
		}
	}
	if !strings.Contains(code, `cp -f "$source" "$ROOT/screenshot.png"`) {
		t.Error("the preview must be a copy of the animated PNG next to the script")
	}
	// The copied file is the preview. Serving it over the app's own dev server
	// was tried and removed: it made the recorder's output depend on the app
	// being up, for no gain over opening the file.
	// Matched as a prefix, not the whole line: the message also names the app the
	// aaa-* links now point at, and pinning the full string made adding that a
	// test failure rather than a behaviour change.
	if !strings.Contains(code, `ok "$count frame(s) — $ROOT/screenshot.png`) {
		t.Error("each change must report the preview file path")
	}
	if strings.Contains(code, "screenshot.png?v=") {
		t.Error("the preview is a file, not a URL served by the app")
	}
	if !strings.Contains(code, `source="$APP_DIR/screenshot.png"`) {
		t.Error("the preview must copy the app's animated PNG")
	}
	// A GIF was dropped: one artifact, and it avoids Pillow's GIF encoder
	// silently merging identical consecutive frames.
	if strings.Contains(code, ".gif") {
		t.Error("the recorder produces an animated PNG only — no GIF")
	}
	// A deleted recording must not leave a stale preview behind. It is blanked
	// rather than removed — see TestScreenshotScriptAlwaysPublishesAPreview.
	if !strings.Contains(code, "blank_preview") {
		t.Error("removing the last frame must blank the preview, not leave stale frames on screen")
	}
}

// WHY a blank placeholder rather than no file: with no frames there is nothing
// to copy, and the editor would show either a missing file or — worse — the
// previous app's recording. The preview must always exist and always belong to
// the app currently being recorded.
func TestScreenshotScriptAlwaysPublishesAPreview(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "blank_preview()") {
		t.Error("a blank preview must be produced when the app has no frames")
	}
	// ffmpeg is already a dependency; a placeholder needs no new tooling.
	if !strings.Contains(code, "-f lavfi -i \"color=c=white") {
		t.Error("the blank preview should be generated with ffmpeg, already a dependency")
	}
	// Published as soon as an app becomes current, so a switch cannot leave the
	// previous app's recording on screen.
	if !strings.Contains(code, `preview "$(frame_count)"`) {
		t.Error("the preview must be published when the current app is resolved, not only after a capture")
	}
	// Deleting the last frame blanks the file rather than removing it, so an
	// editor tab open on it keeps working.
	if strings.Contains(code, `rm -f "$ROOT/screenshot.png"`) {
		t.Error("the last delete must blank the preview, not delete it")
	}
}

// The preview copy lands in the repo root and must never be committed.
func TestScreenshotPreviewCopyIsGitignored(t *testing.T) {
	data, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatalf("read .gitignore: %s", err)
	}
	if !strings.Contains(string(data), "/screenshot.png") {
		t.Error("the preview copy at the repo root must be gitignored")
	}
	// The aaa-* links point outside the repo, into $WORKBENCH_DIR, and follow
	// whichever app is current. Committing one would put a per-machine absolute
	// path into the tree.
	for _, link := range []string{"/aaa-screenshot.png", "/aaa-screenshot"} {
		if !strings.Contains(string(data), link) {
			t.Errorf("%s is a symlink into the workbench and must be gitignored", link)
		}
	}
}

// WHY: the recorder follows the current app, so the convenience links next to the
// script have to follow it too — a link left pointing at the previously-launched
// app resolves to the wrong recording, which is worse than no link at all.
func TestScreenshotLinksFollowTheCurrentApp(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "aaa-screenshot.png") || !strings.Contains(code, "aaa-screenshot") {
		t.Fatal("the recorder must maintain the aaa-screenshot links")
	}
	// preview() runs on every app switch, which is what makes the links follow.
	if !strings.Contains(code, "refresh_app_links") {
		t.Error("the links must be refreshed, not created once at startup")
	}
	if !strings.Contains(code, "clear_app_links") {
		t.Error("the links must be dropped when no app is current, or they aim at a stale app")
	}
	// `ln -sf` (without -n) follows an existing symlink-to-directory and creates
	// the new link INSIDE the old target, leaving aaa-screenshot/screenshot in
	// the previous app rather than repointing the link. Removing first is immune.
	if !strings.Contains(code, `rm -rf "${ROOT}/aaa-screenshot.png" "${ROOT}/aaa-screenshot"`) {
		t.Error("the links must be removed before being recreated: ln -sf nests inside the old target")
	}
	if strings.Contains(code, "ln -sf ") {
		t.Error("ln -sf without -n nests the link inside the previous target — remove and recreate instead")
	}
	// Creating the link target would leave an empty screenshot/ directory in the
	// checkout of an app the user never recorded.
	if strings.Contains(code, `mkdir -p "$frames"`) {
		t.Error("the link target must not be created: it pollutes the app checkout for an unrecorded app")
	}
}

// WHY this is the load-bearing invariant: ffmpeg's APNG encoder requires every
// frame in the %05d sequence to share dimensions. Playwright used to guarantee
// that structurally, by being *told* the viewport. getDisplayMedia returns the
// tab's real size instead — which differs per machine AND changes when the user
// resizes the window mid-session. A stray size fails the encode several captures
// after the resize that caused it, so it surfaces far from its cause.
func TestScreenshotShutterNormalizesFrameSize(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	// The size is baked in from the shell, so blank_preview and real frames can
	// never disagree.
	if !strings.Contains(code, "$SHOT_WIDTH") || !strings.Contains(code, "$SHOT_HEIGHT") {
		t.Error("the fixed frame size must be passed into the payload by the shell")
	}
	if !strings.Contains(code, "canvas.width") || !strings.Contains(code, "canvas.height") {
		t.Error("the grabbed frame must be drawn onto a fixed-size canvas before it is posted")
	}
	// WHY width-first with the bottom cropped, rather than contain-fit: a
	// contain fit letterboxes anything whose ratio differs from the canvas, and
	// at 1512x832 onto 600x800 the image fills barely a third of the height —
	// the reported white bands. Fitting the width keeps the layout at full
	// scale and simply ends the frame further down the page.
	if !strings.Contains(code, "var scale = W / bitmap.width") {
		t.Error("the frame must be fitted by width, so the layout is never scaled down to fit height")
	}
	// Anchored top-left: centring a too-tall frame would cut the header off as
	// well as the footer, and the header is what identifies the page.
	if !strings.Contains(code, "ctx.drawImage(bitmap, 0, 0, w, h)") {
		t.Error("the frame must be anchored top-left, so a tall page loses its bottom rather than its header")
	}
	// WHY the pre-capture resize: the bands come from a source whose ratio
	// differs from the canvas, so the source is corrected rather than the
	// result padded. It cannot happen at window.open time — Chrome's "Sharing
	// this tab" bar only steals viewport height once sharing has started, so
	// the size is measured and fixed after the grant and before the grab.
	if !strings.Contains(code, `window.name === "trustable-capture"`) {
		t.Error("only a window the shutter opened may resize itself; a normal tab must be left alone")
	}
	if !strings.Contains(code, "var dw = W - window.innerWidth, dh = H - window.innerHeight") {
		t.Error("the viewport must be driven to the canvas size before the frame is grabbed")
	}
	// Without a slop tolerance the loop chases the last pixel and can oscillate
	// where the browser clamps the size it will accept.
	if !strings.Contains(code, "Math.abs(dw) <= 2 && Math.abs(dh) <= 2") {
		t.Error("the resize must stop within a pixel or two, or it oscillates against a clamping browser")
	}
	// A hardcoded white fill was the reason the bands were so obvious: on a dark
	// app any residual area flashed bright white. The page's own background
	// makes what is left blend in.
	if strings.Contains(code, `ctx.fillStyle = "#ffffff"`) {
		t.Error("the canvas must not be hardcoded white: on a dark app any leftover area is a glaring band")
	}
	if !strings.Contains(code, "ctx.fillStyle = pageBackground()") {
		t.Error("the fill must come from the page's own background colour")
	}
	// An exact width/height constraint fails the whole call with
	// OverconstrainedError where it cannot be met; a letterboxed frame beats no
	// capture at all.
	if !strings.Contains(code, "ideal: W") || !strings.Contains(code, "ideal: H") {
		t.Error("getDisplayMedia must request the canvas size as an ideal constraint, which is what avoids the letterbox at source")
	}
	if strings.Contains(code, "exact: W") {
		t.Error("an exact size constraint fails the capture outright where the browser cannot meet it")
	}
	// getDisplayMedia already returns device pixels; scaling by DPR would give
	// 1200x1600 frames on Retina and reintroduce the variance.
	if strings.Contains(code, "devicePixelRatio") {
		t.Error("getDisplayMedia already returns device pixels — scaling by DPR breaks the fixed size")
	}
}

// WHY: getDisplayMedia exists only in a secure context. Opening the app through
// the Trustable UI serves it from http://vite.<ip>.nip.io — plain HTTP, not
// localhost — where navigator.mediaDevices is undefined, so the click throws a
// bare TypeError that the catch renders as a 1.2s tooltip change. The reported
// symptom was "the shutter does nothing". Rather than only explaining, the
// button opens the same route on the capture origin — localhost, so a secure
// context — in a window sized to the capture canvas. The check must stay
// synchronous: an await ahead of getDisplayMedia would consume the transient
// user activation it requires.
func TestScreenshotShutterOpensASizedWindowOnTheCaptureOrigin(t *testing.T) {
	script := readScreenshotScript(t)
	code := scriptCode(script)

	if !strings.Contains(code, "!navigator.mediaDevices || !navigator.mediaDevices.getDisplayMedia") {
		t.Error("the payload must check for screen-capture support before calling it")
	}
	if !strings.Contains(code, `window.open(target, "trustable-capture"`) {
		t.Error("a non-secure context must open the app on the capture origin, not just report")
	}
	// WHY: the popup exists to give the app a viewport already in the canvas
	// ratio, so the capture is letterboxed edge to edge with no white bars. If
	// the window size were written as its own numbers it could drift from the
	// canvas; deriving it from W/H is what keeps them one truth.
	if !strings.Contains(code, "var vw = W, vh = H") {
		t.Error("the popup size must derive from the capture canvas, not repeat its numbers")
	}
	// WHY: window.open sizes the whole window, so the content area comes out
	// short by the browser chrome — the body would not be in the requested
	// ratio at all. The correction is what makes the ratio true of the body.
	if !strings.Contains(code, "win.resizeBy(dw, dh)") {
		t.Error("the popup must correct for browser chrome so the body matches the canvas ratio")
	}
	// A blocked popup must say so: silence here reads as the button doing
	// nothing, which is the bug this whole path exists to fix.
	if !strings.Contains(code, "allow popups") {
		t.Error("a blocked popup must be reported, not swallowed")
	}
	// The route has to survive the hop, or the redirect lands on "/" and the
	// user loses the page they meant to record.
	if !strings.Contains(code, "location.pathname + location.search + location.hash") {
		t.Error("the redirect must carry the current route, not just the origin")
	}
	// Baked from $URL rather than hardcoded, so the redirect target cannot
	// drift from the origin the recorder actually polls for frames.
	if !strings.Contains(code, "{capture_origin}") {
		t.Error("the redirect target must come from the recorder's own URL")
	}
	if !strings.Contains(script, `"$SHOT_HEIGHT" "$URL"`) {
		t.Error("$URL must be passed into the payload rewriter")
	}
	// Without this the button would reload forever on a browser that simply has
	// no screen capture.
	if !strings.Contains(code, `location.origin === "{capture_origin}"`) {
		t.Error("the redirect must not loop when the page is already on the capture origin")
	}
	// The check has to precede the call it guards.
	guard := strings.Index(code, "!navigator.mediaDevices")
	call := strings.Index(code, "getDisplayMedia({")
	if guard < 0 || call < 0 || guard > call {
		t.Error("the support check must come before the getDisplayMedia call")
	}
}

// WHY: the button has to stay clickable over whatever the app puts on screen,
// and a high z-index alone cannot do it. A <dialog open> or any [popover]
// renders in the browser's TOP LAYER, which paints above the entire z-index
// stack — INT_MAX included — so the shutter would sit under any modal. The
// button therefore joins the top layer itself, as a *manual* popover (an auto
// one light-dismisses on the first outside click, closing the shutter). Top
// layer order is entry order, so it must re-enter whenever a newer dialog
// opens, which is what the observer is for. The z-index still carries the
// ordinary-stacking case on browsers without popover support.
func TestScreenshotShutterOutranksModals(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "z-index:2147483001") {
		t.Error("the button needs a high z-index for the non-popover stacking case")
	}
	// Not INT_MAX: squatting on the ceiling makes the button un-overridable
	// inside someone else's app.
	if strings.Contains(code, "z-index:2147483647") {
		t.Error("z-index must sit one above the toolkit launcher, not at INT_MAX")
	}
	if !strings.Contains(code, "showPopover()") {
		t.Error("the button must enter the top layer, or any modal paints over it")
	}
	if !strings.Contains(code, `btn.popover = "manual"`) {
		t.Error("the popover must be manual: an auto popover closes on the first outside click")
	}
	// Without re-entry the button falls behind any dialog opened after it.
	if !strings.Contains(code, "MutationObserver") {
		t.Error("the button must re-enter the top layer when a newer dialog opens")
	}
	// showPopover throws where popover is unsupported; that must not break the
	// button, which then simply relies on its z-index.
	if !strings.Contains(code, `catch (e) {{ /* no popover support: the z-index path stands */ }}`) {
		t.Error("popover entry must degrade gracefully where it is unsupported")
	}
	// An opened <dialog> is what actually steals the top layer, so it is the
	// case the observer exists to catch.
	if !strings.Contains(code, "dialog[open]") {
		t.Error("the observer must notice opened dialogs, which is what takes the top layer")
	}
	// inset is a shorthand for top/right/bottom/left, and the UA [popover] rule
	// sets inset:0 plus centering margins. Declared AFTER top/right in the same
	// cssText it silently wipes the corner placement — the button would sit
	// centred on the page, over the app's content, in every session.
	inset := strings.Index(code, "inset:auto")
	corner := strings.Index(code, "top:12px")
	if inset < 0 || corner < 0 || inset > corner {
		t.Error("inset:auto must precede top/right in the cssText, or the popover UA rule moves the button")
	}
}

// WHY: apps scaffolded with @agentic-react/vite render an element-selector
// toolbar over the page, which would otherwise appear in every frame — and so
// would our own shutter button, which sits on the very page being captured.
// Both hide mechanisms are needed: hideToolkit() is the supported API, and the
// data-agentic-react-* rule covers a build that lacks it. Those elements carry
// no id or class, so the attributes are the only stable handle.
func TestScreenshotShutterHidesChromeIncludingItself(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "hideToolkit") {
		t.Error("the injected script must call the plugin's hideToolkit() runtime API")
	}
	if !strings.Contains(code, "data-agentic-react-toolkit") {
		t.Error("the CSS fallback must target the data-agentic-react-* attributes")
	}
	if !strings.Contains(code, "data-agentic-react-launcher") {
		t.Error("the launcher button is a separate element and must be hidden too")
	}
	// An app without the plugin must still capture rather than throw.
	if !strings.Contains(code, "?.hideToolkit?.()") {
		t.Error("the call must be optional-chained: most apps have no such plugin")
	}
	// The button draws on the page it captures. Without this it is in every frame.
	if !strings.Contains(code, "data-trustable-shutter") {
		t.Error("the shutter must hide ITSELF, or it appears in every frame")
	}
	// Hiding the button is not enough while it is a popover: a popover left in
	// the top layer keeps a surface over the page being captured.
	if !strings.Contains(code, "hidePopover()") {
		t.Error("the shutter must leave the top layer while capturing, not only turn invisible")
	}
	// A style change needs a composited frame before it reaches the video stream,
	// and the toolkit animates out rather than snapping.
	if !strings.Contains(code, "requestAnimationFrame") {
		t.Error("the capture must wait for a painted frame after hiding")
	}
	// requestVideoFrameCallback fires only once a NEW frame is presented. Without
	// it a buffered pre-hide frame can be grabbed, baking the toolkit into an
	// occasional frame — an intermittent race that is very hard to diagnose.
	if !strings.Contains(code, "requestVideoFrameCallback") {
		t.Error("the frame must be taken after a fresh presentation, or a pre-hide frame can be captured")
	}
	// A capture that throws must not leave the user's toolkit hidden forever.
	if !strings.Contains(code, "showChrome(style)") {
		t.Error("hidden chrome must be restored in a finally, or a failed capture disfigures the app")
	}
}

// WHY the injection exists at all: the recorder cannot reproduce the user's tab
// — its form input, open modals, scroll position, and tab-storage auth. Driving
// a fresh browser reproduced the route but not the state, so the recording
// showed a page the user had never seen. The page must therefore capture itself,
// which means shipping a button and an upload endpoint into the running app.
func TestScreenshotScriptInjectsTheShutter(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "transformIndexHtml") {
		t.Error("the client script must inject via transformIndexHtml, so the app's index.html on disk is never touched")
	}
	if !strings.Contains(code, "getDisplayMedia") {
		t.Error("the page must capture itself, which is what reproduces the user's actual tab")
	}
	// WHY a dev-server middleware and not the app's MCP server: get-html-elements
	// truncates its reply at 800 characters, so an image could never come back
	// that way, and MCP custom tools would need a zod import in the user's config
	// and only exist on @agentic-react apps.
	if !strings.Contains(code, "configureServer") {
		t.Error("the upload endpoint must be mounted with configureServer")
	}
	if !strings.Contains(code, "server.middlewares.use") {
		t.Error("the endpoint must be a connect middleware on the dev server")
	}
	// WHY not `return () => {...}` from configureServer: a returned function
	// mounts AFTER Vite's internal middlewares, and the SPA fallback would then
	// answer the endpoint with index.html before we ever see the request.
	if strings.Contains(code, "return () => {") {
		t.Error("the middleware must mount before Vite's SPA fallback, not after")
	}
	// One definition, shared by the injected plugin and the curl retrieval.
	if !strings.Contains(code, "SHOT_ENDPOINT=") {
		t.Error("the endpoint path must be a single variable shared by the plugin and the retrieval")
	}
	// The block is delimited so removal is exact and re-injection is idempotent.
	if !strings.Contains(code, "REPORTER_BEGIN=") || !strings.Contains(code, "REPORTER_END=") {
		t.Error("the injected block needs begin/end markers for idempotent injection and exact removal")
	}
	// The route reporter is obsolete: the tab is already on the page to capture.
	if strings.Contains(code, "__trustable_location__") {
		t.Error("the route reporter is obsolete — the page captures itself, so there is no route to report")
	}
	// The factory name appears in the injection AND in remove_reporter's replace
	// string. If they drift, removal silently no-ops and the user's vite.config
	// keeps a call to a plugin that is no longer defined.
	if strings.Count(code, "trustableScreenshotShutter") < 3 {
		t.Error("the factory name must match across injection and removal, or removal silently no-ops")
	}
}

// WHY: the starter templates generate configs with no `plugins` array at all,
// and in the function form — defineConfig(({ mode }) => { ... return {...} }) —
// there is not even a top-level object literal to anchor to. Refusing those
// left real apps (observed: the `tetris` template) with no shutter and no
// explanation, which is exactly the "I launched it and there is no button"
// report this guards against.
func TestScreenshotScriptAddsAMissingPluginsArray(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	// The old hard refusal must not come back.
	if strings.Contains(code, `grep -qE 'plugins:[[:space:]]*\[' "$config" || return 1`) {
		t.Error("a config without a plugins array must gain one, not be refused")
	}
	if !strings.Contains(code, `re.search(r'(?m)^\s*return\s*\{', source)`) {
		t.Error("the function form of defineConfig must be handled, not just an object literal")
	}
	// WHY the anchor is chosen before the factory is spliced in: the factory
	// contains a `return {` of its own, so searching the combined text finds
	// that one first — the plugin then declares itself as its own plugins
	// array, which parses cleanly and does absolutely nothing.
	if !strings.Contains(code, "edit_at += len(prelude)") {
		t.Error("the insertion point must be offset past the spliced factory, or the plugin is added to itself")
	}
	// An added array must be removed whole; leaving `plugins: [],` behind is not
	// byte-exact and keeps the file dirty in the user's git status.
	if !strings.Contains(code, `plugins: \[trustableScreenshotShutter\(\)\],\n`) {
		t.Error("an array added by the injection must be removed entirely on cleanup")
	}
}

// WHY skip-worktree and not .gitignore: both files are tracked, and an ignore
// rule has no effect on a tracked file — adding them to .gitignore leaves them
// showing as modified, and untracking them to make the rule bite would delete
// vite.config.ts from the app's repo. skip-worktree is per-file, so the user's
// own edits stay visible.
func TestScreenshotScriptHidesOnlyTheConfigFiles(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "update-index --skip-worktree") {
		t.Error("the injected config must be hidden with git update-index --skip-worktree")
	}
	if !strings.Contains(code, "update-index --no-skip-worktree") {
		t.Error("the flag must be clearable, or git silently refuses to update the file forever")
	}
	if !strings.Contains(code, "HIDDEN_FILES=(vite.config.ts .gitignore)") {
		t.Error("only vite.config.ts and .gitignore may be hidden")
	}
	// Only a tracked file can carry the flag; ls-files guards the call.
	if !strings.Contains(code, "ls-files --error-unmatch") {
		t.Error("the flag must only be set on tracked files")
	}
}

// The injection is working state. A killed session must not strand a modified
// config or a hidden file, so cleanup runs on every exit path and stale state is
// cleared before injecting.
func TestScreenshotScriptCleansUpTheInjection(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "trap cleanup_reporter EXIT INT TERM") {
		t.Error("the reporter must be removed on every exit path, including ^C")
	}
	if !strings.Contains(code, "unhide_all") {
		t.Error("stale skip-worktree flags from a killed session must be cleared")
	}
	if !strings.Contains(code, "remove_reporter") {
		t.Error("the injected block must be removed, not left in the user's config")
	}
}

// WHY: the frame now arrives from the user's own tab over HTTP, so the recorder
// is a receiver. ENTER collects whatever is waiting and returns immediately — a
// blocking wait would make 'q' unreachable, and an empty queue is the normal
// state between clicks rather than an error.
func TestScreenshotScriptCollectsPostedFrames(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "fetch_frame") {
		t.Error("frames must be retrieved from the dev-server endpoint")
	}
	if !strings.Contains(code, `SHOT_URL="${URL%/}$SHOT_ENDPOINT"`) {
		t.Error("the retrieval URL must be the app URL joined with the endpoint path")
	}
	// WHY the magic-number check: a dev server whose plugin is not mounted
	// answers the endpoint with index.html and a 200. Without this, that HTML
	// lands in screenshot/<stamp>.png and ffmpeg fails on a later regenerate,
	// far from the cause.
	if !strings.Contains(code, "89504e470d0a1a0a") {
		t.Error("a retrieved frame must be verified as a PNG, or the SPA fallback's HTML is saved as a frame")
	}
	// Playwright no longer captures: a fresh browser cannot reproduce the user's
	// tab, which is the whole reason for this design.
	if strings.Contains(code, "tests/screenshot.mjs") {
		t.Fatal("the capture must come from the user's own tab, not a fresh Playwright browser")
	}
	// An empty queue must say so and return, not stall the loop.
	if !strings.Contains(code, "no frame waiting") {
		t.Error("an empty queue must be reported, not treated as a failure")
	}
}

// WHY IndexedDB rather than a plain variable: a frame captured moments before
// Vite hot-reloads, or before the dev server restarts, would simply vanish.
// IndexedDB survives both, holds Blobs without a base64 round-trip, and queues
// several clicks so a burst becomes several frames rather than one.
func TestScreenshotShutterQueuesFramesDurably(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "indexedDB.open") {
		t.Error("captured frames must be queued in IndexedDB, or a reload loses them")
	}
	// Stored first, uploaded second: a POST that fails must leave the frame
	// recoverable rather than dropping it.
	if !strings.Contains(code, "drain()") {
		t.Error("queued frames must be re-uploaded, not dropped when the first POST fails")
	}
	// getDisplayMedia needs transient user activation, and an await consumes it.
	// The database is therefore opened at load — moving that open into the click
	// handler makes captures fail with NotAllowedError on some machines only.
	if !strings.Contains(code, "var dbp = new Promise") {
		t.Error("the IndexedDB connection must be opened at load, not inside the click handler")
	}
}

// WHY the GET is destructive: the recorder saves whatever it retrieves, so a
// frame left in the server's queue would be saved again on the next collect,
// producing a recording of duplicates.
func TestScreenshotEndpointHandsEachFrameOutOnce(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "frames.shift()") {
		t.Error("GET must remove the frame it returns, or the next collect saves it again")
	}
	// A user clicking while the recorder is not polling must not grow the dev
	// server's heap without bound. The shell drain loop is capped to match.
	if !strings.Contains(code, "MAX_FRAMES") {
		t.Error("the in-memory queue must be bounded")
	}
	// The only privacy control in the design: a full-screen share would post
	// whatever else is on the user's screen into a git-staged file.
	if !strings.Contains(code, `displaySurface !== "browser"`) {
		t.Error("a non-tab share must be refused, or the capture can leak the user's screen")
	}
}
