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

import "testing"

func TestDevelopmentApplicationURLUsesManagedAPIHost(t *testing.T) {
	t.Setenv("OPS_APIHOST", "http://miniops.me")

	if got, want := developmentApplicationURL("trutest1"), "http://trutest1.miniops.me/"; got != want {
		t.Fatalf("developmentApplicationURL() = %q, want %q", got, want)
	}
}

func TestDevelopmentApplicationURLPreservesConfiguredProtocolAndPort(t *testing.T) {
	t.Setenv("OPS_APIHOST", "https://cluster.example.test:8443/ignored/path?query=1")

	if got, want := developmentApplicationURL("demoapp"), "https://demoapp.cluster.example.test:8443/"; got != want {
		t.Fatalf("developmentApplicationURL() = %q, want %q", got, want)
	}
}
