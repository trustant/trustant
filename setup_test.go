package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupInstallsCheckedOutOpenServerlessMCP(t *testing.T) {
	content, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	setup := string(content)
	if strings.Contains(setup, "git+https://github.com/apache/openserverless-mcp.git") ||
		strings.Contains(setup, "github:apache/openserverless-mcp") {
		t.Fatal("setup.sh must not overwrite the local OpenServerless MCP with a direct GitHub install")
	}
	if !strings.Contains(setup, `cd mcp && npm pack`) {
		t.Fatal("setup.sh must package the checked-out mcp submodule")
	}
	if !strings.Contains(setup, `secret-unbind`) {
		t.Fatal("setup.sh must verify a Trustable-only MCP registration")
	}
	if !strings.Contains(setup, `TRUSTABLE_CODE_WORKTREE_HASH`) ||
		!strings.Contains(setup, `TRUSTABLE_CODE_BUILD_REF`) {
		t.Fatal("setup.sh must rebuild Trustable Code when its development working tree changes")
	}
	if !strings.Contains(setup, `trustable_code_source_identity trustable-code`) {
		t.Fatal("setup.sh must fingerprint Trustable Code without requiring guest Git metadata")
	}
	if strings.Contains(setup, `git -C trustable-code rev-parse`) {
		t.Fatal("setup.sh must not follow host-only nested submodule Git metadata from the guest")
	}
}

func TestSetupSelectsAvailableKubernetesClient(t *testing.T) {
	content, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	setup := string(content)
	for _, required := range []string{
		`KUBECTL_CMD=(kubectl)`,
		`KUBECTL_CMD=(k3s kubectl)`,
		`KUBECONFIG="$KUBECONFIG_FILE" "${KUBECTL_CMD[@]}" "$@"`,
		`local k3s API did not become ready`,
		`WSL requires systemd-resolved`,
	} {
		if !strings.Contains(setup, required) {
			t.Fatalf("setup.sh is missing portable Kubernetes setup fragment %q", required)
		}
	}
	if strings.Contains(setup, `KUBECONFIG="$KUBECONFIG_FILE" kubectl`) {
		t.Fatal("setup.sh must not assume a standalone kubectl binary")
	}
}

func TestTrustableCodeIdentityDoesNotRequireGuestGitMetadata(t *testing.T) {
	root := filepath.Join(t.TempDir(), "trustable-code")
	if err := os.MkdirAll(filepath.Join(root, "packages", "opencode"), 0o755); err != nil {
		t.Fatalf("create fake Trustable Code checkout: %s", err)
	}
	for path, content := range map[string]string{
		filepath.Join(root, ".git"):                                 "gitdir: /host-only/.git/modules/trustable-code\n",
		filepath.Join(root, "packages", "opencode", "package.json"): "{\"version\":\"test\"}\n",
		filepath.Join(root, "packages", "opencode", "source.ts"):    "export const value = 1\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %s", path, err)
		}
	}

	identity := func(ref string) []string {
		cmd := exec.Command("bash", "-c", `
set -euo pipefail
source ./setup-trustable-code.sh
trustable_code_source_identity "$1"
`, "bash", root)
		cmd.Dir = "."
		if ref != "" {
			cmd.Env = append(os.Environ(), "TRUSTABLE_CODE_SOURCE_REF="+ref)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("resolve source identity: %s\n%s", err, out)
		}
		fields := strings.Fields(string(out))
		if len(fields) != 2 {
			t.Fatalf("unexpected source identity %q", out)
		}
		return fields
	}

	const commit = "0123456789abcdef0123456789abcdef01234567"
	withHostRef := identity(commit)
	if withHostRef[0] != commit {
		t.Fatalf("host revision not preserved: got %q", withHostRef[0])
	}
	withoutGit := identity("")
	if !strings.HasPrefix(withoutGit[0], "source-") {
		t.Fatalf("inaccessible Git metadata must fall back to source fingerprint, got %q", withoutGit[0])
	}
	if withoutGit[1] != withHostRef[1] {
		t.Fatalf("source hash changed when only revision metadata changed: %q != %q", withoutGit[1], withHostRef[1])
	}

	ignored := filepath.Join(root, "node_modules", "generated.js")
	if err := os.MkdirAll(filepath.Dir(ignored), 0o755); err != nil {
		t.Fatalf("create ignored build directory: %s", err)
	}
	if err := os.WriteFile(ignored, []byte("generated\n"), 0o644); err != nil {
		t.Fatalf("write ignored build artifact: %s", err)
	}
	if got := identity(commit)[1]; got != withHostRef[1] {
		t.Fatalf("dependency output changed source fingerprint: %q != %q", got, withHostRef[1])
	}

	source := filepath.Join(root, "packages", "opencode", "source.ts")
	if err := os.WriteFile(source, []byte("export const value = 2\n"), 0o644); err != nil {
		t.Fatalf("modify source: %s", err)
	}
	if got := identity(commit)[1]; got == withHostRef[1] {
		t.Fatal("source change did not change Trustable Code fingerprint")
	}
}

func TestStartPassesTrustableCodeRevisionFromHostToGuest(t *testing.T) {
	content, err := os.ReadFile("start.sh")
	if err != nil {
		t.Fatalf("read start.sh: %s", err)
	}
	start := string(content)
	for _, required := range []string{
		`git -C "$MOUNT_DIR/trustable-code" rev-parse --verify HEAD`,
		`env TRUSTABLE_CODE_SOURCE_REF="$trustable_code_ref" ./setup.sh`,
	} {
		if !strings.Contains(start, required) {
			t.Fatalf("start.sh is missing portable Trustable Code setup fragment %q", required)
		}
	}
}
