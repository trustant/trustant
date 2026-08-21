package main

import (
	"os"
	"strings"
	"testing"
)

// The image's .bashrc snippet is what puts a new terminal into the launched
// app's checkout. It is shell embedded in a Dockerfile, so nothing compiles it
// and nothing runs it until a full `./build.sh --build` reaches a user — a
// `hotfix.sh` cannot even ship it, because that never re-runs this stage. It
// regressed once already, silently, and the only symptom was a `cat:` error in
// the terminal banner that had to be reported by a user.
//
// These tests guard the snippet the same way setup_test.go and
// screenshot_script_test.go guard their scripts: by asserting on content.

func readBashrcSnippet(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("image/Dockerfile")
	if err != nil {
		t.Fatalf("read image/Dockerfile: %s", err)
	}
	const (
		startMarker = "cat >>$HOME/.bashrc <<'BASHRC'"
		endMarker   = "\nBASHRC"
	)
	_, rest, found := strings.Cut(string(data), startMarker)
	if !found {
		t.Fatalf("image/Dockerfile no longer appends the .bashrc heredoc (%q); "+
			"if the mechanism changed, update these tests deliberately", startMarker)
	}
	snippet, _, found := strings.Cut(rest, endMarker)
	if !found {
		t.Fatal("image/Dockerfile has an unterminated BASHRC heredoc")
	}
	return snippet
}

// bashrcCode strips comment lines so assertions match what the shell actually
// runs. The comments in this block deliberately name the broken constructs
// ("export PWD", the relative path) to explain why they are wrong, which would
// otherwise trip the very checks that guard against them.
func bashrcCode(snippet string) string {
	var code []string
	for _, line := range strings.Split(snippet, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	return strings.Join(code, "\n")
}

// WHY: `export PWD=...` sets a variable the shell itself maintains; the next cd
// overwrites it and the shell never actually moves. The old line did this, which
// is why the prompt showed ~/workbench/ while the shell sat in $HOME. Only cd
// changes the working directory.
func TestBashrcChangesDirectoryWithCd(t *testing.T) {
	code := bashrcCode(readBashrcSnippet(t))

	if strings.Contains(code, "export PWD") {
		t.Error("the .bashrc snippet assigns PWD directly; use cd, which is the " +
			"only thing that actually changes the shell's working directory")
	}
	if !strings.Contains(code, `cd "$HOME/workbench/`) {
		t.Error("the .bashrc snippet must cd into $HOME/workbench/<app>")
	}
}

// WHY: the original line guarded on the absolute $HOME/workbench/current but
// read the relative workbench/current, so it only resolved when the shell
// happened to start in $HOME and printed "cat: workbench/current: No such file
// or directory" everywhere else. Every reference must be $HOME-rooted.
func TestBashrcReadsCurrentByAbsolutePath(t *testing.T) {
	code := bashrcCode(readBashrcSnippet(t))

	for _, line := range strings.Split(code, "\n") {
		if !strings.Contains(line, "workbench/current") {
			continue
		}
		// Every mention of the file must carry its $HOME/ prefix.
		for _, ref := range []string{"workbench/current"} {
			for _, idx := range indexesOf(line, ref) {
				if !strings.HasSuffix(line[:idx], "$HOME/") {
					t.Errorf("the .bashrc snippet reads %q by a path that is not "+
						"$HOME-rooted, so it depends on where the shell starts: %s",
						ref, strings.TrimSpace(line))
				}
			}
		}
	}
}

// WHY: `current` names the last-launched app, whose checkout may since have been
// removed (revert, cleanup, a fresh pod restoring the file but not the tree).
// Without a directory guard the cd fails and .bashrc prints a second error, which
// is the same class of bug as the one being fixed.
func TestBashrcGuardsTargetDirectory(t *testing.T) {
	code := bashrcCode(readBashrcSnippet(t))

	if !strings.Contains(code, `[ -d "$HOME/workbench/`) {
		t.Error("the .bashrc snippet must check the app directory exists before " +
			"cd-ing into it, or a stale 'current' produces a second error")
	}
	if !strings.Contains(code, `[ -r "$HOME/workbench/current" ]`) {
		t.Error("the .bashrc snippet must check 'current' is readable before " +
			"reading it, so a fresh pod with no launched app stays silent")
	}
}

// WHY: the file is written by launch.go's writeCurrentApp with os.WriteFile and
// carries whatever trailing newline the name arrived with. An untrimmed read
// builds a path with an embedded newline, which fails the -d test and silently
// leaves the terminal in $HOME. screenshot.sh trims for the same reason.
func TestBashrcTrimsCurrentAppName(t *testing.T) {
	code := bashrcCode(readBashrcSnippet(t))

	if !strings.Contains(code, "tr -d '[:space:]'") {
		t.Error("the .bashrc snippet must strip whitespace from 'current'; the " +
			"file is written without trimming and a trailing newline breaks the path")
	}
}

func indexesOf(s, substr string) []int {
	var out []int
	for i := 0; ; {
		j := strings.Index(s[i:], substr)
		if j < 0 {
			return out
		}
		out = append(out, i+j)
		i += j + len(substr)
	}
}
