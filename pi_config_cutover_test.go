package main

import (
	"os"
	"strings"
	"testing"
)

func readWebAssetForPiCutoverTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %s", path, err)
	}
	return string(data)
}

func TestConfigureUsesSinglePiModelSelection(t *testing.T) {
	html := readWebAssetForPiCutoverTest(t, "web/configure.html")
	for _, required := range []string{
		`id="piDefault"`,
		`pi: { default: '' }`,
		`config.pi`,
		`applist.html?configured=1`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("configure page is missing Pi contract %q", required)
		}
	}
	for _, legacy := range []string{`opencodeSmall`, `small_model`, `config.opencode`} {
		if strings.Contains(html, legacy) {
			t.Fatalf("configure page still exposes legacy contract %q", legacy)
		}
	}
}

func TestAppListRequiresPiConfiguration(t *testing.T) {
	html := readWebAssetForPiCutoverTest(t, "web/applist.html")
	for _, required := range []string{
		`bootConfig.pi`,
		`configure.html?setup=1`,
		`pi: { default: refreshedDefault }`,
		`if (data.setup_required)`,
		`const configurationJustSaved = bootParams.get('configured') === '1'`,
		`!configurationJustSaved && !ownHostOllama`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("app list is missing Pi setup guard %q", required)
		}
	}
	for _, forbidden := range []string{`bootConfig.opencode`, `defaultsChanged`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("app list contains redirect-loop or legacy contract %q", forbidden)
		}
	}
}

func TestProviderSelectionPersistsPiContract(t *testing.T) {
	html := readWebAssetForPiCutoverTest(t, "web/index.html")
	for _, required := range []string{
		`pi: { default: refreshedDefault }`,
		`const configuredPiDefault = String((currentConfig.pi && currentConfig.pi.default) || '').trim()`,
		`if (provider && !forceChoose && !configuredPiDefault)`,
		`window.location.href = 'configure.html?setup=1'`,
		`function catalogSelectionError(providerLabel, section)`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("provider selection is missing Pi migration guard %q", required)
		}
	}
	for _, legacy := range []string{
		`opencode: { default:`,
		`currentConfig.opencode`,
		`if (data.opencode`,
		`cfg.opencode =`,
		`defaultsChanged`,
	} {
		if strings.Contains(html, legacy) {
			t.Fatalf("provider selection contains redirect-loop or legacy contract %q", legacy)
		}
	}
}
