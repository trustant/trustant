package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// maxFileListEntries caps the walk so a runaway tree cannot wedge the UI.
const maxFileListEntries = 5000

// maxViewableFileSize is the largest file the viewer will return. The viewer is
// for source files, not assets.
const maxViewableFileSize = 1 << 20 // 1 MB

// binarySniffLen is how much of a file is inspected for NUL bytes before
// deciding it is binary.
const binarySniffLen = 8192

// skippedDirs are never descended into: build output, dependencies, and the
// git database itself would swamp the listing with irrelevant entries.
var skippedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".venv":        true,
	"__pycache__":  true,
	".next":        true,
}

// allowedDotEntries are the dot-prefixed entries worth showing; every other
// dotfile is hidden to keep the tree readable.
var allowedDotEntries = map[string]bool{
	".env":            true,
	".env.production": true,
	".agents":         true,
	".claude":         true,
}

// errInvalidPath is returned for any path that does not resolve to a location
// inside the requested workbench. The message is deliberately uniform so the
// response cannot be used to probe what exists outside the workbench.
var errInvalidPath = errors.New("Invalid path")

// FileEntry is one node of the workbench tree.
type FileEntry struct {
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

// handleFiles handles GET /api/files/<name> and GET /api/files/<name>?path=<relpath>.
//
// Without a path parameter it returns the workbench file tree; with one it
// returns that file's content. The endpoint is read-only by design: there is no
// write path, which is what makes it safe to expose over the whole workbench.
func handleFiles(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid name", http.StatusBadRequest)
		return
	}

	workbenchPath := filepath.Join(WorkbenchDir, name)
	info, err := os.Stat(workbenchPath)
	if err != nil || !info.IsDir() {
		http.Error(w, "Workbench not found", http.StatusNotFound)
		return
	}

	if rel := r.URL.Query().Get("path"); rel != "" {
		serveFileContent(w, workbenchPath, rel)
		return
	}

	serveFileList(w, workbenchPath)
}

// serveFileList walks the workbench and returns a flat, sorted entry list.
func serveFileList(w http.ResponseWriter, workbenchPath string) {
	entries := []FileEntry{}
	truncated := false

	err := filepath.WalkDir(workbenchPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree should not fail the whole listing.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if path == workbenchPath {
			return nil
		}

		base := d.Name()
		if d.IsDir() && skippedDirs[base] {
			return fs.SkipDir
		}
		if strings.HasPrefix(base, ".") && !allowedDotEntries[base] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if len(entries) >= maxFileListEntries {
			truncated = true
			return fs.SkipAll
		}

		rel, relErr := filepath.Rel(workbenchPath, path)
		if relErr != nil {
			return nil
		}

		entry := FileEntry{Path: filepath.ToSlash(rel), Dir: d.IsDir()}
		if !d.IsDir() {
			if fi, statErr := d.Info(); statErr == nil {
				entry.Size = fi.Size()
			}
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Directories first, then lexicographic, so the tree builds predictably.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return entries[i].Path < entries[j].Path
	})

	writeJSON(w, map[string]interface{}{
		"files":     entries,
		"truncated": truncated,
	})
}

// serveFileContent returns a single file's content after verifying the resolved
// target really is inside the workbench.
func serveFileContent(w http.ResponseWriter, workbenchPath, rel string) {
	target, err := resolveWorkbenchPath(workbenchPath, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.Error(w, "Not a file", http.StatusBadRequest)
		return
	}
	if info.Size() > maxViewableFileSize {
		http.Error(w, "File too large to view (limit 1 MB)", http.StatusRequestEntityTooLarge)
		return
	}

	content, err := os.ReadFile(target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cleanRel := filepath.ToSlash(filepath.Clean(rel))
	if isBinary(content) {
		writeJSON(w, map[string]interface{}{
			"path":    cleanRel,
			"content": "",
			"size":    info.Size(),
			"binary":  true,
		})
		return
	}

	writeJSON(w, map[string]interface{}{
		"path":    cleanRel,
		"content": string(content),
		"size":    info.Size(),
		"binary":  false,
	})
}

// resolveWorkbenchPath validates that rel stays inside workbenchPath.
//
// Both lexical traversal and symlink escape are rejected: a generated symlink
// pointing at ~/.ssh or the host .env must not be readable through the viewer,
// so the resolved target is compared against the resolved root.
func resolveWorkbenchPath(workbenchPath, rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", errInvalidPath
	}
	// Windows-style volume names would defeat the containment check below.
	if filepath.VolumeName(rel) != "" {
		return "", errInvalidPath
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", errInvalidPath
		}
	}

	target := filepath.Join(workbenchPath, filepath.FromSlash(rel))

	root, err := filepath.EvalSymlinks(workbenchPath)
	if err != nil {
		return "", errInvalidPath
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			// Report a missing file as such rather than as a traversal attempt,
			// but only once we know the unresolved path is lexically contained.
			if withinRoot(root, target) {
				return target, nil
			}
		}
		return "", errInvalidPath
	}
	if !withinRoot(root, resolved) {
		return "", errInvalidPath
	}
	return resolved, nil
}

// withinRoot reports whether path is root itself or lives beneath it.
func withinRoot(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

// isBinary reports whether content looks like a binary file, so the viewer can
// refuse it instead of dumping bytes into the browser.
func isBinary(content []byte) bool {
	head := content
	if len(head) > binarySniffLen {
		head = head[:binarySniffLen]
	}
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	// Truncating a multi-byte rune at the sniff boundary would look invalid, so
	// only full content is checked for UTF-8 validity.
	if len(content) <= binarySniffLen && !utf8.Valid(content) {
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload)
}
