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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func resetUILocation(t *testing.T) {
	t.Helper()
	uiLocationState.Lock()
	uiLocationState.loc = uiLocation{}
	uiLocationState.Unlock()
	savedNow := uiNow
	t.Cleanup(func() {
		uiNow = savedNow
		uiLocationState.Lock()
		uiLocationState.loc = uiLocation{}
		uiLocationState.Unlock()
	})
}

func postLocation(t *testing.T, body string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	handleUILocation(rec, httptest.NewRequest(http.MethodPost, "/api/v1/location", strings.NewReader(body)))
	return rec.Code
}

func getLocation(t *testing.T) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	handleUILocation(rec, httptest.NewRequest(http.MethodGet, "http://trustant.10.0.0.5.nip.io:8910/api/v1/location", nil))
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("GET location: %s %s", err, rec.Body.String())
	}
	return v
}

func TestUILocationTracksPageAndExpires(t *testing.T) {
	mcpTestEnv(t)
	resetUILocation(t)

	if v := getLocation(t); v["active"] != false {
		t.Fatalf("no report yet must be inactive: %v", v)
	}
	if code := postLocation(t, `{"page":"app","app":"myapp01","route":"/todos#x","query":"id=3"}`); code != http.StatusOK {
		t.Fatalf("POST: %d", code)
	}
	v := getLocation(t)
	if v["active"] != true || v["page"] != "app" || v["app"] != "myapp01" || v["route"] != "/todos" {
		t.Fatalf("location: %v", v)
	}
	urls := v["urls"].(map[string]any)
	if urls["app"] != "http://vite.10.0.0.5.nip.io:8910/todos?id=3#/todos" {
		t.Fatalf("urls.app must match app.html's iframe URL: %v", urls["app"])
	}
	if urls["acp"] != "http://truacp.10.0.0.5.nip.io:8910/" {
		t.Fatalf("urls.acp: %v", urls["acp"])
	}
	if urls["split"] != "http://trustant.10.0.0.5.nip.io:8910/split.html?query=id%3D3&route=%2Ftodos" {
		t.Fatalf("urls.split must carry the route (the agent browser has no cookies): %v", urls["split"])
	}
	if !strings.HasSuffix(urls["split_acp"].(string), "view=acp") {
		t.Fatalf("urls.split_acp: %v", urls["split_acp"])
	}

	// The app list: no route, URLs fall back to the launched app at "/".
	postLocation(t, `{"page":"applist"}`)
	v = getLocation(t)
	if v["page"] != "applist" || v["launched"] != "myapp01" || v["urls"].(map[string]any)["app"] != "http://vite.10.0.0.5.nip.io:8910/#/" {
		t.Fatalf("applist location: %v", v)
	}

	// No heartbeat for longer than the window: no Trustant tab is open.
	uiNow = func() time.Time { return time.Now().Add(uiLocationActiveWindow + time.Second) }
	if v := getLocation(t); v["active"] != false {
		t.Fatalf("stale report must be inactive: %v", v)
	}
}

func TestUILocationValidates(t *testing.T) {
	mcpTestEnv(t)
	resetUILocation(t)
	for _, body := range []string{`{"page":"evil"}`, `{"page":"app","app":"../x"}`, `not json`} {
		if code := postLocation(t, body); code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", body, code)
		}
	}
	postLocation(t, `{"page":"app","app":"myapp01","route":"//evil.com/x"}`)
	if r := currentUILocation().Route; r != "/" {
		t.Fatalf("a protocol-relative route must be dropped, got %q", r)
	}
}

func TestUILocationMCPTool(t *testing.T) {
	mcpTestEnv(t)
	resetUILocation(t)
	postLocation(t, `{"page":"app","app":"myapp01","route":"/about"}`)
	cs := connectTestMCP(t)
	out, isErr := callText(t, cs, "ui_location", nil)
	if isErr || !strings.Contains(out, `"page": "app"`) || !strings.Contains(out, "/split.html?route=%2Fabout") || !strings.Contains(out, "truacp.") {
		t.Fatalf("ui_location: %s", out)
	}
}

func TestSplitPageIsChromeFree(t *testing.T) {
	data, err := os.ReadFile("web/split.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	appAt, acpAt := strings.Index(page, `id="appFrame"`), strings.Index(page, `id="acpFrame"`)
	if appAt < 0 || acpAt < 0 || appAt > acpAt {
		t.Fatal("split.html must stack the app above the ACP")
	}
	for _, chrome := range []string{"<header", "<nav", `src="trustant-auth.js"`, `src="tailwind.js"`, `src="js/location.js"`} {
		if strings.Contains(page, chrome) {
			t.Errorf("split.html must stay chrome- and dependency-free, found %q", chrome)
		}
	}
	if !publicAuthPath("/split.html") {
		t.Error("split.html must be public: the agent's browser has no Trustant session")
	}
	for _, p := range []string{"web/applist.html", "web/app.html", "web/configure.html", "web/index.html"} {
		html, _ := os.ReadFile(p)
		if !strings.Contains(string(html), `<script src="js/location.js"></script>`) {
			t.Errorf("%s must load js/location.js", p)
		}
	}
}
