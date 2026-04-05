package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
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

// getPgidFile returns the path to the pgid file inside WorkbenchDir
func getPgidFile() string {
	return filepath.Join(WorkbenchDir, "pgid")
}

// getCurrentFile returns the path to the current app name file inside WorkbenchDir
func getCurrentFile() string {
	return filepath.Join(WorkbenchDir, "current")
}

// writeCurrentApp writes the current app name to the current file and workspace config
func writeCurrentApp(name string) error {
	if err := os.WriteFile(getCurrentFile(), []byte(name), 0644); err != nil {
		return err
	}
	wsCfg, err := loadWorkspaceConfig()
	if err != nil {
		return err
	}
	wsCfg.Current = name
	return saveWorkspaceConfig(wsCfg)
}

// readCurrentApp reads the current app name from the current file
func readCurrentApp() (string, error) {
	data, err := os.ReadFile(getCurrentFile())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// removeCurrentFile removes the current app name file and clears it from workspace config
func removeCurrentFile() {
	os.Remove(getCurrentFile())
	wsCfg, err := loadWorkspaceConfig()
	if err == nil {
		wsCfg.Current = ""
		saveWorkspaceConfig(wsCfg)
	}
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

	// Clone to workbench if not already present
	// Use absolute path so child processes (Vite) resolve watches correctly
	workbenchPath, _ := filepath.Abs(filepath.Join(WorkbenchDir, app))
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		log.Printf("Cloning workspace/%s to workbench/%s...", app, app)
		if err := os.MkdirAll(WorkbenchDir, 0755); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to create workbench dir: %s", err)})
			return
		}
		cloneCmd := exec.Command("git", "clone", workspacePath, workbenchPath)
		if output, err := cloneCmd.CombinedOutput(); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to clone to workbench: %s", string(output))})
			return
		}

		// Generate .env and .env.production from config
		if err := generateAppEnvFiles(app); err != nil {
			log.Printf("Warning: failed to generate workbench .env: %s", err)
		}

		// Run npm install if package.json exists
		if _, err := os.Stat(filepath.Join(workbenchPath, "package.json")); err == nil {
			log.Printf("Running npm install in workbench/%s...", app)
			npmCmd := exec.Command("npm", "install")
			npmCmd.Dir = workbenchPath
			if output, err := npmCmd.CombinedOutput(); err != nil {
				log.Printf("Warning: npm install failed: %s, output: %s", err, string(output))
			}
		}

		log.Printf("Workbench for %s set up successfully", app)
	} else {
		log.Printf("Workbench for %s already exists, reusing", app)
		// Always regenerate .env from config to keep in sync
		if err := generateAppEnvFiles(app); err != nil {
			log.Printf("Warning: failed to regenerate workbench .env: %s", err)
		}

		// Run npm install if package.json exists (always, to keep deps in sync)
		if _, err := os.Stat(filepath.Join(workbenchPath, "package.json")); err == nil {
			log.Printf("Running npm install in workbench/%s...", app)
			npmCmd := exec.Command("npm", "install")
			npmCmd.Dir = workbenchPath
			if output, err := npmCmd.CombinedOutput(); err != nil {
				log.Printf("Warning: npm install failed: %s, output: %s", err, string(output))
			}
		}
	}

	// Set up skills if not already present
	skillsAdded := ensureSkills(app)

	// Ensure the OpenWhisk user exists and password is in sync
	log.Printf("Checking OpenWhisk user for %s...", app)
	cfg, err := loadTrustableConfig()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to load config: %s", err)})
		return
	}
	storedPassword := ""
	if cfg.Apps != nil && cfg.Apps[app] != nil {
		storedPassword = cfg.Apps[app].Password
	}

	kubegetCmd := exec.Command("ops", "util", "kubeget", "whiskuser/"+app, ".spec.password")
	kubegetOutput, kubegetErr := kubegetCmd.Output()
	if kubegetErr != nil {
		// User doesn't exist, recreate with stored password
		if storedPassword == "" {
			json.NewEncoder(w).Encode(map[string]string{"error": "No stored password for user " + app + ", cannot recreate"})
			return
		}
		log.Printf("User %s not found, creating with stored password...", app)
		email := app + "@n7s.co"
		addUserCmd := exec.Command("ops", "admin", "adduser", app, email, storedPassword, "--all")
		if output, err := addUserCmd.CombinedOutput(); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to create user: %s", string(output))})
			return
		}
		log.Printf("User %s created successfully", app)
	} else {
		// User exists, check if password matches
		remotePassword := strings.TrimSpace(string(kubegetOutput))
		if remotePassword != storedPassword && remotePassword != "" {
			log.Printf("Password mismatch for %s, updating stored password", app)
			wsCfg, err := loadWorkspaceConfig()
			if err == nil {
				if wsCfg.Apps == nil {
					wsCfg.Apps = make(map[string]*AppConfig)
				}
				if wsCfg.Apps[app] == nil {
					wsCfg.Apps[app] = &AppConfig{
						Development: make(map[string]string),
						Production:  make(map[string]string),
					}
				}
				wsCfg.Apps[app].Password = remotePassword
				if err := saveWorkspaceConfig(wsCfg); err != nil {
					log.Printf("Warning: failed to update stored password: %s", err)
				}
				// Regenerate .env with updated password
				if err := generateAppEnvFiles(app); err != nil {
					log.Printf("Warning: failed to regenerate .env after password update: %s", err)
				}
			}
		}
	}

	// Run ops ide login (always, even when reusing workbench)
	log.Printf("Running ops ide login for %s...", app)
	loginCmd := exec.Command("ops", "ide", "login")
	loginCmd.Dir = workbenchPath
	if output, err := loginCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide login for %s failed: %s, output: %s", app, err, string(output))
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide login failed: %s", string(output))})
		return
	}
	log.Printf("ops ide login for %s completed successfully", app)

	// Run ops ide clean
	log.Printf("Running ops ide clean for %s...", app)
	cleanCmd := exec.Command("ops", "ide", "clean")
	cleanCmd.Dir = workbenchPath
	if output, err := cleanCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide clean for %s failed: %s, output: %s", app, err, string(output))
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide clean failed: %s", string(output))})
		return
	}
	log.Printf("ops ide clean for %s completed successfully", app)

	// Run ops ide deploy
	log.Printf("Running ops ide deploy for %s...", app)
	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workbenchPath
	if output, err := deployCmd.CombinedOutput(); err != nil {
		log.Printf("ops ide deploy for %s failed: %s, output: %s", app, err, string(output))
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("ops ide deploy failed: %s", string(output))})
		return
	}
	log.Printf("ops ide deploy for %s completed successfully", app)

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

	// Always copy opencode.json and opencode.md to workbench (overwriting existing files)
	opencodeConfigSrc := filepath.Join(os.Getenv("HOME"), ".config", "opencode", "opencode.json")
	if _, err := os.Stat(opencodeConfigSrc); os.IsNotExist(err) {
		log.Println("opencode.json not found, generating it...")
		if cfg, err := loadTrustableConfig(); err != nil {
			log.Printf("Warning: failed to load trustable config for opencode generation: %s", err)
		} else if err := generateOpencodeConfig(cfg); err != nil {
			log.Printf("Warning: failed to generate opencode.json: %s", err)
		}
	}
	opencodeConfigDst := filepath.Join(workbenchPath, "opencode.json")
	if data, readErr := os.ReadFile(opencodeConfigSrc); readErr == nil {
		if writeErr := os.WriteFile(opencodeConfigDst, data, 0644); writeErr != nil {
			log.Printf("Warning: failed to copy opencode.json: %s", writeErr)
		}
	} else {
		log.Printf("Warning: opencode.json not found: %s", readErr)
	}

	// Start opencode
	log.Printf("Starting opencode for %s on port %d...", app, leftPort)
	opencodeCmd := exec.Command("opencode", "serve", "--port", strconv.Itoa(leftPort), "--hostname", "0.0.0.0", "--log-level", "DEBUG", "--print-logs")
	opencodeCmd.Dir = workbenchPath
	opencodeCmd.Stdout = os.Stdout
	opencodeCmd.Stderr = os.Stderr
	// Set process group so we can kill all child processes
	opencodeCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := opencodeCmd.Start(); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to start opencode: %s", err)})
		return
	}

	log.Printf("Started opencode in directory: %s", workbenchPath)

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

	// Write pgid and current app name to files
	if err := writePgid(pgid); err != nil {
		killPgid(pgid)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to write pgid file: %s", err)})
		return
	}
	if err := writeCurrentApp(app); err != nil {
		log.Printf("Warning: failed to write current app file: %s", err)
	}

	// Start ops ide devel in the same process group
	log.Printf("Starting ops ide devel for %s on port %d...", app, rightPort)
	develCmd := exec.Command("sh", "-c", fmt.Sprintf("cd %q && ops ide devel", workbenchPath))
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
	absPath, err := filepath.Abs(workbenchPath)
	if err != nil {
		killPgid(pgid)
		removePgidFile()
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to get absolute path: %s", err)})
		return
	}
	b64Path := base64.RawURLEncoding.EncodeToString([]byte(absPath))

	// Log the opencode session URLs
	domain := r.Host
	if colonIdx := strings.LastIndex(domain, ":"); colonIdx != -1 {
		if bracketIdx := strings.LastIndex(domain, "]"); bracketIdx == -1 || colonIdx > bracketIdx {
			domain = domain[:colonIdx]
		}
	}
	log.Printf("Services for %s started - opencode on port %d, opsdevel on port %d", app, leftPort, rightPort)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"left":         leftPort,
		"right":        rightPort,
		"b64dir":       b64Path,
		"encdir":       absPath,
		"skills_added": skillsAdded,
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
	removeCurrentFile()
	log.Printf("Process group %d terminated and pgid file removed", pgid)

	w.WriteHeader(http.StatusNoContent)
}

// findDevelPids finds PIDs in the process group that are ops ide devel or vite related
func findDevelPids(pgid int) ([]int, error) {
	// List all PIDs in the process group
	pgrepCmd := exec.Command("pgrep", "-g", strconv.Itoa(pgid))
	output, err := pgrepCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pgrep failed: %w", err)
	}

	var develPids []int
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			continue
		}

		// Check command line for this PID
		psCmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=")
		psOutput, err := psCmd.Output()
		if err != nil {
			continue
		}
		cmdLine := string(psOutput)

		// Match ops ide devel, vite, or node processes on port 5173
		if strings.Contains(cmdLine, "ops ide devel") ||
			strings.Contains(cmdLine, "vite") ||
			strings.Contains(cmdLine, "5173") {
			develPids = append(develPids, pid)
		}
	}
	return develPids, nil
}

// waitForHTTP waits for an HTTP server to respond to HEAD requests
func waitForHTTP(port int, timeout time.Duration) error {
	client := &http.Client{Timeout: 1 * time.Second}
	url := fmt.Sprintf("http://localhost:%d/", port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Head(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("http://localhost:%d not responding after %v", port, timeout)
}

// waitForPortFree waits for a port to stop accepting connections
func waitForPortFree(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isPortListening(port) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("port %d still listening after %v", port, timeout)
}

// handleRedeploy handles GET /api/redeploy?name=<app> - restarts ops ide devel, streaming progress via SSE
func handleRedeploy(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}

	req := struct{ Name string }{Name: name}

	workbenchPath, _ := filepath.Abs(filepath.Join(WorkbenchDir, req.Name))
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		http.Error(w, "Workbench not found", http.StatusNotFound)
		return
	}

	pgid, err := readPgid()
	if err != nil {
		http.Error(w, "No running session found", http.StatusBadRequest)
		return
	}

	// Stream progress as text/event-stream
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func(event, data string) {
		fmt.Fprintf(w, "event: %s\n", event)
		for _, line := range strings.Split(data, "\n") {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprintf(w, "\n")
		flusher.Flush()
	}

	// Step 1: Terminate ops ide devel
	send("status", "Terminating ops ide devel...")
	log.Printf("Redeploy: finding devel PIDs in process group %d...", pgid)
	develPids, _ := findDevelPids(pgid)

	if len(develPids) > 0 {
		for _, pid := range develPids {
			syscall.Kill(pid, syscall.SIGTERM)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			allDead := true
			for _, pid := range develPids {
				if err := syscall.Kill(pid, 0); err == nil {
					allDead = false
					break
				}
			}
			if allDead {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		for _, pid := range develPids {
			if err := syscall.Kill(pid, 0); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}

	// Step 2: Wait for port free
	send("status", "Waiting for port 5173 to be free...")
	if err := waitForPortFree(opsdevelPort, 10*time.Second); err != nil {
		send("error", fmt.Sprintf("Port %d did not free up: %s", opsdevelPort, err))
		return
	}

	// Step 3: Deploy actions
	send("status", "Deploying actions (ops ide deploy)...")
	log.Printf("Redeploy: running ops ide deploy for %s...", req.Name)
	deployCmd := exec.Command("ops", "ide", "deploy")
	deployCmd.Dir = workbenchPath
	if deployOutput, err := deployCmd.CombinedOutput(); err != nil {
		send("error", fmt.Sprintf("ops ide deploy failed: %s\n%s", err, string(deployOutput)))
		return
	}
	log.Printf("Redeploy: ops ide deploy completed for %s", req.Name)

	// Step 4: Get action list
	send("status", "Getting action list...")
	actionCmd := exec.Command("ops", "action", "list")
	actionCmd.Dir = workbenchPath
	actionOutput, err := actionCmd.CombinedOutput()
	actionList := string(actionOutput)
	if err != nil {
		actionList = fmt.Sprintf("(ops action list failed: %s)\n%s", err, actionList)
	}
	log.Printf("Redeploy: action list:\n%s", actionList)

	// Step 5: Start ops ide devel --fast
	send("status", "Starting dev server (ops ide devel --fast)...")
	develCmd := exec.Command("sh", "-c", fmt.Sprintf("cd %q && ops ide devel --fast", workbenchPath))
	develCmd.Stdout = os.Stdout
	develCmd.Stderr = os.Stderr
	develCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}

	if err := develCmd.Start(); err != nil {
		send("error", fmt.Sprintf("Failed to start ops ide devel: %s", err))
		return
	}

	develExited := make(chan error, 1)
	go func() {
		develExited <- develCmd.Wait()
	}()

	select {
	case err := <-develExited:
		errMsg := "ops ide devel exited unexpectedly"
		if err != nil {
			errMsg = fmt.Sprintf("ops ide devel exited with error: %s", err)
		}
		send("error", errMsg)
		return
	case <-time.After(500 * time.Millisecond):
	}

	// Step 6: Wait for dev server to respond
	send("status", "Waiting for dev server to be ready...")
	log.Printf("Redeploy: waiting for HTTP response on port %d...", opsdevelPort)
	if err := waitForHTTP(opsdevelPort, 30*time.Second); err != nil {
		send("error", fmt.Sprintf("Dev server not responding: %s", err))
		return
	}
	log.Printf("Redeploy: dev server on port %d is responding", opsdevelPort)

	// Done - send action list as the data
	send("done", actionList)
	log.Printf("Redeploy: completed successfully for %s", req.Name)
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
