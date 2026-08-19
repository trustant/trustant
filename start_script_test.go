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
