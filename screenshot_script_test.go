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
// the one-second frame delay entirely. Pillow is the only thing here that
// writes a correct APNG, so a "simplification" back to ImageMagick must fail
// the tests rather than quietly produce a broken animation.
func TestScreenshotScriptDoesNotUseImageMagickForAPNG(t *testing.T) {
	script := readScreenshotScript(t)
	code := scriptCode(script)
	if strings.Contains(code, "apng:") {
		t.Fatal("screenshot.sh must not build APNGs with ImageMagick: IM6 delegates apng: to ffmpeg, which re-times frames at 25fps")
	}
	if strings.Contains(code, "convert -delay") {
		t.Fatal("screenshot.sh must not build animations with ImageMagick convert")
	}
	if !strings.Contains(script, "python3-pil") {
		t.Fatal("screenshot.sh must install Pillow, which is what writes both animations")
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

// One second per frame, looping forever, in both formats.
func TestScreenshotScriptWritesOneSecondLoopingFrames(t *testing.T) {
	script := readScreenshotScript(t)
	if !strings.Contains(script, "duration=1000") {
		t.Error("the animation must be written with duration=1000 (one second per frame)")
	}
	if !strings.Contains(script, "loop=0") {
		t.Error("the animation must be written with loop=0 (loop forever)")
	}
	if !strings.Contains(script, "save_all=True") {
		t.Error("save_all=True is required to write multi-frame images")
	}
}

// WHY: Pillow merges identical consecutive frames into one frame with a summed
// delay. Capturing twice before the app changes — a normal thing to do — would
// otherwise yield one two-second frame instead of two one-second frames, and the
// animation would disagree with the files on disk. disposal=1/blend=0 keeps
// every frame. This was observed, not theorised.
func TestScreenshotScriptKeepsIdenticalFrames(t *testing.T) {
	script := readScreenshotScript(t)
	if !strings.Contains(script, "disposal=1") || !strings.Contains(script, "blend=0") {
		t.Fatal("the APNG save needs disposal=1 and blend=0, or Pillow collapses identical consecutive captures into a single frame")
	}
}

// Deleting the last remaining frame must remove the animations, not write a
// zero-frame file.
func TestScreenshotScriptHandlesTheEmptyCase(t *testing.T) {
	script := readScreenshotScript(t)
	if !strings.Contains(script, "if not files:") {
		t.Error("screenshot.sh must handle the no-frames-left case explicitly")
	}
	if !strings.Contains(script, "os.remove(apng)") {
		t.Error("the last delete must remove screenshot.png")
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
	// An APNG only animates in a browser — the editor's image preview stops at
	// the first frame — so the loop prints a URL the app's own Vite server
	// already serves. The query string defeats the browser cache.
	if !strings.Contains(code, `$URL/screenshot.png?v=$count`) {
		t.Error("each change must print a cache-busted URL for viewing the animation in a browser")
	}
	if !strings.Contains(code, "Simple Browser: Show") {
		t.Error("the startup help must name the VS Code command that opens the URL")
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
