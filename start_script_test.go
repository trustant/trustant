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
