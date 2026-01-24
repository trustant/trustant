package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
)

// ipPattern matches IPv4 addresses
var ipPattern = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)

// hostnameMiddleware wraps an http.Handler with hostname verification and IP redirect logic
func hostnameMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host

		// Extract host without port
		hostname, port, err := net.SplitHostPort(host)
		if err != nil {
			// No port in host header
			hostname = host
			port = ""
		}

		// Get the protocol (scheme)
		protocol := "http"
		if r.TLS != nil {
			protocol = "https"
		}
		// Also check X-Forwarded-Proto header
		if fwdProto := r.Header.Get("X-Forwarded-Proto"); fwdProto != "" {
			protocol = fwdProto
		}

		// Point 7: If the hostname is an IP address, redirect to tru.<ip>.nip.io format
		if ipPattern.MatchString(hostname) {
			var redirectURL string
			if port != "" {
				redirectURL = fmt.Sprintf("%s://tru.%s.nip.io:%s%s", protocol, hostname, port, r.URL.RequestURI())
			} else {
				redirectURL = fmt.Sprintf("%s://tru.%s.nip.io%s", protocol, hostname, r.URL.RequestURI())
			}
			log.Printf("Redirecting IP-based URL to: %s", redirectURL)
			http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
			return
		}

		// Point 8: Verify hostname starts with "tru."
		if !strings.HasPrefix(hostname, "tru.") {
			// Get local hostname for error message
			localHostname := getLocalHostname()
			var suggestedURL string
			if port != "" {
				suggestedURL = fmt.Sprintf("%s://tru.%s.nip.io:%s", protocol, localHostname, port)
			} else {
				suggestedURL = fmt.Sprintf("%s://tru.%s.nip.io", protocol, localHostname)
			}

			errorMsg := fmt.Sprintf("Invalid hostname. Please use %s", suggestedURL)
			log.Printf("Hostname verification failed: %s (expected tru.* prefix)", hostname)

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

		// Hostname is valid, proceed with the request
		next.ServeHTTP(w, r)
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
