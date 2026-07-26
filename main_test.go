package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoStoreHTMLPreventsStaleConfigurationFlow(t *testing.T) {
	handler := noStoreHTML(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/", "/applist.html", "/configure.html?reselect=1"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s Cache-Control = %q, want no-store", path, got)
		}
	}

	// Assets can still use their normal HTTP caching semantics; only documents
	// carry inline boot logic capable of redirecting between configuration pages.
	request := httptest.NewRequest(http.MethodGet, "/trustable-ui.css", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("CSS Cache-Control = %q, want the file server default", got)
	}
}
