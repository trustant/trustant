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
	if !strings.Contains(setup, `TRUSTABLE_CODE_WORKTREE_HASH`) ||
		!strings.Contains(setup, `TRUSTABLE_CODE_BUILD_REF`) {
		t.Fatal("setup.sh must rebuild Trustable Code when its development working tree changes")
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
