package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
)

//go:embed web
var embeddedWeb embed.FS

func main() {
	// Run preflight checks
	if err := runPreflight(); err != nil {
		log.Fatalf("Preflight checks failed: %s", err)
	}

	// Ensure workspace directory exists
	if err := os.MkdirAll("workspace", 0755); err != nil {
		log.Printf("Warning: failed to create workspace directory: %s", err)
	}

	// Terminate any leftover processes from previous run (kept for backward compatibility)
	terminateLeftoverProcesses()

	// API routes
	http.HandleFunc("/api/repo", handleRepo)
	http.HandleFunc("/api/launch/", handleLaunch)
	http.HandleFunc("/api/launch", handleLaunch)
	http.HandleFunc("/api/git", handleGit)
	http.HandleFunc("/api/publish", handlePublish)

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
	if err := http.ListenAndServe(":8910", nil); err != nil {
		log.Fatal(err)
	}
}
