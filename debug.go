package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// handleActivationPoll streams `ops activation poll` for the selected app.
// It runs in the app workbench with the generated development .env appended to
// the process environment, matching the user context used by the editor.
func handleActivationPoll(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	var writeMu sync.Mutex
	send := func(event, data string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		fmt.Fprintf(w, "event: %s\n", event)
		cleaned := strings.ReplaceAll(data, "\r", "")
		for _, line := range strings.Split(cleaned, "\n") {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprint(w, "\n")
		flusher.Flush()
	}

	workbenchPath, _ := filepath.Abs(filepath.Join(WorkbenchDir, name))
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		send("error", "Workbench not found")
		return
	}
	if err := generateAppEnvFiles(name); err != nil {
		send("error", "Failed to generate app env: "+err.Error())
		return
	}

	send("status", fmt.Sprintf("Starting ops activation poll for %s...", name))

	cmd := exec.CommandContext(r.Context(), "ops", "activation", "poll")
	cmd.Dir = workbenchPath
	cmd.Env = os.Environ()
	for key, value := range parseEnvFile(filepath.Join(workbenchPath, ".env")) {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		send("error", "Failed to open stdout: "+err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		send("error", "Failed to open stderr: "+err.Error())
		return
	}

	if err := cmd.Start(); err != nil {
		send("error", "Failed to start ops activation poll: "+err.Error())
		return
	}
	log.Printf("Activation debug: started ops activation poll for %s in %s", name, workbenchPath)

	go terminateCommandGroupOnCancel(r.Context().Done(), cmd)

	var scanWG sync.WaitGroup
	scan := func(reader io.Reader, prefix string) {
		defer scanWG.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if prefix != "" {
				line = prefix + line
			}
			send("line", line)
		}
		if err := scanner.Err(); err != nil && r.Context().Err() == nil {
			send("line", prefix+"stream read error: "+err.Error())
		}
	}
	scanWG.Add(2)
	go scan(stdout, "")
	go scan(stderr, "[stderr] ")

	waitErr := cmd.Wait()
	scanWG.Wait()

	if r.Context().Err() != nil {
		log.Printf("Activation debug: request closed for %s", name)
		return
	}
	if waitErr != nil {
		send("error", "ops activation poll stopped: "+waitErr.Error())
		log.Printf("Activation debug: ops activation poll failed for %s: %s", name, waitErr)
		return
	}

	send("done", "ops activation poll stopped")
	log.Printf("Activation debug: ops activation poll completed for %s", name)
}

func terminateCommandGroupOnCancel(done <-chan struct{}, cmd *exec.Cmd) {
	<-done
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pgid, err := syscall.Getpgid(pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.Sleep(500 * time.Millisecond)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
}
