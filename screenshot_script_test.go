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

func readScreenshotCapture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("tests/screenshot.mjs")
	if err != nil {
		t.Fatalf("read tests/screenshot.mjs: %s", err)
	}
	return string(data)
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

// The workbench is a live user checkout with arbitrary dirty state. An unscoped
// commit would sweep the user's work-in-progress into a screenshot commit.
func TestScreenshotScriptCommitsOnlyTheScreenshotPaths(t *testing.T) {
	script := readScreenshotScript(t)
	if !strings.Contains(script, "add -- screenshot screenshot.png") {
		t.Error("git add must be scoped to the screenshot paths")
	}
	if !strings.Contains(script, `commit -q -m "$message" -- screenshot screenshot.png`) {
		t.Error("git commit must carry the screenshot pathspec")
	}
	if strings.Contains(scriptCode(script), "commit -a") {
		t.Fatal("screenshot.sh must never commit the whole workbench")
	}
	if !strings.Contains(script, "diff --cached --quiet") {
		t.Error("an unchanged tree must skip the commit rather than abort under set -e")
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
	// A deleted recording must not leave a stale preview behind.
	if !strings.Contains(code, `rm -f "$ROOT/screenshot.png"`) {
		t.Error("removing the last frame must delete the preview copy too")
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

// Frames must share dimensions or the animation is corrupt, so the viewport is
// fixed and fullPage is off.
func TestScreenshotCaptureUsesFixedViewportAndNoSandbox(t *testing.T) {
	capture := readScreenshotCapture(t)
	if !strings.Contains(capture, `"--no-sandbox"`) {
		t.Error("chromium must launch with --no-sandbox in the VM")
	}
	if !strings.Contains(capture, "TRUSTABLE_SCREENSHOT_WIDTH || 600") {
		t.Error("the capture must default to a 600px wide viewport")
	}
	if !strings.Contains(capture, "TRUSTABLE_SCREENSHOT_HEIGHT || 800") {
		t.Error("the capture must default to an 800px tall viewport")
	}
	if !strings.Contains(capture, "fullPage: false") {
		t.Error("fullPage must stay false: a full-page shot varies with content and breaks the animation")
	}
	if !strings.Contains(capture, "await browser.close()") {
		t.Error("the browser must always be closed, or chromium leaks across loop iterations")
	}
}

// WHY: apps scaffolded with @agentic-react/vite render an element-selector
// toolbar over the page, which would otherwise appear in every frame. Both
// mechanisms are needed — hideToolkit() is the supported API, and the
// data-agentic-react-* rule covers a build that lacks it. The elements carry no
// id or class, so those attributes are the only stable handle.
func TestScreenshotCaptureHidesTheElementSelector(t *testing.T) {
	capture := readScreenshotCapture(t)
	if !strings.Contains(capture, "hideToolkit") {
		t.Error("the capture must call the plugin's hideToolkit() runtime API")
	}
	if !strings.Contains(capture, "data-agentic-react-toolkit") {
		t.Error("the CSS fallback must target the data-agentic-react-* attributes")
	}
	if !strings.Contains(capture, "data-agentic-react-launcher") {
		t.Error("the launcher button is a separate element and must be hidden too")
	}
	// An app without the plugin must still capture rather than throw.
	if !strings.Contains(capture, "?.hideToolkit?.()") {
		t.Error("the call must be optional-chained: most apps have no such plugin")
	}
}

// WHY the reporter exists: the route lives in a browser cookie no server code
// reads, and the preview iframe is cross-origin, so nothing on the Trustable
// side can see where the user navigated. Without it every frame is the app root.
func TestScreenshotScriptInjectsTheRouteReporter(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, "transformIndexHtml") {
		t.Error("the reporter must inject via transformIndexHtml, so the app's index.html on disk is never touched")
	}
	if !strings.Contains(code, "__trustable_location__") {
		t.Error("the reporter must publish the location in a known meta element")
	}
	// A HashRouter app keeps the route in the hash, so pathname alone is wrong.
	if !strings.Contains(code, "location.pathname + location.search + location.hash") {
		t.Error("the reported route must include search and hash, not just pathname")
	}
	// Client-side navigation does not fire hashchange or popstate on its own.
	for _, event := range []string{"hashchange", "popstate", "history.pushState"} {
		if !strings.Contains(code, event) {
			t.Errorf("the reporter must keep the route current on %s", event)
		}
	}
	// The block is delimited so removal is exact and re-injection is idempotent.
	if !strings.Contains(code, "REPORTER_BEGIN=") || !strings.Contains(code, "REPORTER_END=") {
		t.Error("the injected block needs begin/end markers for idempotent injection and exact removal")
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

// The capture must follow the reported route, and must still work for an app
// with no reporter at all.
func TestScreenshotScriptCapturesTheReportedRoute(t *testing.T) {
	code := scriptCode(readScreenshotScript(t))

	if !strings.Contains(code, `target_url="${URL%/}$route"`) {
		t.Error("the capture URL must be the app URL joined with the reported route")
	}
	if !strings.Contains(code, `route="${detected:-/}"`) {
		t.Error("an unavailable route must fall back to /, not fail the capture")
	}
	if !strings.Contains(code, `node "$ROOT/tests/screenshot.mjs" "$target_url"`) {
		t.Error("the capture must use the route-aware URL")
	}

	// The URL is announced BEFORE the shutter, and the fallback is called out.
	// Printing only on success, or staying silent when the route is "/", makes a
	// wrong capture impossible to diagnose: you cannot tell "never detected" from
	// "detected and wrong".
	if !strings.Contains(code, `ok "capturing $target_url"`) {
		t.Error("the URL being captured must be printed before the capture")
	}
	if !strings.Contains(code, "no route reported") {
		t.Error("falling back to / must say so, not silently capture the app root")
	}
}

// The route is read over MCP because MCP reflects the tab the user has open;
// loading the page ourselves would only report the URL we just requested.
func TestScreenshotRouteReaderUsesMCP(t *testing.T) {
	data, err := os.ReadFile("tests/screenshot-route.mjs")
	if err != nil {
		t.Fatalf("read tests/screenshot-route.mjs: %s", err)
	}
	reader := string(data)

	if !strings.Contains(reader, "get-html-elements") {
		t.Error("the route must be read with the get-html-elements MCP tool")
	}
	if !strings.Contains(reader, "mcp-session-id") {
		t.Error("the MCP transport requires a session id from initialize")
	}
	if !strings.Contains(reader, "domPreview") {
		t.Error("the meta element's markup comes back as domPreview")
	}
	// This runs between a keypress and the shutter: a hung server must not stall
	// the recorder.
	if !strings.Contains(reader, "setTimeout") {
		t.Error("the MCP calls need a timeout so a hung server cannot stall a capture")
	}
	if !strings.Contains(reader, `route.startsWith("/")`) {
		t.Error("only absolute in-app routes may be captured")
	}
}

// setup.sh is guarded by setup_test.go against playwright/chromium; the install
// belongs here instead. This asserts the install actually lives in this script.
func TestScreenshotScriptOwnsItsBrowserInstall(t *testing.T) {
	script := readScreenshotScript(t)
	if !strings.Contains(script, "npx playwright install chromium") {
		t.Error("screenshot.sh must install chromium on demand")
	}
	if !strings.Contains(script, "TRUSTABLE_SCREENSHOT_SKIP_BROWSER_INSTALL") {
		t.Error("the browser install needs a skip flag, like the e2e harness has")
	}
	if strings.Contains(script, "playwright install-deps") {
		t.Fatal("install-deps is a large unattended sudo install and must stay manual")
	}
}
