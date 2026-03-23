package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	opencodePort  = 4096
	opsdevelPort  = 5173
)

// getPgidFile returns the path to the pgid file inside WorkspaceDir
func getPgidFile() string {
	return filepath.Join(WorkspaceDir, "pgid")
}


// isPortFree checks if a port is available for use
func isPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// isPortListening checks if a port is accepting connections
func isPortListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// waitForPort waits for a port to start accepting connections
func waitForPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isPortListening(port) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("port %d not listening after %v", port, timeout)
}

// readPgid reads the process group ID from the pgid file
func readPgid() (int, error) {
	data, err := os.ReadFile(getPgidFile())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// writePgid writes the process group ID to the pgid file
func writePgid(pgid int) error {
	return os.WriteFile(getPgidFile(), []byte(strconv.Itoa(pgid)), 0644)
}

// removePgidFile removes the pgid file
func removePgidFile() {
	os.Remove(getPgidFile())
}

// killPgid forcefully terminates a process group by its pgid
func killPgid(pgid int) error {
	// Kill the entire process group (negative pgid)
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	if err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

// terminateLeftoverProcesses checks for and kills any leftover process group
func terminateLeftoverProcesses() {
	pgid, err := readPgid()
	if err != nil {
		// No pgid file or can't read it - nothing to do
		return
	}

	log.Printf("Found leftover pgid file with pgid %d, terminating...", pgid)

	if err := killPgid(pgid); err != nil {
		log.Printf("Warning: failed to kill process group %d: %s", pgid, err)
	}

	removePgidFile()

	// Wait a bit for ports to be freed
	time.Sleep(500 * time.Millisecond)
}

// waitForProcessStart waits for a process to either exit (error) or stay running for the specified duration
// Returns nil if process stays running, error if it exits prematurely
func waitForProcessStart(cmd *exec.Cmd, duration time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		// Process exited within the duration - this is an error
		if err != nil {
			return fmt.Errorf("process exited with error: %w", err)
		}
		return fmt.Errorf("process exited unexpectedly")
	case <-time.After(duration):
		// Process is still running after the duration - success
		return nil
	}
}

// handleLaunchGet handles GET /api/launch/<app>
func handleLaunchGet(w http.ResponseWriter, r *http.Request, app string) {
	w.Header().Set("Content-Type", "application/json")

	// Validate app name format
	if !namePattern.MatchString(app) {
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid app name format"})
		return
	}

	// Check if workspace folder exists
	workspacePath := filepath.Join(WorkspaceDir, "workspace", app)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("App folder not found: %s/workspace/%s", WorkspaceDir, app)})
		return
	}

	// Terminate leftover processes
	terminateLeftoverProcesses()

	// Run ops ide login
	log.Printf("Running ops ide login for %s...", app)
	loginCmd := exec.Command("ops", "ide", "login")
	loginCmd.Dir = workspacePath
	if output, err := loginCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide login for %s failed: %s, output: %s", app, err, string(output))
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide login failed: %s", string(output))})
		return
	}
	log.Printf("ops ide login for %s completed successfully", app)

	// Detect ports
	leftPort := opencodePort
	rightPort := opsdevelPort

	// Check if ports are free
	if !isPortFree(leftPort) {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Port %d (opencode) is not available", leftPort)})
		return
	}
	if !isPortFree(rightPort) {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Port %d (opsdevel) is not available", rightPort)})
		return
	}

	// Start opencode
	log.Printf("Starting opencode for %s on port %d...", app, leftPort)
	opencodeCmd := exec.Command("opencode", "serve", "--port", strconv.Itoa(leftPort), "--hostname", "0.0.0.0", "--log-level", "DEBUG", "--print-logs")
	opencodeCmd.Dir = workspacePath
	opencodeCmd.Stdout = os.Stdout
	opencodeCmd.Stderr = os.Stderr
	// Set process group so we can kill all child processes
	opencodeCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := opencodeCmd.Start(); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to start opencode: %s", err)})
		return
	}

	log.Printf("Started opencode in directory: %s", workspacePath)

	// Get the process group ID
	pgid, err := syscall.Getpgid(opencodeCmd.Process.Pid)
	if err != nil {
		opencodeCmd.Process.Kill()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to get process group: %s", err)})
		return
	}

	// Check opencode doesn't terminate within 0.5 seconds
	opencodeExited := make(chan error, 1)
	go func() {
		opencodeExited <- opencodeCmd.Wait()
	}()

	select {
	case err := <-opencodeExited:
		// Process exited within 0.5 seconds - this is an error
		errMsg := "opencode exited unexpectedly"
		if err != nil {
			errMsg = fmt.Sprintf("opencode exited with error: %s", err)
		}
		json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	case <-time.After(500 * time.Millisecond):
		// Process is still running - continue
	}

	// Write pgid to file
	if err := writePgid(pgid); err != nil {
		killPgid(pgid)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to write pgid file: %s", err)})
		return
	}

	// Start ops ide devel in the same process group
	log.Printf("Starting ops ide devel for %s on port %d...", app, rightPort)
	develCmd := exec.Command("ops", "ide", "devel")
	develCmd.Dir = workspacePath
	develCmd.Stdout = os.Stdout
	develCmd.Stderr = os.Stderr
	// Join the same process group as opencode
	develCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}

	if err := develCmd.Start(); err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to start ops ide devel: %s", err)})
		return
	}

	// Check ops ide devel doesn't terminate within 0.5 seconds
	develExited := make(chan error, 1)
	go func() {
		develExited <- develCmd.Wait()
	}()

	select {
	case err := <-develExited:
		// Process exited within 0.5 seconds - this is an error
		errMsg := "ops ide devel exited unexpectedly"
		if err != nil {
			errMsg = fmt.Sprintf("ops ide devel exited with error: %s", err)
		}
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	case <-time.After(500 * time.Millisecond):
		// Process is still running - continue
	}

	// Wait for both ports to be listening
	log.Printf("Waiting for ports %d and %d to be listening...", leftPort, rightPort)
	if err := waitForPort(leftPort, 30*time.Second); err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("opencode failed to start listening: %s", err)})
		return
	}
	if err := waitForPort(rightPort, 30*time.Second); err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide devel failed to start listening: %s", err)})
		return
	}

	// Calculate URL-encoded absolute path of the app folder
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to get absolute path: %s", err)})
		return
	}
	b64Path := base64.RawURLEncoding.EncodeToString([]byte(absPath))
	encPath := url.PathEscape(absPath)

	// Log the opencode session URLs
	domain := r.Host
	if colonIdx := strings.LastIndex(domain, ":"); colonIdx != -1 {
		if bracketIdx := strings.LastIndex(domain, "]"); bracketIdx == -1 || colonIdx > bracketIdx {
			domain = domain[:colonIdx]
		}
	}

	// POST to opencode session endpoint to initialize the session
	sessionURL := fmt.Sprintf("http://%s:%d/session/", domain, leftPort)
	sessionReq, err := http.NewRequest("POST", sessionURL, nil)
	if err != nil {
		log.Printf("Warning: failed to create session request: %s", err)
	} else {
		sessionReq.Header.Set("X-Opencode-Directory", absPath)
		resp, err := http.DefaultClient.Do(sessionReq)
		if err != nil {
			log.Printf("Warning: POST %s failed: %s", sessionURL, err)
		} else {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("POST %s -> %d: %s", sessionURL, resp.StatusCode, string(body))
		}
	}

	log.Printf("Services for %s started - opencode on port %d, opsdevel on port %d", app, leftPort, rightPort)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"left":   leftPort,
		"right":  rightPort,
		"b64dir": b64Path,
		"encdir": encPath,
	})
}

// handleLaunchDelete handles DELETE /api/launch
func handleLaunchDelete(w http.ResponseWriter, r *http.Request) {
	pgid, err := readPgid()
	if err != nil {
		// No pgid file - nothing running
		w.WriteHeader(http.StatusNoContent)
		return
	}

	log.Printf("Terminating process group %d...", pgid)
	if err := killPgid(pgid); err != nil {
		log.Printf("Warning: failed to kill process group %d: %s", pgid, err)
	}

	removePgidFile()
	log.Printf("Process group %d terminated and pgid file removed", pgid)

	w.WriteHeader(http.StatusNoContent)
}

// handleLaunch routes launch API requests
func handleLaunch(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	// Extract app name from URL path /api/launch/<app>
	app := strings.TrimPrefix(r.URL.Path, "/api/launch/")
	app = strings.TrimPrefix(app, "/api/launch")

	switch r.Method {
	case http.MethodGet:
		if app == "" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"error": "App name is required"})
			return
		}
		handleLaunchGet(w, r, app)
	case http.MethodDelete:
		handleLaunchDelete(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
