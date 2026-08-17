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
	if !strings.Contains(script, "read -rsn1 key || ") {
		t.Error("a failed read (EOF) must exit the loop instead of spinning")
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
	if !strings.Contains(code, `ok "$count frame(s) — $ROOT/screenshot.png"`) {
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
	// Contain-fit, not cover: the tab is landscape and the canvas portrait, so a
	// crop would discard most of the width — including the app's left nav.
	if !strings.Contains(code, "Math.min(W / bitmap.width, H / bitmap.height)") {
		t.Error("the frame must be contain-fit, not stretched or cropped")
	}
	// The letterbox bars and ffmpeg's blank placeholder must be the same colour,
	// or the animation flashes between white and black.
	if !strings.Contains(code, `ctx.fillStyle = "#ffffff"`) {
		t.Error("the canvas must be filled white to match blank_preview's ffmpeg placeholder")
	}
	// getDisplayMedia already returns device pixels; scaling by DPR would give
	// 1200x1600 frames on Retina and reintroduce the variance.
	if strings.Contains(code, "devicePixelRatio") {
		t.Error("getDisplayMedia already returns device pixels — scaling by DPR breaks the fixed size")
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
