package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
)

// ipPattern matches IPv4 addresses
var ipPattern = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)

// reverse proxy instances for opencode and vite
var opencodeProxy = httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "localhost:4096"})
var viteProxy = httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "localhost:5173"})

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
