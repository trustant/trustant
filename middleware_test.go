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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestHostnameMiddlewareProxiesExistingDevelopmentAppToManagedAPIHost(t *testing.T) {
	originalWorkspace := WorkspaceDir
	originalTransport := developmentProxyTransport
	WorkspaceDir = t.TempDir()
	t.Cleanup(func() {
		WorkspaceDir = originalWorkspace
		developmentProxyTransport = originalTransport
	})
	t.Setenv("OPS_APIHOST", "http://miniops.me")

	repoDir := filepath.Join(WorkspaceDir, "workspace", "trutest1")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir repo: %s", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/trustable-ai/trureact.git\n"), 0644); err != nil {
		t.Fatalf("write repo config: %s", err)
	}

	var upstreamURL, upstreamHost, forwardedHost string
	developmentProxyTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		upstreamURL = request.URL.String()
		upstreamHost = request.Host
		forwardedHost = request.Header.Get("X-Forwarded-Host")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("development app")),
			Request:    request,
		}, nil
	})

	handler := hostnameMiddleware(http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodGet, "http://trutest1.192.168.64.9.nip.io:8910/dashboard?q=1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "development app" {
		t.Fatalf("response = %d %q, want 200 development app", response.Code, response.Body.String())
	}
	if upstreamURL != "http://trutest1.miniops.me/dashboard?q=1" {
		t.Fatalf("upstream URL = %q", upstreamURL)
	}
	if upstreamHost != "trutest1.miniops.me" {
		t.Fatalf("upstream Host = %q", upstreamHost)
	}
	if forwardedHost != "trutest1.192.168.64.9.nip.io:8910" {
		t.Fatalf("X-Forwarded-Host = %q", forwardedHost)
	}
}
