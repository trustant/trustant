package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func handleVLLMProxy(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}

	upstream := strings.TrimRight(os.Getenv("TRUSTABLE_VLLM_UPSTREAM"), "/")
	if upstream == "" {
		upstream = "http://vllm:8000"
	}
	target, err := url.Parse(upstream)
	if err != nil {
		http.Error(w, "invalid vLLM upstream", http.StatusInternalServerError)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/vllm")
	if path == "" {
		path = "/"
	}
	upstreamURL := *target
	upstreamURL.Path = strings.TrimRight(target.Path, "/") + path
	upstreamURL.RawQuery = r.URL.RawQuery

	if r.Method == http.MethodPost && path == "/v1/chat/completions" {
		handleVLLMChatCompletionsProxy(w, r, upstreamURL.String())
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	copyProxyHeaders(req.Header, r.Header)
	req.Host = target.Host

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyProxyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	contentType := resp.Header.Get("Content-Type")

	if strings.Contains(contentType, "text/event-stream") {
		streamSanitizedVLLMResponse(w, resp.Body)
		return
	}

	if strings.Contains(contentType, "application/json") || strings.Contains(contentType, "json") {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("vLLM proxy: failed to read JSON response: %s", err)
			return
		}
		w.Write(sanitizeVLLMJSON(body))
		return
	}

	io.Copy(w, resp.Body)
}

func handleVLLMChatCompletionsProxy(w http.ResponseWriter, r *http.Request, upstreamURL string) {
	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var requestPayload map[string]interface{}
	streamingRequest := false
	upstreamBody := requestBody
	if err := json.Unmarshal(requestBody, &requestPayload); err == nil {
		if stream, ok := requestPayload["stream"].(bool); ok && stream {
			streamingRequest = true
			requestPayload["stream"] = false
			delete(requestPayload, "stream_options")
			upstreamBody, err = json.Marshal(requestPayload)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(upstreamBody))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	copyProxyHeaders(req.Header, r.Header)
	req.Header.Del("Content-Length")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		copyProxyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}

	sanitized := sanitizeVLLMJSON(body)
	if !streamingRequest {
		copyProxyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		w.Write(sanitized)
		return
	}

	writeChatCompletionAsSSE(w, sanitized)
}

func writeChatCompletionAsSSE(w http.ResponseWriter, body []byte) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	choices, _ := payload["choices"].([]interface{})
	if len(choices) == 0 {
		writeSSEData(w, payload)
		writeSSEDone(w)
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	for idx, choiceValue := range choices {
		choice, _ := choiceValue.(map[string]interface{})
		message, _ := choice["message"].(map[string]interface{})
		index := idx
		if rawIndex, ok := choice["index"].(float64); ok {
			index = int(rawIndex)
		}
		baseChunk := map[string]interface{}{
			"id":      payload["id"],
			"object":  "chat.completion.chunk",
			"created": payload["created"],
			"model":   payload["model"],
		}

		roleChunk := cloneMap(baseChunk)
		roleChunk["choices"] = []interface{}{
			map[string]interface{}{
				"index": index,
				"delta": map[string]interface{}{"role": "assistant"},
			},
		}
		writeSSEData(w, roleChunk)

		if toolCalls, ok := message["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
			toolChunk := cloneMap(baseChunk)
			toolChunk["choices"] = []interface{}{
				map[string]interface{}{
					"index": index,
					"delta": map[string]interface{}{"tool_calls": toolCalls},
				},
			}
			writeSSEData(w, toolChunk)
		}

		if content, ok := message["content"].(string); ok && content != "" {
			contentChunk := cloneMap(baseChunk)
			contentChunk["choices"] = []interface{}{
				map[string]interface{}{
					"index": index,
					"delta": map[string]interface{}{"content": content},
				},
			}
			writeSSEData(w, contentChunk)
		}

		finishReason := choice["finish_reason"]
		if finishReason == nil {
			finishReason = "stop"
		}
		finishChunk := cloneMap(baseChunk)
		finishChunk["choices"] = []interface{}{
			map[string]interface{}{
				"index":         index,
				"delta":         map[string]interface{}{},
				"finish_reason": finishReason,
			},
		}
		writeSSEData(w, finishChunk)
	}

	writeSSEDone(w)
	if flusher != nil {
		flusher.Flush()
	}
}

func cloneMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func writeSSEData(w http.ResponseWriter, value interface{}) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	w.Write([]byte("data: "))
	w.Write(data)
	w.Write([]byte("\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeSSEDone(w http.ResponseWriter) {
	w.Write([]byte("data: [DONE]\n\n"))
}

func copyProxyHeaders(dst, src http.Header) {
	for key, values := range src {
		lower := strings.ToLower(key)
		if lower == "connection" || lower == "keep-alive" || lower == "proxy-authenticate" ||
			lower == "proxy-authorization" || lower == "te" || lower == "trailer" ||
			lower == "transfer-encoding" || lower == "upgrade" {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func streamSanitizedVLLMResponse(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.HasPrefix(line, []byte("data: ")) {
			payload := bytes.TrimPrefix(line, []byte("data: "))
			if !bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
				payload = sanitizeVLLMJSON(payload)
			}
			w.Write([]byte("data: "))
			w.Write(payload)
			w.Write([]byte("\n\n"))
		} else {
			w.Write(line)
			w.Write([]byte("\n"))
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("vLLM proxy: stream read error: %s", err)
	}
}

func sanitizeVLLMJSON(body []byte) []byte {
	var payload interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return sanitizeChannelMarkers(body)
	}
	payload = sanitizeVLLMPayload(payload)
	data, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return data
}

func sanitizeVLLMPayload(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		delete(typed, "reasoning")
		for key, item := range typed {
			if key == "content" {
				if text, ok := item.(string); ok {
					typed[key] = trimAfterChannelMarker(text)
					continue
				}
			}
			typed[key] = sanitizeVLLMPayload(item)
		}
		return typed
	case []interface{}:
		for idx, item := range typed {
			typed[idx] = sanitizeVLLMPayload(item)
		}
		return typed
	default:
		return typed
	}
}

func sanitizeChannelMarkers(body []byte) []byte {
	return []byte(trimAfterChannelMarker(string(body)))
}

func trimAfterChannelMarker(text string) string {
	const marker = "<channel|>"
	if idx := strings.LastIndex(text, marker); idx >= 0 {
		return strings.TrimSpace(text[idx+len(marker):])
	}
	return text
}
