package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
)

// Shared server-sent-events progress writer.
//
// Launch (spec/4-launch.md) and publish (spec/6-publish.md) both stream their
// progress over the same wire format: `progress` events while the work runs,
// `output` events carrying command output line by line, and a terminal
// `done`/`error` event holding the JSON body the non-streaming path would have
// returned. `total` is supplied by the caller, so each feature keeps its own
// stage count.
//
// Handlers stay written against http.ResponseWriter: when the client does not
// ask for text/event-stream the plain writer is returned unchanged and the
// helpers below become no-ops.

type progressReporter interface {
	reportProgress(stage int, message string)
	reportOutput(line string)
}

type progressSSEResponseWriter struct {
	http.ResponseWriter
	flusher http.Flusher
	total   int
	// Subprocess output is streamed from reader goroutines while the handler
	// goroutine reports stages, so every send is serialized through mu.
	mu     sync.Mutex
	buffer []byte
	// redact holds secrets (production passwords) that must never be written
	// to the stream. See spec/6-publish.md.
	redact []string
}

// prepareProgressResponseWriter upgrades w to an SSE writer when the request
// accepts text/event-stream. The bool is false only when streaming was
// requested but is unsupported, in which case the error response is already
// written.
//
// IMPORTANT: this flushes the response headers, and Go's HTTP server stops
// making an unread request body available once the response is committed. Any
// handler with a request body must therefore decode r.Body BEFORE calling
// this, or the decode fails with an EOF. See spec/6-publish.md.
func prepareProgressResponseWriter(w http.ResponseWriter, r *http.Request, total int, unsupportedMessage string) (http.ResponseWriter, bool) {
	if !strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		return w, true
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, unsupportedMessage, http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()
	return &progressSSEResponseWriter{ResponseWriter: w, flusher: flusher, total: total}, true
}

// redactSecrets registers values to strip from streamed output.
func redactSecrets(w http.ResponseWriter, secrets ...string) {
	writer, ok := w.(*progressSSEResponseWriter)
	if !ok {
		return
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			writer.redact = append(writer.redact, secret)
		}
	}
}

func (w *progressSSEResponseWriter) scrub(text string) string {
	for _, secret := range w.redact {
		text = strings.ReplaceAll(text, secret, "********")
	}
	return text
}

func (w *progressSSEResponseWriter) send(event string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w.ResponseWriter, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

func (w *progressSSEResponseWriter) reportProgress(stage int, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.send("progress", map[string]interface{}{"stage": stage, "total": w.total, "message": w.scrub(message)})
}

func (w *progressSSEResponseWriter) reportOutput(line string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.send("output", map[string]interface{}{"line": w.scrub(line)})
}

// Write turns the handler's final JSON body into the terminal SSE event. The
// body arrives in arbitrarily many chunks, so it is buffered until it parses as
// JSON.
func (w *progressSSEResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, data...)
	if !json.Valid(w.buffer) {
		return len(data), nil
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(w.buffer, &payload); err != nil {
		return 0, err
	}
	event := "done"
	if _, failed := payload["error"]; failed {
		event = "error"
	}
	w.buffer = nil
	if text, ok := payload["output"].(string); ok {
		payload["output"] = w.scrub(text)
	}
	if err := w.send(event, payload); err != nil {
		return 0, err
	}
	return len(data), nil
}

func reportProgress(w http.ResponseWriter, stage int, message string) {
	if reporter, ok := w.(progressReporter); ok {
		reporter.reportProgress(stage, message)
	}
}

func reportOutput(w http.ResponseWriter, line string) {
	if reporter, ok := w.(progressReporter); ok {
		reporter.reportOutput(line)
	}
}

// progressOutputWriter accumulates subprocess output into a buffer (which still
// populates the terminal payload's `output` field) while emitting each complete
// line as an `output` event. Partial lines are held back until their newline
// arrives so the stream never splits a line in two.
type progressOutputWriter struct {
	response http.ResponseWriter
	sink     *bytes.Buffer
	pending  []byte
	mu       sync.Mutex
}

func newProgressOutputWriter(w http.ResponseWriter, sink *bytes.Buffer) *progressOutputWriter {
	return &progressOutputWriter{response: w, sink: sink}
}

func (w *progressOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sink != nil {
		w.sink.Write(data)
	}
	w.pending = append(w.pending, data...)
	for {
		index := bytes.IndexByte(w.pending, '\n')
		if index == -1 {
			break
		}
		line := strings.TrimRight(string(w.pending[:index]), "\r")
		w.pending = w.pending[index+1:]
		reportOutput(w.response, line)
	}
	return len(data), nil
}

// flush emits any trailing output that never ended with a newline.
func (w *progressOutputWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return
	}
	line := strings.TrimRight(string(w.pending), "\r")
	w.pending = nil
	if strings.TrimSpace(line) != "" {
		reportOutput(w.response, line)
	}
}

// runStreamingCommand runs cmd with both streams piped into the progress
// writer, replacing the CombinedOutput() calls that buffered to the end and so
// showed the user nothing until the command finished.
func runStreamingCommand(w http.ResponseWriter, sink *bytes.Buffer, cmd *exec.Cmd) error {
	stream := newProgressOutputWriter(w, sink)
	cmd.Stdout = stream
	cmd.Stderr = stream
	err := cmd.Run()
	stream.flush()
	return err
}
