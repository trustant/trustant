package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed web
var embeddedWeb embed.FS

//go:embed version.txt
var versionTxt string

func main() {
	// Run preflight checks
	if err := runPreflight(); err != nil {
		log.Fatalf("Preflight checks failed: %s", err)
	}

	// Ensure workspace directory exists
	wsPath := filepath.Join(WorkspaceDir, "workspace")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		log.Printf("Warning: failed to create workspace directory: %s", err)
	}

	// Terminate any leftover processes from previous run (kept for backward compatibility)
	terminateLeftoverProcesses()

	// Parse version info
	parseVersion(versionTxt)

	// API routes
	http.HandleFunc("/api/version", handleVersion)
	http.HandleFunc("/api/repo", handleRepo)
	http.HandleFunc("/api/upload", handleUpload)
	http.HandleFunc("/api/launch/", handleLaunch)
	http.HandleFunc("/api/launch", handleLaunch)
	http.HandleFunc("/api/git", handleGit)
	http.HandleFunc("/api/publish", handlePublish)
	http.HandleFunc("/api/sshkey", handleSSHKey)
	http.HandleFunc("/api/configure", handleConfigure)
	http.HandleFunc("/api/configuration", handleConfiguration)

	// Static file serving
	if _, err := os.Stat("web"); err == nil {
		// Serve from disk (development mode)
		log.Println("Serving from disk: ./web")
		http.Handle("/", http.FileServer(http.Dir("web")))
	} else {
		// Serve from embedded filesystem
		log.Println("Serving from embedded filesystem")
		webFS, err := fs.Sub(embeddedWeb, "web")
		if err != nil {
			log.Fatal(err)
		}
		http.Handle("/", http.FileServer(http.FS(webFS)))
	}

	log.Println("Starting server on :8910")
	// Wrap with hostname verification middleware (implements spec points 7 & 8)
	handler := hostnameMiddleware(http.DefaultServeMux)
	if err := http.ListenAndServe(":8910", handler); err != nil {
		log.Fatal(err)
	}
}
