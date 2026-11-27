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
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const provisionTestHost = "http://cluster.test"

// fakeAdminAPI serves the provisioning endpoint and the app host. Every
// hostname is dialed to it, so "<user>.cluster.test" reaches it too.
type fakeAdminAPI struct {
	createStatus int
	readyAfter   int32 // app answers 200 from this poll on
	polls        atomic.Int32
	gotAuth      string
	gotBody      map[string]string
}

func startFakeAdminAPI(t *testing.T, f *fakeAdminAPI) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == provisionUsersPath && r.Host == "cluster.test" {
			f.gotAuth = r.Header.Get("Authorization")
			json.NewDecoder(r.Body).Decode(&f.gotBody)
			w.WriteHeader(f.createStatus)
			w.Write([]byte(`{"status":"ko","message":"fake"}`))
			return
		}
		if strings.HasSuffix(r.Host, ".cluster.test") {
			if f.polls.Add(1) >= f.readyAfter {
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	origClient, origTimeout, origInterval := provisionHTTPClient, provisionReadyTimeout, provisionPollInterval
	provisionHTTPClient = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}}
	provisionReadyTimeout = 2 * time.Second
	provisionPollInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		provisionHTTPClient, provisionReadyTimeout, provisionPollInterval = origClient, origTimeout, origInterval
	})
}

// setupProvisionWorkspace installs a license for provisionTestHost and an app.
func setupProvisionWorkspace(t *testing.T, token string) {
	t.Helper()
	priv := withTestMasterKey(t)
	license := makeLicense(t, priv, licensePayload{V: 1, Sub: "acme", Email: "acme@example.com", Hosts: []string{provisionTestHost}})
	useTestWorkspace(t, "")
	rec := httptest.NewRecorder()
	handleLicense(rec, httptest.NewRequest(http.MethodPost, "/api/license", strings.NewReader(`{"license":"`+license+`"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST license = %d: %s", rec.Code, rec.Body.String())
	}
	cfg, _ := loadWorkspaceConfig()
	cfg.ProvisioningToken = token
	cfg.Apps = map[string]*AppConfig{"MyApp01": {Password: "s3cretpass"}}
	if err := saveWorkspaceConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func provision(t *testing.T, body string) (int, map[string]interface{}, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	handlePublishProvision(rec, httptest.NewRequest(http.MethodPost, "/api/publish/provision", strings.NewReader(body)))
	var out map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func TestLicenseSaveSeedsHostAndEmail(t *testing.T) {
	setupProvisionWorkspace(t, "")
	st := currentLicenseStatus()
	if st.LicenseHost != provisionTestHost || st.LicenseEmail != "acme@example.com" || st.ProvisioningTokenSet {
		t.Fatalf("status = %+v", st)
	}
}

func TestLicenseSettings(t *testing.T) {
	setupProvisionWorkspace(t, "")
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handleLicenseSettings(rec, httptest.NewRequest(http.MethodPost, "/api/license/settings", strings.NewReader(body)))
		return rec
	}
	if rec := post(`{"license_host":"https://other.example.com","license_email":"a@b.io"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unlicensed host accepted: %d", rec.Code)
	}
	if rec := post(`{"license_host":"` + provisionTestHost + `","license_email":"not-an-email"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad email accepted: %d", rec.Code)
	}
	rec := post(`{"license_host":"` + provisionTestHost + `","license_email":"b@c.io","provisioning_token":"tok"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "tok\"") {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	cfg, _ := loadWorkspaceConfig()
	if cfg.LicenseEmail != "b@c.io" || cfg.ProvisioningToken != "tok" {
		t.Fatalf("cfg = %+v", cfg)
	}
	// An empty token keeps the stored one; clear_token removes it.
	post(`{"license_host":"` + provisionTestHost + `","license_email":"b@c.io"}`)
	if cfg, _ := loadWorkspaceConfig(); cfg.ProvisioningToken != "tok" {
		t.Error("empty token cleared the stored one")
	}
	post(`{"license_host":"` + provisionTestHost + `","license_email":"b@c.io","clear_token":true}`)
	if cfg, _ := loadWorkspaceConfig(); cfg.ProvisioningToken != "" {
		t.Error("clear_token did not clear")
	}
}

func TestProvisionNeedsToken(t *testing.T) {
	setupProvisionWorkspace(t, "")
	_, out, _ := provision(t, `{"name":"MyApp01"}`)
	if out["needs_token"] != true || out["host"] != provisionTestHost {
		t.Fatalf("out = %v", out)
	}
}

func TestProvisionCreatesAndWaits(t *testing.T) {
	f := &fakeAdminAPI{createStatus: http.StatusAccepted, readyAfter: 3}
	startFakeAdminAPI(t, f)
	setupProvisionWorkspace(t, "")
	code, out, raw := provision(t, `{"name":"MyApp01","token":"provtok"}`)
	if code != http.StatusOK || out["error"] != nil {
		t.Fatalf("code %d: %s", code, raw)
	}
	if f.gotAuth != "Bearer provtok" || f.gotBody["user"] != "myapp01" || f.gotBody["password"] != "s3cretpass" ||
		f.gotBody["email"] != "acme@example.com" || f.gotBody["services"] != "all" {
		t.Fatalf("request auth=%q body=%v", f.gotAuth, f.gotBody)
	}
	if f.polls.Load() < 3 {
		t.Errorf("polled %d times, want >= 3", f.polls.Load())
	}
	if out["apihost"] != provisionTestHost || out["user"] != "myapp01" || out["password"] != "s3cretpass" || out["exists"] != false {
		t.Errorf("out = %v", out)
	}
	cfg, _ := loadWorkspaceConfig()
	prod := cfg.Apps["MyApp01"].Production
	if cfg.ProvisioningToken != "provtok" || prod["OPS_APIHOST"] != provisionTestHost || prod["OPS_USER"] != "myapp01" || prod["OPS_PASSWORD"] != "s3cretpass" {
		t.Errorf("cfg token=%q prod=%v", cfg.ProvisioningToken, prod)
	}
}

func TestProvisionExistingUserWarns(t *testing.T) {
	startFakeAdminAPI(t, &fakeAdminAPI{createStatus: http.StatusConflict, readyAfter: 1})
	setupProvisionWorkspace(t, "provtok")
	_, out, raw := provision(t, `{"name":"MyApp01"}`)
	if out["exists"] != true || !strings.Contains(raw, "override") {
		t.Fatalf("out = %s", raw)
	}
}

func TestProvisionInvalidToken(t *testing.T) {
	startFakeAdminAPI(t, &fakeAdminAPI{createStatus: http.StatusUnauthorized, readyAfter: 1})
	setupProvisionWorkspace(t, "provtok")
	_, out, raw := provision(t, `{"name":"MyApp01"}`)
	if out["needs_token"] != true || out["token_invalid"] != true || out["error"] != nil {
		t.Fatalf("out = %s", raw)
	}
	// The rejected token is removed so the modal asks for it again.
	if cfg, _ := loadWorkspaceConfig(); cfg.ProvisioningToken != "" {
		t.Errorf("invalid token kept: %q", cfg.ProvisioningToken)
	}
}

func TestProvisionTimeout(t *testing.T) {
	startFakeAdminAPI(t, &fakeAdminAPI{createStatus: http.StatusAccepted, readyAfter: 1 << 30})
	setupProvisionWorkspace(t, "provtok")
	provisionReadyTimeout = 100 * time.Millisecond
	_, out, raw := provision(t, `{"name":"MyApp01"}`)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "not ready") {
		t.Fatalf("out = %s", raw)
	}
	cfg, _ := loadWorkspaceConfig()
	if cfg.Apps["MyApp01"].Production["OPS_USER"] != "" {
		t.Error("production config saved although the app never became ready")
	}
}

func TestProvisionStreamRedactsToken(t *testing.T) {
	startFakeAdminAPI(t, &fakeAdminAPI{createStatus: http.StatusAccepted, readyAfter: 1})
	setupProvisionWorkspace(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/publish/provision", strings.NewReader(`{"name":"MyApp01","token":"provtok-secret"}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	handlePublishProvision(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "event: done") || strings.Contains(body, "provtok-secret") {
		t.Fatalf("stream = %s", body)
	}
}
