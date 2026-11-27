// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFilesWorkbench builds a workbench under a temp WorkbenchDir and returns the
// app name to address it with. The name must satisfy namePattern.
func newFilesWorkbench(t *testing.T) (name string, workbench string) {
	t.Helper()

	dir := t.TempDir()
	prev := WorkbenchDir
	WorkbenchDir = dir
	t.Cleanup(func() { WorkbenchDir = prev })

	name = "viewapp"
	workbench = filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(workbench, "src"), 0755); err != nil {
		t.Fatalf("mkdir workbench: %s", err)
	}
	return name, workbench
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %s", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %s", path, err)
	}
}

func getFiles(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	handleFiles(rec, req)
	return rec
}

func TestFilesListSkipsNoiseAndSortsDirsFirst(t *testing.T) {
	name, workbench := newFilesWorkbench(t)

	writeFile(t, filepath.Join(workbench, "src", "App.tsx"), "export default 1\n")
	writeFile(t, filepath.Join(workbench, "package.json"), "{}\n")
	writeFile(t, filepath.Join(workbench, ".env"), "KEY=value\n")
	writeFile(t, filepath.Join(workbench, ".agents", "skills", "note.md"), "skill\n")
	// Noise that must not appear in the listing.
	writeFile(t, filepath.Join(workbench, ".git", "config"), "[core]\n")
	writeFile(t, filepath.Join(workbench, "node_modules", "left-pad", "index.js"), "module.exports=1\n")
	writeFile(t, filepath.Join(workbench, "dist", "bundle.js"), "bundled\n")
	writeFile(t, filepath.Join(workbench, ".DS_Store"), "junk\n")

	rec := getFiles(t, "/api/files/"+name)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Files     []FileEntry `json:"files"`
		Truncated bool        `json:"truncated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %s", err)
	}

	got := map[string]FileEntry{}
	for _, f := range resp.Files {
		got[f.Path] = f
	}

	for _, want := range []string{"src", "src/App.tsx", "package.json", ".env", ".agents"} {
		if _, ok := got[want]; !ok {
			t.Errorf("listing missing %q; got %v", want, got)
		}
	}
	for _, unwanted := range []string{".git", ".git/config", "node_modules", "node_modules/left-pad/index.js", "dist", "dist/bundle.js", ".DS_Store"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("listing should not contain %q", unwanted)
		}
	}

	if entry := got["src/App.tsx"]; entry.Dir || entry.Size == 0 {
		t.Errorf("src/App.tsx = %+v, want a file with non-zero size", entry)
	}
	if entry := got["src"]; !entry.Dir {
		t.Errorf("src = %+v, want a directory", entry)
	}

	// Directories must sort ahead of files.
	seenFile := false
	for _, f := range resp.Files {
		if !f.Dir {
			seenFile = true
			continue
		}
		if seenFile {
			t.Fatalf("directory %q sorted after a file; order = %v", f.Path, resp.Files)
		}
	}

	if resp.Truncated {
		t.Errorf("truncated = true, want false for a small tree")
	}
}

func TestFilesReadReturnsContent(t *testing.T) {
	name, workbench := newFilesWorkbench(t)
	writeFile(t, filepath.Join(workbench, "src", "App.tsx"), "export default 1\n")

	rec := getFiles(t, "/api/files/"+name+"?path=src/App.tsx")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Binary  bool   `json:"binary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %s", err)
	}
	if resp.Content != "export default 1\n" {
		t.Errorf("content = %q, want the file contents", resp.Content)
	}
	if resp.Path != "src/App.tsx" {
		t.Errorf("path = %q, want src/App.tsx", resp.Path)
	}
	if resp.Binary {
		t.Errorf("binary = true, want false for a text file")
	}
}

func TestFilesRejectsTraversal(t *testing.T) {
	name, workbench := newFilesWorkbench(t)
	// A secret living next to the workbench, i.e. what traversal would target.
	writeFile(t, filepath.Join(filepath.Dir(workbench), "secret.txt"), "top secret\n")

	for _, rel := range []string{
		"../secret.txt",
		"src/../../secret.txt",
		"..%2Fsecret.txt",
		"/etc/passwd",
		"src/../..",
	} {
		rec := getFiles(t, "/api/files/"+name+"?path="+rel)
		if rec.Code == http.StatusOK {
			t.Errorf("path=%q returned 200, want rejection (body %q)", rel, rec.Body.String())
			continue
		}
		if strings.Contains(rec.Body.String(), "top secret") {
			t.Errorf("path=%q leaked the out-of-workbench file", rel)
		}
	}
}

func TestFilesRejectsSymlinkEscape(t *testing.T) {
	name, workbench := newFilesWorkbench(t)

	outside := filepath.Join(filepath.Dir(workbench), "outside-secret.txt")
	writeFile(t, outside, "ssh private key\n")

	// A symlink the generated app could plausibly contain.
	link := filepath.Join(workbench, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %s", err)
	}

	rec := getFiles(t, "/api/files/"+name+"?path=escape.txt")
	if rec.Code == http.StatusOK {
		t.Fatalf("symlink escape returned 200, want rejection (body %q)", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "ssh private key") {
		t.Fatalf("symlink escape leaked the target file")
	}
}

func TestFilesAllowsSymlinkInsideWorkbench(t *testing.T) {
	name, workbench := newFilesWorkbench(t)

	writeFile(t, filepath.Join(workbench, "src", "real.txt"), "inside content\n")
	link := filepath.Join(workbench, "alias.txt")
	if err := os.Symlink(filepath.Join(workbench, "src", "real.txt"), link); err != nil {
		t.Skipf("symlinks unavailable: %s", err)
	}

	rec := getFiles(t, "/api/files/"+name+"?path=alias.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a symlink that stays inside (body %q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "inside content") {
		t.Errorf("body = %q, want the linked file's content", rec.Body.String())
	}
}

func TestFilesRejectsOversizeFile(t *testing.T) {
	name, workbench := newFilesWorkbench(t)
	writeFile(t, filepath.Join(workbench, "big.txt"), strings.Repeat("a", maxViewableFileSize+1))

	rec := getFiles(t, "/api/files/"+name+"?path=big.txt")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %q)", rec.Code, rec.Body.String())
	}
}

func TestFilesReportsBinaryWithoutContent(t *testing.T) {
	name, workbench := newFilesWorkbench(t)
	if err := os.WriteFile(filepath.Join(workbench, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a, 0x0a}, 0644); err != nil {
		t.Fatalf("write binary: %s", err)
	}

	rec := getFiles(t, "/api/files/"+name+"?path=logo.png")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Content string `json:"content"`
		Binary  bool   `json:"binary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %s", err)
	}
	if !resp.Binary {
		t.Errorf("binary = false, want true for PNG bytes")
	}
	if resp.Content != "" {
		t.Errorf("content = %q, want empty for a binary file", resp.Content)
	}
}

func TestFilesRejectsBadNameAndMissingWorkbench(t *testing.T) {
	_, _ = newFilesWorkbench(t)

	if rec := getFiles(t, "/api/files/bad"); rec.Code != http.StatusBadRequest {
		t.Errorf("short name status = %d, want 400", rec.Code)
	}
	if rec := getFiles(t, "/api/files/"); rec.Code != http.StatusBadRequest {
		t.Errorf("empty name status = %d, want 400", rec.Code)
	}
	if rec := getFiles(t, "/api/files/../../etc"); rec.Code != http.StatusBadRequest {
		t.Errorf("traversal in name status = %d, want 400", rec.Code)
	}
	if rec := getFiles(t, "/api/files/missingapp"); rec.Code != http.StatusNotFound {
		t.Errorf("missing workbench status = %d, want 404", rec.Code)
	}
}

func TestFilesRejectsNonGetMethods(t *testing.T) {
	name, workbench := newFilesWorkbench(t)
	writeFile(t, filepath.Join(workbench, "src", "App.tsx"), "export default 1\n")

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/api/files/"+name+"?path=src/App.tsx", strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		handleFiles(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, rec.Code)
		}
	}

	// The viewer is read-only: a rejected POST must not have altered the file.
	content, err := os.ReadFile(filepath.Join(workbench, "src", "App.tsx"))
	if err != nil {
		t.Fatalf("read back: %s", err)
	}
	if string(content) != "export default 1\n" {
		t.Errorf("file content changed to %q", content)
	}
}

func TestFilesMissingFileReturns404(t *testing.T) {
	name, _ := newFilesWorkbench(t)

	rec := getFiles(t, "/api/files/"+name+"?path=src/nope.tsx")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %q)", rec.Code, rec.Body.String())
	}
}

func TestFilesRejectsDirectoryRead(t *testing.T) {
	name, _ := newFilesWorkbench(t)

	rec := getFiles(t, "/api/files/"+name+"?path=src")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a directory (body %q)", rec.Code, rec.Body.String())
	}
}
