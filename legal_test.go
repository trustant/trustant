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
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestLegalEndpointsServeEmbeddedTexts(t *testing.T) {
	for name, want := range map[string]string{
		"deps":    "TRUSTANT - THIRD-PARTY COMPONENTS",
		"notice":  "THIRD-PARTY NOTICES",
		"license": "GNU AFFERO GENERAL PUBLIC LICENSE",
	} {
		rec := httptest.NewRecorder()
		handleLegal(rec, httptest.NewRequest(http.MethodGet, "/api/legal/"+name, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s: content type %q", name, ct)
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: body lacks %q", name, want)
		}
	}
}

func TestLegalEndpointRejectsUnknownAndWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	handleLegal(rec, httptest.NewRequest(http.MethodGet, "/api/legal/../main.go", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown text: status %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	handleLegal(rec, httptest.NewRequest(http.MethodPost, "/api/legal/deps", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rec.Code)
	}
}

// Every license named in DEPS must have its full text in LICENSE. WHY: DEPS and
// LICENSE are generated together by deps.sh, but a hand edit or a stale rerun
// could ship a component whose license terms are not distributed with it.
func TestLegalEveryDepsLicenseHasItsText(t *testing.T) {
	sep := regexp.MustCompile(`\s{2,}`)
	ids := regexp.MustCompile(`[A-Za-z0-9.+\-]+`)
	inTable, rows := false, 0
	for _, line := range strings.Split(legalDeps, "\n") {
		cols := sep.Split(strings.TrimSpace(line), -1)
		switch {
		case len(cols) == 4 && cols[0] == "NAME" && cols[2] == "LICENSE":
			inTable = true
			continue
		case strings.TrimSpace(line) == "":
			inTable = false
			continue
		case !inTable || strings.HasPrefix(line, "----"):
			continue
		}
		if len(cols) != 4 {
			t.Fatalf("DEPS row does not have 4 columns: %q", line)
		}
		rows++
		for _, id := range ids.FindAllString(cols[2], -1) {
			if id == "AND" || id == "OR" || id == "WITH" {
				continue
			}
			if !strings.Contains(legalLicense, "\nLicense: "+id+"\n") {
				t.Errorf("DEPS names %s (%s) but LICENSE has no text for it", id, cols[0])
			}
		}
	}
	if rows < 100 {
		t.Fatalf("parsed only %d DEPS rows; table format changed?", rows)
	}
}

func TestLegalNoticeCarriesTrustantNotice(t *testing.T) {
	if !strings.HasPrefix(legalNotice, "Trustant\nCopyright ") {
		t.Errorf("NOTICE must start with Trustant's own notice")
	}
	if !strings.HasPrefix(legalLicense, "                    GNU AFFERO GENERAL PUBLIC LICENSE") {
		t.Errorf("LICENSE must start with the AGPL text")
	}
}

// The image must carry the same texts as the binary. build.sh stages them into
// image/ on both build paths (--build and --buildx) and hotfix.sh does the same
// for its layer; a missing copy would fail the build, a missing COPY would ship
// an image without them.
func TestLegalTextsShipInTheImage(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %s", name, err)
		}
		return string(data)
	}
	const stage = "cp -v LICENSE NOTICE DEPS image/"
	const copyLine = "COPY LICENSE NOTICE DEPS /usr/share/doc/trustant/"
	if n := strings.Count(read("build.sh"), stage); n != 2 {
		t.Errorf("build.sh stages the legal texts %d times, want 2 (--build and --buildx)", n)
	}
	hotfix := read("hotfix.sh")
	if !strings.Contains(hotfix, stage) || !strings.Contains(hotfix, copyLine) {
		t.Errorf("hotfix.sh must stage the legal texts and COPY them into its layer")
	}
	if !strings.Contains(read("image/Dockerfile"), copyLine) {
		t.Errorf("image/Dockerfile must COPY the legal texts")
	}
}
