package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ipPattern matches IPv4 addresses
var ipPattern = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)

// reverse proxy instances for opencode and vite
var opencodeProxy = newSilentProxy("localhost:4096")
var viteProxy = newSilentProxy("localhost:5173")

type opencodeSessionSummary struct {
	ID string `json:"id"`
}

// newSilentProxy creates a reverse proxy that silently returns 502 when the backend is unavailable
func newSilentProxy(host string) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: host})
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.WriteHeader(http.StatusBadGateway)
	}
	return proxy
}

// parseHostname extracts hostname, port, and protocol from a request
func parseHostname(r *http.Request) (hostname, port, protocol string) {
	host := r.Host
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		hostname = host
		port = ""
	}

	protocol = "http"
	if r.TLS != nil {
		protocol = "https"
	}
	if fwdProto := r.Header.Get("X-Forwarded-Proto"); fwdProto != "" {
		protocol = fwdProto
	}

	return hostname, port, protocol
}

func latestOpenCodeSessionID(directory string) string {
	if directory == "" {
		return ""
	}
	sessionURL := fmt.Sprintf("http://localhost:4096/session?directory=%s&roots=true&limit=1", url.QueryEscape(directory))
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(sessionURL)
	if err != nil {
		log.Printf("opencode redirect: failed to query latest session for %s: %s", directory, err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("opencode redirect: latest session query for %s returned %d", directory, resp.StatusCode)
		return ""
	}
	var sessions []opencodeSessionSummary
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		log.Printf("opencode redirect: failed to decode latest session for %s: %s", directory, err)
		return ""
	}
	if len(sessions) == 0 {
		return ""
	}
	return sessions[0].ID
}

func decodeOpenCodeDirectory(encoded string) string {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	directory := string(decoded)
	if !filepath.IsAbs(directory) {
		return ""
	}
	return directory
}

func currentOpenCodeDirectory() (encodedDir, directory string) {
	app, err := readCurrentApp()
	if err != nil || app == "" {
		return "", ""
	}
	directory, err = filepath.Abs(filepath.Join(WorkbenchDir, app))
	if err != nil {
		return "", ""
	}
	encodedDir = base64.RawURLEncoding.EncodeToString([]byte(directory))
	return encodedDir, directory
}

func redirectToLatestOpenCodeSession(w http.ResponseWriter, r *http.Request, encodedDir, directory string) bool {
	sessionID := latestOpenCodeSessionID(directory)
	if sessionID == "" {
		return false
	}

	target := fmt.Sprintf("/%s/session/%s", encodedDir, url.PathEscape(sessionID))
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	return true
}

func redirectOpenCodeSession(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}

	currentEncodedDir, currentDirectory := currentOpenCodeDirectory()
	if currentDirectory == "" {
		return false
	}

	if r.URL.Path == "/" {
		return redirectToLatestOpenCodeSession(w, r, currentEncodedDir, currentDirectory)
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[1] != "session" {
		return false
	}
	if len(parts) > 3 {
		return false
	}

	requestedEncodedDir := parts[0]
	requestedDirectory := decodeOpenCodeDirectory(requestedEncodedDir)
	if requestedDirectory == "" {
		return false
	}
	if requestedDirectory != currentDirectory {
		return redirectToLatestOpenCodeSession(w, r, currentEncodedDir, currentDirectory)
	}
	if len(parts) == 2 {
		return redirectToLatestOpenCodeSession(w, r, requestedEncodedDir, requestedDirectory)
	}
	return false
}

func canonicalPath(path string) string {
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func samePath(a, b string) bool {
	return canonicalPath(a) == canonicalPath(b)
}

func rewriteOpenCodeDirectoryQueryToCurrent(r *http.Request) {
	q := r.URL.Query()
	directory := q.Get("directory")
	if directory == "" {
		return
	}
	_, currentDirectory := currentOpenCodeDirectory()
	if currentDirectory == "" || samePath(directory, currentDirectory) {
		return
	}
	q.Set("directory", currentDirectory)
	r.URL.RawQuery = q.Encode()
	log.Printf("opencode: rewrote directory %s to current app %s", directory, currentDirectory)
}

func handleScopedOpenCodeProjectList(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet || r.URL.Path != "/project" {
		return false
	}
	_, currentDirectory := currentOpenCodeDirectory()
	if currentDirectory == "" {
		return false
	}

	reqURL := fmt.Sprintf("http://localhost:4096/project/current?directory=%s", url.QueryEscape(currentDirectory))
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(reqURL)
	if err != nil {
		log.Printf("opencode project list: failed to query current project for %s: %s", currentDirectory, err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("opencode project list: current project query for %s returned %d", currentDirectory, resp.StatusCode)
		return false
	}
	var project json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		log.Printf("opencode project list: failed to decode current project for %s: %s", currentDirectory, err)
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("["))
	w.Write(project)
	w.Write([]byte("]"))
	return true
}

// hostnameMiddleware wraps an http.Handler with hostname verification, IP redirect, and host-based routing
func hostnameMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostname, port, protocol := parseHostname(r)

		// If hostname is "localhost", redirect to trustable.127.0.0.1.nip.io
		if hostname == "localhost" {
			hostname = "127.0.0.1"
		}

		// If the hostname is an IP address, redirect to trustable.<ip>.nip.io format
		if ipPattern.MatchString(hostname) {
			var redirectURL string
			if port != "" {
				redirectURL = fmt.Sprintf("%s://trustable.%s.nip.io:%s%s", protocol, hostname, port, r.URL.RequestURI())
			} else {
				redirectURL = fmt.Sprintf("%s://trustable.%s.nip.io%s", protocol, hostname, r.URL.RequestURI())
			}
			log.Printf("Redirecting IP-based URL to: %s", redirectURL)
			http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
			return
		}

		// Parse hostname as <host>.<domain> (host has no dots, domain can have dots)
		dotIdx := strings.Index(hostname, ".")
		if dotIdx == -1 {
			// No dot found - not a valid FQDN, redirect to trustable.<ip>.nip.io
			localHostname := getLocalHostname()
			var redirectURL string
			if port != "" {
				redirectURL = fmt.Sprintf("%s://trustable.%s.nip.io:%s%s", protocol, localHostname, port, r.URL.RequestURI())
			} else {
				redirectURL = fmt.Sprintf("%s://trustable.%s.nip.io%s", protocol, localHostname, r.URL.RequestURI())
			}
			log.Printf("Redirecting plain hostname %s to: %s", hostname, redirectURL)
			http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
			return
		}

		hostPart := hostname[:dotIdx]

		switch hostPart {
		case "trustable":
			// Serve the web folder (static files + API)
			next.ServeHTTP(w, r)
		case "opencode":
			if redirectOpenCodeSession(w, r) {
				return
			}
			if handleScopedOpenCodeProjectList(w, r) {
				return
			}
			rewriteOpenCodeDirectoryQueryToCurrent(r)
			// Proxy pass to port 4096
			opencodeProxy.ServeHTTP(w, r)
		case "vite":
			// Proxy pass to port 5173
			viteProxy.ServeHTTP(w, r)
		default:
			// Unknown host prefix - show error
			domain := hostname[dotIdx+1:]
			var suggestedURL string
			if port != "" {
				suggestedURL = fmt.Sprintf("%s://trustable.%s:%s", protocol, domain, port)
			} else {
				suggestedURL = fmt.Sprintf("%s://trustable.%s", protocol, domain)
			}

			errorMsg := fmt.Sprintf("Invalid hostname. Please use %s", suggestedURL)
			log.Printf("Hostname verification failed: %s (unknown host prefix: %s)", hostname, hostPart)

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>Invalid Hostname</title>
    <style>
        body { font-family: sans-serif; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; background: #f5f5f5; }
        .error { text-align: center; background: white; padding: 40px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        h1 { color: #e53935; }
        a { color: #1976d2; }
        code { background: #f5f5f5; padding: 2px 8px; border-radius: 4px; }
    </style>
</head>
<body>
    <div class="error">
        <h1>Invalid Hostname</h1>
        <p>%s</p>
        <p>Current hostname: <code>%s</code></p>
        <p><a href="%s">Click here to use the correct URL</a></p>
    </div>
</body>
</html>`, errorMsg, hostname, suggestedURL)
			return
		}
	})
}

// getLocalHostname returns the first IP address from 'hostname -I'
func getLocalHostname() string {
	cmd := exec.Command("hostname", "-I")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Warning: failed to get local hostname: %v", err)
		return "localhost"
	}

	// Get the first field (first IP address)
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) == 0 {
		return "localhost"
	}

	return fields[0]
}
