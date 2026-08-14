package main

import (
	"os"
	"strings"
	"testing"
)

func TestLauncherUsesTruACPWithoutOpenCodeSessionBootstrap(t *testing.T) {
	data, err := os.ReadFile("launch.go")
	if err != nil {
		t.Fatalf("read launch.go: %s", err)
	}
	source := string(data)
	// WHY: launch must derive MCPs and companion wrappers from the same
	// post-login config snapshot; the old helper re-read config independently.
	for _, required := range []string{
		`exec.Command("truacp", "--port", strconv.Itoa(leftPort), "--dir", workbenchPath)`,
		`serviceConfig, err := loadOpsConfig()`,
		`generateProjectAssetsInDir(workbenchPath, buildMCPFromOpsConfig(serviceConfig))`,
		`setupServiceToolingFromConfig(serviceConfig)`,
		`writeTrustablePiRuntimeManifest(app, workbenchPath, browserURL, watcherLogPath)`,
		`"TRUSTABLE_RUNTIME_CONFIG="+runtimeManifestPath`,
		`"TRUSTABLE_PI_EXTENSION_PATH="+extensionPath`,
		`if piDefaultModel(cfg) == ""`,
		`"PI_SKIP_VERSION_CHECK=1"`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("TruACP launcher contract missing %q", required)
		}
	}
	for _, removed := range []string{
		`exec.Command("opencode"`,
		`handleOpenCodeSessions`,
		`resolveOpencodeSession`,
		`writeTrustableRuntimeManifest`,
		`writePiGlobalConfig(`,
	} {
		if strings.Contains(source, removed) {
			t.Fatalf("legacy OpenCode runtime path is still active: %q", removed)
		}
	}
}

func TestTruACPUsesHistoricalPublicHostnameOnly(t *testing.T) {
	data, err := os.ReadFile("middleware.go")
	if err != nil {
		t.Fatalf("read middleware.go: %s", err)
	}
	source := string(data)
	if !strings.Contains(source, `case "opencode":`) || !strings.Contains(source, `truacpProxy.ServeHTTP(w, r)`) {
		t.Fatal("historical opencode hostname must proxy the TruACP UI")
	}
	if strings.Contains(source, "redirectOpenCodeSession") || strings.Contains(source, "X-Opencode-Directory") {
		t.Fatal("TruACP proxy must not rewrite OpenCode directories or sessions")
	}
}

func TestRuntimeImageBuildsPinnedTruACPInsteadOfOpenCode(t *testing.T) {
	dockerfile, err := os.ReadFile("image/Dockerfile")
	if err != nil {
		t.Fatalf("read image/Dockerfile: %s", err)
	}
	source := string(dockerfile)
	for _, required := range []string{
		"COPY --chown=trustable:trustable truacp-runtime/setup.sh /tmp/truacp/setup.sh",
		"COPY --chown=trustable:trustable truacp-runtime/pi.version /tmp/truacp/pi.version",
		"COPY --chown=trustable:trustable truacp-runtime/pi.integrity /tmp/truacp/pi.integrity",
		"COPY --chown=trustable:trustable truacp-runtime/dist-bin/truacp.cjs /tmp/truacp/dist-bin/truacp.cjs",
		"COPY --chown=trustable:trustable truacp-runtime/extensions/trustable-runtime.ts /tmp/truacp/extensions/trustable-runtime.ts",
		"sh setup.sh",
		`test -x "$HOME/.local/bin/truacp"`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("TruACP image contract missing %q", required)
		}
	}
	for _, removed := range []string{"opencode-builder", "OPENCODE_VERSION", "@opencode-ai/plugin", "COPY trustable-code", "COPY --chown=trustable:trustable trustable-acp"} {
		if strings.Contains(source, removed) {
			t.Fatalf("runtime image still contains OpenCode build path %q", removed)
		}
	}

	imageScript, err := os.ReadFile("image/image.sh")
	if err != nil {
		t.Fatalf("read image/image.sh: %s", err)
	}
	staging := string(imageScript)
	for _, required := range []string{
		`TRUACP_ARTIFACT_DIR="truacp-runtime"`,
		`npm run build`,
		`cp ../trustable-acp/setup.sh "$TRUACP_ARTIFACT_DIR/setup.sh"`,
		`cp ../trustable-acp/pi.version "$TRUACP_ARTIFACT_DIR/pi.version"`,
		`cp ../trustable-acp/pi.integrity "$TRUACP_ARTIFACT_DIR/pi.integrity"`,
		`cp ../trustable-acp/dist-bin/truacp.cjs "$TRUACP_ARTIFACT_DIR/dist-bin/truacp.cjs"`,
		`cp ../trustable-acp/extensions/trustable-runtime.ts "$TRUACP_ARTIFACT_DIR/extensions/trustable-runtime.ts"`,
		// The staged artifact must still be identified by the submodule commit
		// it came from and by a content hash of what was actually staged.
		// These used to feed `printf 'trustable-acp=%s:%s\n'` into a BASE_HASH
		// cache key for a hash-tagged base image; that two-stage split was
		// removed in 0bebc41 because buildkit cannot resolve `FROM base`
		// against an image it has just built, so the key it fed is gone with
		// it. What must not regress is that both values are still derived.
		`TRUACP_REF="$(git -C ../trustable-acp rev-parse HEAD)"`,
		`TRUACP_HASH="$(find "$TRUACP_ARTIFACT_DIR" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-12)"`,
	} {
		if !strings.Contains(staging, required) {
			t.Fatalf("TruACP artifact staging contract missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`tar -C ../trustable-acp`,
		`TRUACP_CONTEXT_DIR="trustable-acp"`,
		`PI_LOCAL_RELEASE_USE_CHECKED_IN_MODELS`,
		`pi-packages`,
		// WHY: the issue #57 execution-policy extension must return through
		// its complete contract, not as the stale issue #58 build placeholder.
		`trustable-guardrails.ts`,
	} {
		if strings.Contains(staging, forbidden) {
			t.Fatalf("image build contains forbidden TruACP staging entry %q", forbidden)
		}
	}
	if strings.Contains(staging, "TRUSTABLE_CODE_CONTEXT_DIR") {
		t.Fatal("image context still stages Trustable Code")
	}
}
