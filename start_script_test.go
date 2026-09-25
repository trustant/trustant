// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

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

	if !strings.Contains(start, `chown -Rvf trustant:trustant "$HOME/workspace"`) {
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

	// start.sh runs as root, the server reads the lock as trustant. An
	// existing-but-unreadable lock is indistinguishable from a stale one and
	// would hang the splash, so ownership is set at creation time rather than
	// left to the background chown to reach.
	if !strings.Contains(start, `chown trustant:trustant "$(dirname "$INIT_LOCK")" "$INIT_LOCK"`) {
		t.Fatal("image/start.sh must make the init lock owned by trustant so the server can read it")
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
func TestStartScriptResolvesPackageFromOpenServerlessIndex(t *testing.T) {
	content, err := os.ReadFile("start.sh")
	if err != nil {
		t.Fatalf("read start.sh: %s", err)
	}
	script := string(content)

	if !strings.Contains(script, `OPENSERVERLESS_INDEX="https://openserverless.nuvolaris.download/index.json"`) {
		t.Error("start.sh must resolve the package through the OpenServerless index.json")
	}
	// The landing endpoint served whatever the current release was, with no
	// version selector, so nothing in the repo could pin what got installed.
	// Any reappearance is a regression back to unpinned installs.
	if strings.Contains(script, "landing.nuvolaris.org") {
		t.Error("start.sh must not download from landing.nuvolaris.org — it serves an unpinned release")
	}
	if strings.Contains(script, "landing2") {
		t.Error("start.sh must not use landing2.nuvolaris.org — it serves an older release")
	}

	// openserverless.txt selects what actually gets installed, so unlike
	// version.txt it must be a hard pin: no "unknown" fallback, and no silent
	// fallback onto the index's "latest" key.
	if !strings.Contains(script, `OPENSERVERLESS_VERSION="$(head -n1 openserverless.txt`) {
		t.Error("start.sh must read the pinned package version from openserverless.txt")
	}
	if !strings.Contains(script, `|| fail "openserverless.txt not found or empty`) {
		t.Error("start.sh must fail (not warn) when openserverless.txt is missing or empty")
	}
	if !strings.Contains(script, `is not published.`) {
		t.Error("start.sh must fail loudly when the pinned version is absent from the index")
	}
	if !strings.Contains(script, "index_versions_for") {
		t.Error("start.sh must list the available versions when the pin does not resolve")
	}

	// ensure_deb runs on the HOST and before ensure_jq on both paths, and
	// ensure_jq installs jq in the VM — so it can never help a Mac parse the
	// index. A jq call inside the resolver would break provisioning on a clean
	// Mac, which is exactly the host that cannot be caught by CI here.
	resolver := script[strings.Index(script, "index_url_for()"):strings.Index(script, "# Copy the cached")]
	if strings.Contains(resolver, "jq ") || strings.Contains(resolver, "jq\n") {
		t.Error("the index resolver must not depend on jq — it runs on the host before jq exists")
	}

	// The cache is keyed by the pinned version, and a cache hit must not need
	// the network: a fully-cached run cannot depend on the bucket being up.
	if !strings.Contains(script, `DEB_FILE="${DIST_DIR}/openserverless_${OPENSERVERLESS_VERSION}_${DEB_ARCH}.deb"`) {
		t.Error("start.sh must cache the deb under its pinned version and arch")
	}
	if !strings.Contains(script, `ok "Using cached package: $DEB_FILE"
    return 0`) {
		t.Error("a cache hit must return before fetching the index (no network on cached runs)")
	}

	// The package installed is now openserverless, so every installed-check must
	// name it; a leftover `dpkg -l trustant` would never match and would
	// re-run the ~3GB install on every invocation.
	if strings.Contains(script, "dpkg -l trustant") {
		t.Error("start.sh must check for the openserverless package, not trustant")
	}

	if !strings.Contains(script, "curl -fL --retry 3") {
		t.Error("start.sh must keep --retry on the download")
	}
	if !strings.Contains(script, `TMP_DEB="${DEB_FILE}.part"`) {
		t.Error("start.sh must stage the download through a .part file")
	}

	// version.txt keeps its own, separate meaning (the app release identity
	// shared with build.sh/hotfix.sh/run.sh) and must not be conflated with the
	// package pin above.
	if !strings.Contains(script, `TRUSTANT_VERSION="$(head -n1 version.txt`) {
		t.Error("start.sh must still derive TRUSTANT_VERSION from version.txt")
	}
	if !strings.Contains(script, `TRUSTANT_VERSION="${TRUSTANT_VERSION#v}"`) {
		t.Error("start.sh must strip the leading v from version.txt (v0.4.0 -> 0.4.0)")
	}
}
