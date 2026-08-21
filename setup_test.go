package main

import (
	"os"
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
	if !strings.Contains(setup, `mongodb-mcp-server@1.9.0`) {
		t.Fatal("setup.sh must install the MongoDB MCP version compatible with the pinned parser")
	}
	// WHY: the browser MCP and its Playwright/Chromium runtime were removed.
	// The e2e harness keeps its own @playwright/test dependency, which lives in
	// the root package.json and must never be reintroduced here.
	if strings.Contains(setup, "trustable-browser-mcp") ||
		strings.Contains(setup, "playwright") ||
		strings.Contains(setup, "chromium") {
		t.Fatal("setup.sh must not install the removed browser MCP or a Chromium runtime")
	}
	if !strings.Contains(setup, `install -m 0755 image/redis-mcp "$MCP_BIN/trustable-redis-mcp"`) {
		t.Fatal("setup.sh must install the Trustable Redis namespace wrapper")
	}
	if strings.Contains(setup, `git -C trustable-acp`) || strings.Contains(setup, `git -C mcp`) {
		t.Fatal("setup.sh must consume mounted source without following host-only Git metadata")
	}
	if !strings.Contains(setup, `(cd trustable-acp && ./setup.sh)`) {
		t.Fatal("setup.sh must build TruACP from the checked-out submodule")
	}
}

func TestRedisMCPNamespaceWrapperIsInstalledInVMAndImage(t *testing.T) {
	setupContent, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	dockerContent, err := os.ReadFile(filepath.Join("image", "Dockerfile"))
	if err != nil {
		t.Fatalf("read image/Dockerfile: %s", err)
	}
	if !strings.Contains(string(setupContent), `image/redis-mcp "$MCP_BIN/trustable-redis-mcp"`) {
		t.Fatal("VM setup must install the Redis MCP namespace wrapper")
	}
	if !strings.Contains(string(dockerContent), `COPY redis-mcp /usr/local/bin/trustable-redis-mcp`) {
		t.Fatal("production image must install the Redis MCP namespace wrapper")
	}
}

func TestSetupConfiguresUserOwnedNPMGlobalPrefix(t *testing.T) {
	content, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	setup := string(content)
	for _, required := range []string{
		`NPM_DEFAULT_PREFIX="$HOME/.npm-global"`,
		`NPM_CONFIGURED_PREFIX="${NPM_CONFIG_PREFIX:-}"`,
		`NPM_CONFIGURED_PREFIX="$(npm config get prefix`,
		`mkdir -p "$prefix/bin" "$prefix/lib/node_modules"`,
		`stat -c '%u' "$prefix"`,
		`npm_prefix_is_compatible "$NPM_CONFIGURED_PREFIX"`,
		`export NPM_CONFIG_PREFIX="$NPM_GLOBAL_PREFIX"`,
		`npm config set prefix "$NPM_GLOBAL_PREFIX" --location=user`,
		`export PATH="$NPM_GLOBAL_BIN:$PATH"`,
		`if ! grep -qF "$IMAGE_PATH" "$shell_rc"`,
		`IMAGE_PATH="\$HOME/.local/bin:${NPM_GLOBAL_BIN}:`,
	} {
		if !strings.Contains(setup, required) {
			t.Fatalf("setup.sh is missing npm-prefix fragment %q", required)
		}
	}
	if strings.Contains(setup, `--prefix "$HOME/.local"`) {
		t.Fatal("setup.sh global npm installs must consume the selected npm prefix")
	}
	if strings.Contains(setup, "sudo npm") {
		t.Fatal("setup.sh must never require sudo for a global npm install")
	}
	configureAt := strings.Index(setup, `npm config set prefix "$NPM_GLOBAL_PREFIX"`)
	firstGlobalInstallAt := strings.Index(setup, "npm install -g")
	if configureAt < 0 || firstGlobalInstallAt < 0 || configureAt > firstGlobalInstallAt {
		t.Fatal("setup.sh must configure and export the npm prefix before global npm installs")
	}

	acpContent, err := os.ReadFile(filepath.Join("trustable-acp", "setup.sh"))
	if err != nil {
		t.Fatalf("read trustable-acp/setup.sh: %s", err)
	}
	acpSetup := string(acpContent)
	if !strings.Contains(acpSetup, `GLOBAL_PREFIX=$(npm config get prefix)`) {
		t.Fatal("trustable-acp/setup.sh must consume the caller-selected npm prefix")
	}
	if strings.Contains(acpSetup, ".npm-global") {
		t.Fatal("trustable-acp/setup.sh must not define a competing persistent npm-prefix default")
	}
}

// pi.version owns the integrity-pinned upstream Pi CLI, so repository setup
// must install and verify it before the nested installer builds only pi-acp.
func TestSetupChecksUpstreamPiBeforeNestedACPBuild(t *testing.T) {
	content, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	setup := string(content)

	nestedBuildAt := strings.Index(setup, `(cd trustable-acp && ./setup.sh)`)
	if nestedBuildAt < 0 {
		t.Fatal("setup.sh must build the nested trustable-acp installer")
	}
	piCheckAt := strings.Index(setup, `command -v pi &>/dev/null`)
	if piCheckAt < 0 {
		t.Fatal("setup.sh must verify the pi CLI is installed")
	}
	if piCheckAt > nestedBuildAt {
		t.Fatal("setup.sh must check upstream pi before trustable-acp/setup.sh builds pi-acp")
	}

	versions, err := os.ReadFile(filepath.Join("trustable-acp", "pi.version"))
	if err != nil {
		t.Fatalf("read trustable-acp/pi.version: %s", err)
	}
	hasUpstreamPi := false
	for _, line := range strings.Split(string(versions), "\n") {
		spec := strings.TrimSpace(line)
		if i := strings.Index(spec, "#"); i >= 0 {
			spec = strings.TrimSpace(spec[:i])
		}
		if strings.HasPrefix(spec, "@earendil-works/pi-coding-agent@") {
			hasUpstreamPi = true
		}
	}
	if !hasUpstreamPi {
		t.Fatal("pi.version must pin the upstream @earendil-works/pi-coding-agent package")
	}
}

// Ubuntu's stock ~/.bashrc returns early for non-interactive shells, so a PATH
// line appended only there never runs under `bash -lc`. ~/.profile is what
// actually carries the toolchain into a login shell.
func TestSetupWritesImagePathToProfileNotOnlyBashrc(t *testing.T) {
	content, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	setup := string(content)
	for _, required := range []string{
		`for shell_rc in "$HOME/.profile" "$HOME/.bashrc"`,
		`grep -qF "$IMAGE_PATH" "$shell_rc"`,
		`echo "export PATH=\"$IMAGE_PATH\"" >> "$shell_rc"`,
	} {
		if !strings.Contains(setup, required) {
			t.Fatalf("setup.sh is missing login-shell PATH fragment %q", required)
		}
	}
	if strings.Contains(setup, `echo "export PATH=\"$IMAGE_PATH\"" >> "$HOME/.bashrc"`) {
		t.Fatal("setup.sh must not write the image PATH to ~/.bashrc alone")
	}
}

func TestRuntimeInstallsPortRecoveryDependencyInVMAndImage(t *testing.T) {
	setupContent, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	setup := string(setupContent)
	for _, required := range []string{
		`command -v lsof`,
		`APT_MISSING+=(lsof)`,
	} {
		if !strings.Contains(setup, required) {
			t.Fatalf("setup.sh must install the launch port-recovery dependency: missing %q", required)
		}
	}

	dockerContent, err := os.ReadFile(filepath.Join("image", "Dockerfile"))
	if err != nil {
		t.Fatalf("read image/Dockerfile: %s", err)
	}
	if !strings.Contains(string(dockerContent), "vim lsof libatomic1") {
		t.Fatal("production image must contain lsof for orphaned TruACP/Vite listener recovery")
	}
}

// TestPythonMCPServersPinTheMCPSDK guards the constraint that keeps postgres-mcp
// and redis-mcp-server on the 1.x MCP SDK. Both declare an open upper bound but
// import `mcp.server.fastmcp`, which mcp 2.x renamed; unconstrained they resolve
// mcp 2.x and die at import, surfacing only as a reduced server count in the
// agent runtime. The import probe is asserted for the same reason: without it a
// broken resolution passes setup silently.
func TestPythonMCPServersPinTheMCPSDK(t *testing.T) {
	setupContent, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	dockerContent, err := os.ReadFile(filepath.Join("image", "Dockerfile"))
	if err != nil {
		t.Fatalf("read image/Dockerfile: %s", err)
	}
	for name, content := range map[string]string{
		"setup.sh":         string(setupContent),
		"image/Dockerfile": string(dockerContent),
	} {
		for _, required := range []string{
			"postgres-mcp==0.3.0|--with|mcp<2",
			"redis-mcp-server==0.5.0|--with|mcp<2",
			"postgres-mcp:postgres_mcp",
			"redis-mcp-server:src.main",
			"mcp-server-milvus:mcp_server_milvus",
		} {
			if !strings.Contains(content, required) {
				t.Fatalf("%s is missing pinned Python MCP fragment %q", name, required)
			}
		}
	}
	// The interpreter pin belongs to the VM setup path: postgres-mcp requires
	// pglast==7.2.0, which ships no cp313 wheel. The image already pins
	// /usr/bin/python3 explicitly.
	if !strings.Contains(string(setupContent), `uv tool install --python "$UV_PYTHON_PIN"`) {
		t.Fatal("setup.sh does not pin the uv interpreter for the Python MCP servers")
	}
	if !strings.Contains(string(dockerContent), "uv tool install --python /usr/bin/python3") {
		t.Fatal("image/Dockerfile does not pin the uv interpreter for the Python MCP servers")
	}
}

func TestGitHubCLIIsPinnedForVMAndImage(t *testing.T) {
	setupContent, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	dockerContent, err := os.ReadFile(filepath.Join("image", "Dockerfile"))
	if err != nil {
		t.Fatalf("read image/Dockerfile: %s", err)
	}
	// image/Dockerfile owns the literal pin and checksums; setup.sh must read
	// them from it rather than duplicating the values, so the VM and the pod
	// install the same verified GitHub CLI. Asserting the literal in both files
	// would force exactly the drift this contract exists to prevent.
	for _, required := range []string{
		"ARG GH_VERSION=2.96.0",
		"ARG GH_SHA_AMD64=",
		"ARG GH_SHA_ARM64=",
	} {
		if !strings.Contains(string(dockerContent), required) {
			t.Fatalf("image/Dockerfile is missing pinned GitHub CLI fragment %q", required)
		}
	}

	setup := string(setupContent)
	for _, required := range []string{
		"GH_VERSION GH_SHA_AMD64 GH_SHA_ARM64",
		`read_arg "$v"`,
	} {
		if !strings.Contains(setup, required) {
			t.Fatalf("setup.sh must read the GitHub CLI pin from image/Dockerfile, missing %q", required)
		}
	}
	if strings.Contains(setup, "GH_VERSION=2.96.0") {
		t.Fatal("setup.sh must not hardcode GH_VERSION; it reads the pin from image/Dockerfile")
	}

	for name, content := range map[string]string{
		"setup.sh":         setup,
		"image/Dockerfile": string(dockerContent),
	} {
		for _, required := range []string{
			"gh_${GH_VERSION}_linux_",
			"sha256sum -c -",
		} {
			if !strings.Contains(content, required) {
				t.Fatalf("%s is missing pinned GitHub CLI fragment %q", name, required)
			}
		}
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
		// Not the whole command line: start.sh threads `-c core.autocrlf=false
		// -c core.eol=lf` between `git -C "$MOUNT_DIR"` and the subcommand so a
		// fresh Windows checkout lands as LF. What must hold is that both
		// source submodules are initialised recursively, not the exact flags.
		`submodule update --init --recursive mcp trustable-acp`,
		`[[ -f "$MOUNT_DIR/mcp/package.json" ]]`,
		`[[ -f "$MOUNT_DIR/trustable-acp/package.json" ]]`,
		`ensure_source_submodules`,
	} {
		if !strings.Contains(start, required) {
			t.Fatalf("start.sh is missing portable TruACP source setup fragment %q", required)
		}
	}
	// The submodule update must still be scoped to the mounted worktree.
	if !strings.Contains(start, `git -C "$MOUNT_DIR"`) {
		t.Fatal(`start.sh must run the submodule update with git -C "$MOUNT_DIR"`)
	}
}

// WHY: ssh.sh is the user-facing wrapper — it takes no command and always ends
// in `exec bash`. start.sh once probed reachability with `./ssh.sh true`, whose
// argument was ignored, so the probe opened an interactive shell and blocked the
// rest of the run: `./start.sh -v` never reached the step that opens VS Code and
// the user was dropped into a VM shell with no error explaining it.
func TestStartDoesNotShellOutToSSHScript(t *testing.T) {
	content, err := os.ReadFile("start.sh")
	if err != nil {
		t.Fatalf("read start.sh: %s", err)
	}
	// Comments explain why the call is forbidden, so only executable lines count.
	var code []string
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			code = append(code, line)
		}
	}
	start := strings.Join(code, "\n")

	// The echoed help line naming ./ssh.sh for the user is fine; invoking it is
	// not. Only the call forms are rejected.
	for _, forbidden := range []string{`./ssh.sh true`, `if ./ssh.sh`, `./ssh.sh >`} {
		if strings.Contains(start, forbidden) {
			t.Fatalf("start.sh must not invoke ssh.sh (%q): it always execs an interactive shell and would block the start", forbidden)
		}
	}
	// Reachability is proven over ssh with the support-dir identity, because that
	// is the path VS Code Remote-SSH takes. limactl would succeed even when ssh
	// could not connect.
	if !strings.Contains(start, "probe_ssh") {
		t.Fatal("start.sh must probe ssh reachability directly, via probe_ssh")
	}
	// BatchMode keeps a missing or unauthorized key a failure rather than a
	// password prompt that would hang an unattended start.
	if !strings.Contains(start, "BatchMode=yes") {
		t.Fatal("the ssh probe must use BatchMode=yes, or a key problem hangs on a password prompt")
	}
}

func TestStartAllocatesBuildCapableLimaDisk(t *testing.T) {
	content, err := os.ReadFile("start.sh")
	if err != nil {
		t.Fatalf("read start.sh: %s", err)
	}
	start := string(content)
	if !strings.Contains(start, `disk: "60GiB"`) {
		t.Fatal("start.sh must leave enough disk headroom for k3s and repeated Trustable image imports")
	}
	if strings.Contains(start, `disk: "40GiB"`) {
		t.Fatal("start.sh must not recreate trudev with the DiskPressure-prone 40 GiB allocation")
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
		`TRUSTABLE_BUILD_BRANCH`,
		`> _build.txt`,
	} {
		if !strings.Contains(run, required) {
			t.Fatalf("run.sh is missing clean-worktree build metadata fragment %q", required)
		}
	}
	// The metadata used to be written only when _build.txt was missing or
	// empty (`[[ -s _build.txt ]] || write_dev_build_metadata`). A reused
	// worktree carries an ignored _build.txt from an earlier image build, so
	// that left Air running with stale release metadata. Size alone is still
	// not a valid reason to skip; the only sanctioned skip is an exact HEAD
	// tag match, below.
	if strings.Contains(run, `[[ -s _build.txt ]] || write_dev_build_metadata`) {
		t.Fatal("run.sh must not skip _build.txt regeneration merely because one already exists")
	}
	// Regeneration now runs through ensure_build_metadata, which keeps release
	// metadata when HEAD is exactly the tag it was built from.
	body := run[strings.Index(run, "write_dev_build_metadata() {"):]
	if !strings.Contains(body, "\nensure_build_metadata\n") {
		t.Fatal("run.sh must call ensure_build_metadata before starting Air")
	}
	for _, required := range []string{
		`git describe --exact-match --tags HEAD`,
		// An untagged HEAD yields an empty tag, and `grep ""` matches any
		// non-empty file. Without this guard every untagged dev checkout would
		// keep whatever stale release metadata the worktree carried.
		`[[ -n "$tag" ]] || return 1`,
		// Missing/empty _build.txt must still regenerate.
		`[[ -s _build.txt ]] || return 1`,
		// -F: a tag like v0.4.0 is a literal; as a regex the dots would match
		// unrelated builds.
		`grep -qF "$tag" _build.txt`,
	} {
		if !strings.Contains(run, required) {
			t.Fatalf("run.sh is missing HEAD-tag build metadata guard fragment %q", required)
		}
	}
}

func TestRunRestoresSelectedNPMGlobalPrefix(t *testing.T) {
	content, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatalf("read run.sh: %s", err)
	}
	run := string(content)
	for _, required := range []string{
		`NPM_GLOBAL_PREFIX="${NPM_CONFIG_PREFIX:-$(npm config get prefix`,
		`export NPM_CONFIG_PREFIX="$NPM_GLOBAL_PREFIX"`,
		`export PATH="$HOME/.local/bin:$NPM_GLOBAL_PREFIX/bin:$PATH"`,
		`for runtime_command in pi pi-acp`,
		`is missing from the configured npm prefix`,
	} {
		if !strings.Contains(run, required) {
			t.Fatalf("run.sh is missing npm runtime PATH fragment %q", required)
		}
	}
}

func TestStartInstallsPinnedKubefwdForSupportedArchitectures(t *testing.T) {
	content, err := os.ReadFile("start.sh")
	if err != nil {
		t.Fatalf("read start.sh: %s", err)
	}
	start := string(content)
	for _, required := range []string{
		`KUBEFWD_VERSION="1.25.16"`,
		`kubefwd_Linux_${archive_arch}.tar.gz`,
		`KUBEFWD_SHA_AMD64=`,
		`KUBEFWD_SHA_ARM64=`,
		`sha256sum -c -`,
		`/usr/local/bin/kubefwd`,
		`ensure_kubefwd`,
	} {
		if !strings.Contains(start, required) {
			t.Fatalf("start.sh is missing pinned kubefwd fragment %q", required)
		}
	}
}

func TestRunOwnsOneNamespaceWideKubefwd(t *testing.T) {
	content, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatalf("read run.sh: %s", err)
	}
	run := string(content)
	if got := strings.Count(run, "kubefwd svc"); got != 1 {
		t.Fatalf("run.sh must start exactly one kubefwd process, found %d", got)
	}
	for _, required := range []string{
		`-f 'metadata.name!=trustable-svc'`,
		`--kubeconfig "$KUBECONFIG_FILE"`,
		`-n nuvolaris`,
		`getent ahostsv4 "$FORWARD_PROBE_SERVICE"`,
		`grep -q '^127\.'`,
		`KUBEFWD_PID_FILE=`,
		`printf "%s\n" "$$"`,
		`exec "$@"`,
		`sudo -n kill -INT "$KUBEFWD_PID"`,
		`wait "$KUBEFWD_SUDO_PID"`,
		`tail -n 80 "$KUBEFWD_LOG"`,
	} {
		if !strings.Contains(run, required) {
			t.Fatalf("run.sh is missing kubefwd lifecycle fragment %q", required)
		}
	}
}

func TestSetupInstallsGlobalPinnedMilvusCli(t *testing.T) {
	content, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	setup := string(content)
	for _, required := range []string{
		`MILVUS_CLI_VERSION="1.2.1"`,
		`UV_TOOL_BIN_DIR=/usr/local/bin`,
		`"milvus-cli==${MILVUS_CLI_VERSION}"`,
		`/usr/local/bin/milvus_cli`,
	} {
		if !strings.Contains(setup, required) {
			t.Fatalf("setup.sh is missing global Milvus CLI fragment %q", required)
		}
	}
	if strings.Contains(setup, `UV_TOOL_BIN_DIR="$LOCAL_BIN" uv tool install milvus-cli`) {
		t.Fatal("setup.sh must not install the upstream milvus_cli into the managed wrapper directory")
	}
}

func TestMilvusMCPUsesPinnedTrustableFork(t *testing.T) {
	setupData, err := os.ReadFile("setup.sh")
	if err != nil {
		t.Fatalf("read setup.sh: %s", err)
	}
	dockerData, err := os.ReadFile("image/Dockerfile")
	if err != nil {
		t.Fatalf("read image/Dockerfile: %s", err)
	}
	setup := string(setupData)
	dockerfile := string(dockerData)
	const repository = "https://github.com/trustable-ai/mcp-server-milvus.git"
	const revision = "a7e624f3057a0d739528bca3ed92504943224ceb"
	if !strings.Contains(dockerfile, repository) || !strings.Contains(dockerfile, revision) {
		t.Fatal("image/Dockerfile must own the pinned Trustable Milvus MCP fork")
	}
	for name, source := range map[string]string{"setup.sh": setup, "image/Dockerfile": dockerfile} {
		if strings.Contains(source, "github.com/zilliztech/mcp-server-milvus") {
			t.Fatalf("%s still installs the upstream Milvus MCP directly", name)
		}
	}
	// WHY: setup must consume the Dockerfile-owned source identity rather than
	// grow a second hardcoded install that can diverge from the pod.
	for _, required := range []string{"MILVUS_MCP_REPO", "MILVUS_MCP_REF"} {
		if !strings.Contains(setup, `read_arg "$v"`) || !strings.Contains(setup, required) {
			t.Fatalf("setup.sh does not consume Dockerfile variable %s", required)
		}
	}
	for _, required := range []string{
		`MILVUS_MCP_RECEIPT="$(uv tool dir)/mcp-server-milvus/uv-receipt.toml"`,
		`UV_INSTALL_ARGS+=(--force)`,
		`installed Milvus MCP does not match`,
	} {
		if !strings.Contains(setup, required) {
			t.Fatalf("setup.sh is missing Milvus MCP source reconciliation %q", required)
		}
	}
}

func TestLocalE2ERequiresRunKubefwd(t *testing.T) {
	data, err := os.ReadFile("tests/e2e_issue98.sh")
	if err != nil {
		t.Fatalf("read local E2E runner: %s", err)
	}
	source := string(data)
	for _, required := range []string{
		`pgrep -x kubefwd`,
		`"${#KUBEFWD_PIDS[@]}" -ne 1`,
		`"-n nuvolaris"`,
		`metadata.name!=trustable-svc`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("local E2E runner is missing kubefwd preflight %q", required)
		}
	}
	if strings.Contains(source, "kubefwd svc") {
		t.Fatal("E2E runner must not start a second kubefwd process")
	}
}

func TestScriptSpecificationsLiveUnderSpec(t *testing.T) {
	for _, name := range []string{"setup.md", "run.md", "start.md"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("obsolete root specification %s still exists", name)
		}
		path := filepath.Join("spec", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read relocated specification %s: %s", path, err)
		}
		if !strings.Contains(string(data), "repository-root") {
			t.Fatalf("%s must identify the top-level script it specifies", path)
		}
	}
}
