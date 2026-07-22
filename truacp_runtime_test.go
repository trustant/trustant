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
	for _, required := range []string{
		`exec.Command("truacp", "--port", strconv.Itoa(leftPort), "--dir", workbenchPath)`,
		`generateProjectAssetsForApp(app)`,
		`writePiGlobalConfig(cfg)`,
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
		"COPY --chown=trustable:trustable truacp-runtime/dist-bin/truacp.cjs /tmp/truacp/dist-bin/truacp.cjs",
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
		`cp ../trustable-acp/dist-bin/truacp.cjs "$TRUACP_ARTIFACT_DIR/dist-bin/truacp.cjs"`,
		`printf 'trustable-acp=%s:%s\n' "$TRUACP_REF" "$TRUACP_HASH"`,
	} {
		if !strings.Contains(staging, required) {
			t.Fatalf("TruACP artifact staging contract missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`tar -C ../trustable-acp`,
		`TRUACP_CONTEXT_DIR="trustable-acp"`,
	} {
		if strings.Contains(staging, forbidden) {
			t.Fatalf("image build must not stage TruACP source: found %q", forbidden)
		}
	}
	if strings.Contains(staging, "TRUSTABLE_CODE_CONTEXT_DIR") {
		t.Fatal("image context still stages Trustable Code")
	}
}
