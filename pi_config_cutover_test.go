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

func TestProviderSelectionResumesPiGateAfterOllamaCloudSignin(t *testing.T) {
	html := readWebAssetForPiCutoverTest(t, "web/index.html")
	for _, required := range []string{
		`async function waitForOllamaSignin()`,
		`line.startsWith('AUTH_REQUIRED:')`,
		`saveResult.testmodel.auth_required`,
		`return runConfiguration();`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("provider selection is missing Ollama Cloud recovery contract %q", required)
		}
	}
}
