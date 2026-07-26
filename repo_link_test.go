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
