package main

import (
	"os"
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
	if !strings.Contains(setup, `mongodb-mcp-server@1.9.0`) {
		t.Fatal("setup.sh must install the MongoDB MCP version compatible with the pinned parser")
	}
	if !strings.Contains(setup, `cd browser-mcp && npm pack`) ||
		!strings.Contains(setup, `command -v trustable-browser-mcp`) {
		t.Fatal("setup.sh must package and verify the checked-out browser MCP")
	}
	if !strings.Contains(setup, `playwright@1.56.1 install --with-deps chromium`) {
		t.Fatal("setup.sh must install pinned Chromium and its Linux runtime dependencies")
	}
	if strings.Contains(setup, `git -C trustable-acp`) || strings.Contains(setup, `git -C mcp`) {
		t.Fatal("setup.sh must consume mounted source without following host-only Git metadata")
	}
	if !strings.Contains(setup, `(cd trustable-acp && ./setup.sh)`) {
		t.Fatal("setup.sh must build TruACP from the checked-out submodule")
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
	} {
		if !strings.Contains(setup, required) {
			t.Fatalf("setup.sh is missing portable Kubernetes setup fragment %q", required)
		}
	}
	if strings.Contains(setup, `KUBECONFIG="$KUBECONFIG_FILE" kubectl`) {
		t.Fatal("setup.sh must not assume a standalone kubectl binary")
	}
	for _, forbidden := range []string{
		`systemd-resolved`,
		`/etc/systemd/resolved.conf.d`,
		`systemctl restart`,
		`get svc kube-dns`,
	} {
		if strings.Contains(setup, forbidden) {
			t.Fatalf("setup.sh must not change VM DNS or systemd state: found %q", forbidden)
		}
	}
}

func TestStartInitializesRuntimeSourcesOnHost(t *testing.T) {
	content, err := os.ReadFile("start.sh")
	if err != nil {
		t.Fatalf("read start.sh: %s", err)
	}
	start := string(content)
	for _, required := range []string{
		`git -C "$MOUNT_DIR" submodule update --init --recursive mcp trustable-acp`,
		`[[ -f "$MOUNT_DIR/mcp/package.json" ]]`,
		`[[ -f "$MOUNT_DIR/trustable-acp/package.json" ]]`,
		`ensure_source_submodules`,
	} {
		if !strings.Contains(start, required) {
			t.Fatalf("start.sh is missing portable TruACP source setup fragment %q", required)
		}
	}
}

func TestStartProxyPreservesTruACPWebSocketUpgrades(t *testing.T) {
	content, err := os.ReadFile("start.sh")
	if err != nil {
		t.Fatalf("read start.sh: %s", err)
	}
	start := string(content)
	for _, required := range []string{
		`proxy_set_header Upgrade \$http_upgrade;`,
		`proxy_set_header Connection "upgrade";`,
		`proxy_buffering off;`,
		`proxy_request_buffering off;`,
		`proxy_read_timeout 600s;`,
		`proxy_send_timeout 600s;`,
	} {
		if !strings.Contains(start, required) {
			t.Fatalf("start.sh port-8080 proxy is missing TruACP WebSocket fragment %q", required)
		}
	}
}

func TestRunGeneratesBuildMetadataForCleanWorktree(t *testing.T) {
	content, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatalf("read run.sh: %s", err)
	}
	run := string(content)
	for _, required := range []string{
		`write_dev_build_metadata`,
		`[[ -s _build.txt ]] || write_dev_build_metadata`,
		`TRUSTABLE_BUILD_BRANCH`,
		`> _build.txt`,
	} {
		if !strings.Contains(run, required) {
			t.Fatalf("run.sh is missing clean-worktree build metadata fragment %q", required)
		}
	}
}
