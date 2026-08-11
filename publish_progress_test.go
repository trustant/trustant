package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The publish handlers stream the same SSE contract as launch: `progress`
// events while the work runs, `output` events per command line, and a terminal
// `done`/`error` carrying the JSON body. See spec/6-publish.md.

func TestPublishProgressSSEContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/publish/remote", nil)
	request.Header.Set("Accept", "text/event-stream")
	writer, ok := preparePublishResponseWriter(recorder, request, publishRemoteProgressTotal)
	if !ok {
		t.Fatal("expected streaming response writer")
	}
	reportProgress(writer, 5, "Connecting to OpenServerless...")
	reportOutput(writer, "deploying action hello")
	if err := json.NewEncoder(writer).Encode(map[string]interface{}{"message": "Published successfully", "output": "done"}); err != nil {
		t.Fatalf("encode publish result: %v", err)
	}
	body := recorder.Body.String()
	for _, expected := range []string{
		"event: progress",
		"\"stage\":5",
		"\"total\":6",
		"Connecting to OpenServerless...",
		"event: output",
		"\"line\":\"deploying action hello\"",
		"event: done",
		"Published successfully",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("stream does not contain %q: %s", expected, body)
		}
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
}

func TestPublishPushProgressTotalIsThree(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/publish/push", nil)
	request.Header.Set("Accept", "text/event-stream")
	writer, ok := preparePublishResponseWriter(recorder, request, publishPushProgressTotal)
	if !ok {
		t.Fatal("expected streaming response writer")
	}
	reportProgress(writer, 3, "Pushing to GitHub...")
	if body := recorder.Body.String(); !strings.Contains(body, "\"total\":3") {
		t.Fatalf("push stream total is not 3: %s", body)
	}
}

func TestPublishProgressSSEPreservesErrorPayload(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/publish/remote", nil)
	request.Header.Set("Accept", "text/event-stream")
	writer, ok := preparePublishResponseWriter(recorder, request, publishRemoteProgressTotal)
	if !ok {
		t.Fatal("expected streaming response writer")
	}
	writePublishJSON(writer, 500, map[string]string{"error": "Deploy failed: exit 1", "output": "boom"})
	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "Deploy failed: exit 1") {
		t.Fatalf("stream did not preserve publish error payload: %s", body)
	}
	// The SSE response is committed as 200; the outcome rides in the event.
	if recorder.Code != 200 {
		t.Fatalf("streaming status = %d, want 200", recorder.Code)
	}
}

// The license-required prefix is what the frontend keys its license modal on,
// so it must survive the streaming path verbatim. See spec/14-license.md.
func TestPublishProgressPreservesLicenseErrorPrefix(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/publish/remote", nil)
	request.Header.Set("Accept", "text/event-stream")
	writer, _ := preparePublishResponseWriter(recorder, request, publishRemoteProgressTotal)
	writePublishJSON(writer, 402, map[string]string{"error": "License required: expired"})
	if body := recorder.Body.String(); !strings.Contains(body, "License required: expired") {
		t.Fatalf("license error prefix lost: %s", body)
	}
}

// Request-validation failures must arrive as a terminal `error` event. Using
// http.Error here would write a plain-text body with no `data:` line, leaving
// the client with a dead stream and no reason for the failure.
func TestPublishValidationErrorReachesStreamAsEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/publish/push", nil)
	request.Header.Set("Accept", "text/event-stream")
	writer, _ := preparePublishResponseWriter(recorder, request, publishPushProgressTotal)
	reportProgress(writer, 1, "Checking license and repository configuration...")
	writePublishError(writer, "Invalid name format", 400)

	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Fatalf("validation failure did not produce a terminal error event: %s", body)
	}
	if !strings.Contains(body, "Invalid name format") {
		t.Fatalf("validation message lost from stream: %s", body)
	}
}

// The same helper keeps plain-text semantics when not streaming.
func TestPublishValidationErrorStaysPlainWithoutStreaming(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/publish/push", nil)
	writer, _ := preparePublishResponseWriter(recorder, request, publishPushProgressTotal)
	writePublishError(writer, "Invalid name format", 400)
	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Invalid name format") {
		t.Fatalf("plain error body lost: %s", recorder.Body.String())
	}
}

// Requests without the Accept header keep the plain JSON behaviour, which is
// what the needs_config probe relies on.
func TestPublishWithoutAcceptHeaderStaysJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/publish/remote", nil)
	writer, ok := preparePublishResponseWriter(recorder, request, publishRemoteProgressTotal)
	if !ok {
		t.Fatal("expected plain response writer")
	}
	if _, streaming := writer.(*progressSSEResponseWriter); streaming {
		t.Fatal("request without Accept header must not be upgraded to SSE")
	}
	reportProgress(writer, 2, "Preparing workbench...")
	writePublishJSON(writer, 200, map[string]interface{}{"needs_config": true})
	body := recorder.Body.String()
	if strings.Contains(body, "event:") {
		t.Fatalf("non-streaming response leaked SSE events: %s", body)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("non-streaming body is not JSON: %s", body)
	}
	if payload["needs_config"] != true {
		t.Fatalf("needs_config not preserved: %s", body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

// OPS_PASSWORD is user-supplied and must never reach the stream.
func TestPublishProgressRedactsPassword(t *testing.T) {
	const secret = "sup3r-s3cret-pw"
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/publish/remote", nil)
	request.Header.Set("Accept", "text/event-stream")
	writer, _ := preparePublishResponseWriter(recorder, request, publishRemoteProgressTotal)
	redactSecrets(writer, secret)

	reportOutput(writer, "ops ide login --password "+secret)
	reportProgress(writer, 5, "logging in as "+secret)
	var sink bytes.Buffer
	stream := newProgressOutputWriter(writer, &sink)
	stream.Write([]byte("echoed " + secret + "\n"))
	writePublishJSON(writer, 500, map[string]string{"error": "Login failed", "output": "trace " + secret})

	if body := recorder.Body.String(); strings.Contains(body, secret) {
		t.Fatalf("password leaked into publish stream: %s", body)
	}
}

func TestPublishOutputWriterStreamsLinesAndAccumulates(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/publish/push", nil)
	request.Header.Set("Accept", "text/event-stream")
	writer, _ := preparePublishResponseWriter(recorder, request, publishPushProgressTotal)

	var sink bytes.Buffer
	stream := newProgressOutputWriter(writer, &sink)
	// A line split across writes must be emitted once, whole.
	stream.Write([]byte("first line\nsecond "))
	stream.Write([]byte("line\ntrailing without newline"))
	stream.flush()

	body := recorder.Body.String()
	for _, expected := range []string{"\"line\":\"first line\"", "\"line\":\"second line\"", "\"line\":\"trailing without newline\""} {
		if !strings.Contains(body, expected) {
			t.Errorf("stream does not contain %q: %s", expected, body)
		}
	}
	if strings.Count(body, "event: output") != 3 {
		t.Errorf("expected 3 output events, got %d: %s", strings.Count(body, "event: output"), body)
	}
	// The buffer still holds everything for the terminal payload.
	if got := sink.String(); got != "first line\nsecond line\ntrailing without newline" {
		t.Errorf("accumulated output = %q", got)
	}
}

func TestApplicationListPublishProgressWiring(t *testing.T) {
	content, err := os.ReadFile("web/applist.html")
	if err != nil {
		t.Fatalf("read application list: %v", err)
	}
	html := string(content)
	for _, expected := range []string{
		`id="publishProgressBar"`,
		`id="publishProgressTrack"`,
		`id="publishOutput"`,
		`id="publishOutputToggle"`,
		`id="gitPushProgressBar"`,
		`id="gitPushOutput"`,
		`role="progressbar"`,
		`aria-expanded="false"`,
		"function fetchPublishResponse",
		"function updatePublishProgress",
		"Show output",
		"'Accept': 'text/event-stream'",
		"response.body.getReader()",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("application list does not contain %q", expected)
		}
	}
	// All three publish call sites must go through the streaming fetch.
	if count := strings.Count(html, "fetchPublishResponse('/api/publish/"); count != 3 {
		t.Errorf("expected 3 streaming publish call sites, found %d", count)
	}
}
