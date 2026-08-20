package main

import (
	"os"
	"strings"
	"testing"
)

// The workbench is ephemeral scratch space: it MUST NOT survive a pod restart,
// because committing is the only thing that makes work durable. An older image
// symlinked $HOME/workbench onto the persistent $HOME/workspace volume, which
// silently made it survive. That is exactly the kind of invariant that is easy
// to regress in a shell script and hard to notice at runtime — the pod just
// quietly keeps state again — so guard the entrypoint's content directly.
func TestStartScriptKeepsWorkbenchEphemeral(t *testing.T) {
	content, err := os.ReadFile("image/start.sh")
	if err != nil {
		t.Fatalf("read image/start.sh: %s", err)
	}
	start := string(content)

	for _, line := range strings.Split(start, "\n") {
		code := strings.TrimSpace(line)
		if code == "" || strings.HasPrefix(code, "#") {
			continue
		}
		if strings.Contains(code, "workspace/workbench") {
			t.Fatalf("image/start.sh must not touch the persistent workspace/workbench path: %q", code)
		}
		if strings.Contains(code, "ln ") && strings.Contains(code, "workbench") {
			t.Fatalf("image/start.sh must not recreate a workbench symlink: %q", code)
		}
	}

	if !strings.Contains(start, `rm -rf "$HOME/workbench"`) {
		t.Fatal("image/start.sh must clear $HOME/workbench on every start, including a stale symlink from an older image")
	}
	if !strings.Contains(start, `mkdir -p "$HOME/workbench"`) {
		t.Fatal("image/start.sh must recreate $HOME/workbench as an empty real directory")
	}
}

// The startup chown must stay narrow, non-blocking, and visible. Its scope is
// $HOME/workspace — the only hostPath mount whose ownership can be wrong;
// chowning bare $HOME walked the image's own ~/.local, ~/.ops and baked-in
// node_modules for nothing and blocked the boot while doing it. All three
// properties are one-line regressions in a shell script and invisible at
// runtime (the pod just boots slowly again), so guard the entrypoint directly.
func TestStartScriptChownIsNarrowBackgroundedAndTracked(t *testing.T) {
	content, err := os.ReadFile("image/start.sh")
	if err != nil {
		t.Fatalf("read image/start.sh: %s", err)
	}
	start := string(content)

	for _, line := range strings.Split(start, "\n") {
		code := strings.TrimSpace(line)
		if code == "" || strings.HasPrefix(code, "#") {
			continue
		}
		// The scope must stay $HOME/workspace: a recursive chown of bare $HOME
		// is the regression this guards.
		if strings.Contains(code, "chown") && strings.Contains(code, "-R") &&
			(strings.Contains(code, `"$HOME"`) || strings.HasSuffix(code, "$HOME")) {
			t.Fatalf("image/start.sh must not recursively chown bare $HOME, only $HOME/workspace: %q", code)
		}
	}

	if !strings.Contains(start, `chown -Rvf trustable:trustable "$HOME/workspace"`) {
		t.Fatal("image/start.sh must chown $HOME/workspace recursively")
	}
	if !strings.Contains(start, ") &") {
		t.Fatal("image/start.sh must background the chown so supervisord starts immediately")
	}

	// The lock must exist before the subshell is backgrounded, or the splash
	// can poll, see no lock, and wrongly conclude init has already finished.
	lockCreated := strings.Index(start, `echo 0 > "$INIT_LOCK"`)
	backgrounded := strings.Index(start, ") &")
	if lockCreated < 0 {
		t.Fatal("image/start.sh must create the init lock with an initial count")
	}
	if lockCreated > backgrounded {
		t.Fatal("image/start.sh must create the init lock before backgrounding the chown")
	}

	// start.sh runs as root, the server reads the lock as trustable. An
	// existing-but-unreadable lock is indistinguishable from a stale one and
	// would hang the splash, so ownership is set at creation time rather than
	// left to the background chown to reach.
	if !strings.Contains(start, `chown trustable:trustable "$(dirname "$INIT_LOCK")" "$INIT_LOCK"`) {
		t.Fatal("image/start.sh must make the init lock owned by trustable so the server can read it")
	}
	if !strings.Contains(start, `chmod 644 "$INIT_LOCK"`) {
		t.Fatal("image/start.sh must make the init lock readable by the server user")
	}

	// Two removals, deliberately: the trap covers error and signal paths, the
	// explicit rm covers the normal end of the init loop. Losing either can
	// strand the lock and leave the splash waiting forever.
	if !strings.Contains(start, `trap 'rm -f "$INIT_LOCK"' EXIT`) {
		t.Fatal("image/start.sh must trap EXIT to remove the init lock on failure or signal")
	}
	trap := strings.Index(start, `trap 'rm -f "$INIT_LOCK"' EXIT`)
	if strings.Count(start[trap:backgrounded], `rm -f "$INIT_LOCK"`) < 2 {
		t.Fatal("image/start.sh must remove the init lock at the end of the init loop, not only via the trap")
	}
}

// The host-side provisioning script (root start.sh, not the image entrypoint
// guarded above) downloads a ~4GB .deb. Two invariants there are easy to
// regress and expensive to notice, because the failure is a silently wrong or
// mislabeled 4GB cache entry rather than an error:
//
//  1. The host. landing2.nuvolaris.org still answers, but serves an older
//     release than landing.nuvolaris.org — reverting the URL would cache a
//     stale package under the current version's filename.
//  2. The version. version.txt is the single release identity (shared with
//     build.sh/hotfix.sh/run.sh) and holds the tagged form "v0.4.0"; the deb
//     filename needs the numeric "0.4.0". A hardcoded copy in start.sh drifts
//     silently whenever the tag moves.
func TestStartScriptDownloadsCurrentReleaseFromLandingHost(t *testing.T) {
	content, err := os.ReadFile("start.sh")
	if err != nil {
		t.Fatalf("read start.sh: %s", err)
	}
	script := string(content)

	if !strings.Contains(script, `DOWNLOAD_BASE="https://landing.nuvolaris.org/api/my/v1/download"`) {
		t.Error("start.sh must download the package from landing.nuvolaris.org")
	}
	// Substring-safe: every landing2 occurrence is a real regression, and
	// "landing.nuvolaris.org" does not contain "landing2".
	if strings.Contains(script, "landing2") {
		t.Error("start.sh must not use landing2.nuvolaris.org — it serves an older release")
	}

	if !strings.Contains(script, `TRUSTABLE_VERSION="$(head -n1 version.txt`) {
		t.Error("start.sh must derive TRUSTABLE_VERSION from version.txt, not hardcode it")
	}
	if !strings.Contains(script, `TRUSTABLE_VERSION="${TRUSTABLE_VERSION#v}"`) {
		t.Error("start.sh must strip the leading v from version.txt (v0.4.0 -> 0.4.0)")
	}
	// The fallback keeps a worktree without version.txt provisioning instead of
	// caching to a nameless trustable__<arch>.deb, but it must be loud.
	if !strings.Contains(script, `warn "version.txt not found or empty`) {
		t.Error("start.sh must warn when version.txt is missing or empty")
	}

	// The download endpoint is an OpenWhisk action: a cold start can answer the
	// first request with HTTP 400 before serving normally, so --retry is what
	// makes provisioning reliable, and .part staging is what stops an
	// interrupted transfer from poisoning the cache.
	if !strings.Contains(script, "curl -fL --retry 3") {
		t.Error("start.sh must keep --retry on the download (cold-start 400s)")
	}
	if !strings.Contains(script, `TMP_DEB="${DEB_FILE}.part"`) {
		t.Error("start.sh must stage the download through a .part file")
	}
}
