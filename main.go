package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed web
var embeddedWeb embed.FS

//go:embed _build.txt
var buildTxt string

//go:embed opencode.md
var opencodeMd string

//go:embed openserverless-contract.md
var openserverlessContractMd string

//go:embed app-agents.md
var appAgentsMd string

// The checker is embedded rather than copied from the host. WHY: every app
// must receive the exact source-contract checker matching this Trustable
// binary, including its managed-live behavior that does not poll deploy ZIPs.
//go:embed check_openserverless_actions.sh
var openserverlessCheckerSh string

//go:embed check_trustable_frontend.sh
var trustableFrontendCheckerSh string

//go:embed check_trustable_app.sh
var trustableAppCheckerSh string

//go:embed milvus_cli.tmpl
var milvusCliTemplate string

// noStoreHTML prevents an upgraded Trustable UI from restoring stale inline
// configuration logic from browser history. This is especially important for
// hard schema cutovers such as OpenCode -> Pi, where old JavaScript can create
// a redirect loop even though the server-side configuration is already valid.
func noStoreHTML(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	auth, err := newAuthManagerFromEnv()
	if err != nil {
		log.Fatalf("Authentication configuration failed: %s", err)
	}

	// Run preflight checks
	if err := runPreflight(); err != nil {
		log.Fatalf("Preflight checks failed: %s", err)
	}

	// Ensure workspace and workbench directories exist
	wsPath := filepath.Join(WorkspaceDir, "workspace")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		log.Printf("Warning: failed to create workspace directory: %s", err)
	}
	if err := os.MkdirAll(WorkbenchDir, 0755); err != nil {
		log.Printf("Warning: failed to create workbench directory: %s", err)
	}

	// Terminate any leftover processes from previous run (kept for backward compatibility)
	terminateLeftoverProcesses()

	// Recreate missing ephemeral checkouts from durable bare repos. TruACP/Pi is
	// launched with those paths as cwd, so restoring them before serving requests
	// keeps persisted applications editable across pod rebuilds and VM restarts.
	restoreMissingWorkbenchCheckouts()

	// Parse version info
	parseVersion(buildTxt)

	// API routes
	http.HandleFunc("/api/version", handleVersion)
	http.HandleFunc("/api/status", handleStatus)
	http.HandleFunc("/api/repo", handleRepo)
	http.HandleFunc("/api/starters", handleStarters)
	http.HandleFunc("/api/upload", handleUpload)
	http.HandleFunc("/api/launch/", handleLaunch)
	http.HandleFunc("/api/launch", handleLaunch)
	http.HandleFunc("/api/git", handleGit)
	http.HandleFunc("/api/git/status/", handleGitStatus)
	http.HandleFunc("/api/git/save", handleGitSave)
	http.HandleFunc("/api/git/pull", handleGitPull)
	http.HandleFunc("/api/git/deploy", handleGitDeploy)
	http.HandleFunc("/api/github/status", handleGitHubStatus)
	http.HandleFunc("/api/github/login", handleGitHubLogin)
	http.HandleFunc("/api/github/login/cancel", handleGitHubLoginCancel)
	http.HandleFunc("/api/github/logout", handleGitHubLogout)
	http.HandleFunc("/api/github/repos", handleGitHubRepos)
	http.HandleFunc("/api/publish/force-push", handlePublish)
	http.HandleFunc("/api/sshkey", handleSSHKey)
	http.HandleFunc("/api/configure", handleConfigure)
	http.HandleFunc("/api/testmodel", handleTestModel)
	http.HandleFunc("/api/ollama-connect", handleOllamaConnect)
	http.HandleFunc("/api/discover-models", handleDiscoverModels)
	http.HandleFunc("/api/configuration", handleConfiguration)
	http.HandleFunc("/api/predefined-env", handlePredefinedEnv)
	http.HandleFunc("/api/appconfig/", handleAppConfig)
	http.HandleFunc("/api/publish/push", handlePublish)
	http.HandleFunc("/api/publish/remote", handlePublish)
	http.HandleFunc("/api/skills/", handleSkills)
	http.HandleFunc("/api/memory/", handleMemory)
	http.HandleFunc("/api/terminal/", handleTerminal)
	http.HandleFunc("/api/files/", handleFiles)
	http.HandleFunc("/api/redeploy", handleRedeploy)
	http.HandleFunc("/api/undeploy", handleUndeploy)
	http.HandleFunc("/api/clean", handleClean)
	http.HandleFunc("/api/activations/poll", handleActivationPoll)
	http.HandleFunc("/api/license", handleLicense)
	http.HandleFunc("/api/credits", handleCredits)
	http.HandleFunc("/api/topup", handleTopUp)
	auth.registerRoutes(http.DefaultServeMux)

	// Static file serving
	if _, err := os.Stat("web"); err == nil {
		// Serve from disk (development mode)
		log.Println("Serving from disk: ./web")
		http.Handle("/", noStoreHTML(http.FileServer(http.Dir("web"))))
	} else {
		// Serve from embedded filesystem
		log.Println("Serving from embedded filesystem")
		webFS, err := fs.Sub(embeddedWeb, "web")
		if err != nil {
			log.Fatal(err)
		}
		http.Handle("/", noStoreHTML(http.FileServer(http.FS(webFS))))
	}

	log.Println("Starting server on :8910")
	// Wrap with hostname verification middleware (implements spec points 7 & 8)
	handler := hostnameMiddleware(auth.middleware(http.DefaultServeMux))
	if err := http.ListenAndServe(":8910", handler); err != nil {
		log.Fatal(err)
	}
}
