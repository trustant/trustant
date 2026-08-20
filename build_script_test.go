package main

import (
	"os"
	"strings"
	"testing"
)

// build.sh clears every existing tag before creating the one for this build, so
// the repo carries exactly one build tag. The obvious spelling is wrong in a way
// that fails silently:
//
//	git tag -d "$(git tag)" || true
//
// The quotes make the whole list ONE argument with embedded newlines, so git
// reports `tag 'a\nb\nc' not found`, `|| true` swallows the non-zero exit, and
// nothing is deleted. Tags then accumulate on every build — 69 of them before
// this was noticed — while the script still prints its normal output.
//
// `git tag -l | xargs -r git tag -d` is the correct form: xargs splits on
// newlines and -r skips the call entirely when there is nothing to delete.
// image/image.sh has always used it; this test keeps build.sh from drifting back.
func TestBuildScriptDeletesEveryExistingTag(t *testing.T) {
	content, err := os.ReadFile("build.sh")
	if err != nil {
		t.Fatalf("read build.sh: %s", err)
	}
	build := string(content)

	if !strings.Contains(build, "git tag -l | xargs -r git tag -d") {
		t.Error("build.sh must delete tags with `git tag -l | xargs -r git tag -d`, " +
			"the only form that handles more than one tag")
	}

	// The specific regression: a quoted command substitution passes every tag as
	// a single argument. Catch it whatever the surrounding quoting style.
	for _, broken := range []string{
		`git tag -d "$(git tag)"`,
		`git tag -d "$(git tag -l)"`,
	} {
		if strings.Contains(build, broken) {
			t.Errorf("build.sh uses %s, which passes all tags as one argument and deletes none", broken)
		}
	}
}

// The same invariant on the image build path, which gets the tag right today.
func TestImageScriptDeletesEveryExistingTag(t *testing.T) {
	content, err := os.ReadFile("image/image.sh")
	if err != nil {
		t.Fatalf("read image/image.sh: %s", err)
	}
	if !strings.Contains(string(content), "git tag -l | xargs -r git tag -d") {
		t.Error("image/image.sh must delete tags with `git tag -l | xargs -r git tag -d`")
	}
}

// shellCode returns the non-comment lines of a script, so assertions about what
// a script *does* are not tripped by comments explaining what it must not do.
func shellCode(script string) []string {
	var out []string
	for _, line := range strings.Split(script, "\n") {
		code := strings.TrimSpace(line)
		if code == "" || strings.HasPrefix(code, "#") {
			continue
		}
		out = append(out, code)
	}
	return out
}

// hotfix.sh carries the same tag invariant as build.sh and image.sh: it
// replaces every existing tag with the one it just computed. The quoted
// command-substitution spelling fails silently, so it is worth pinning here
// too rather than trusting the comment in the script.
func TestHotfixScriptDeletesEveryExistingTag(t *testing.T) {
	content, err := os.ReadFile("hotfix.sh")
	if err != nil {
		t.Fatalf("read hotfix.sh: %s", err)
	}
	hotfix := string(content)

	if !strings.Contains(hotfix, "git tag -l | xargs -r git tag -d") {
		t.Error("hotfix.sh must delete tags with `git tag -l | xargs -r git tag -d`, " +
			"the only form that handles more than one tag")
	}
	// Comments explain the broken spelling on purpose; only real code counts.
	for _, code := range shellCode(hotfix) {
		for _, broken := range []string{
			`git tag -d "$(git tag)"`,
			`git tag -d "$(git tag -l)"`,
		} {
			if strings.Contains(code, broken) {
				t.Errorf("hotfix.sh uses %s, which passes all tags as one argument and deletes none", broken)
			}
		}
	}
}

// hotfix.sh must never record its tag in opsroot.json and never commit.
//
// This is not style. opsroot keeps pointing at the BASE image so the next
// hotfix chains off it instead of nesting (...2118-1-1), and it is the reason
// the rollout patches the StatefulSet directly rather than calling
// `ops bestia trustable redeploy`, which would resolve the base image and roll
// out the wrong thing. The moment this script writes opsroot, that whole design
// stops making sense.
func TestHotfixScriptDoesNotWriteOpsroot(t *testing.T) {
	content, err := os.ReadFile("hotfix.sh")
	if err != nil {
		t.Fatalf("read hotfix.sh: %s", err)
	}
	hotfix := string(content)

	for _, code := range shellCode(hotfix) {
		// Reading opsroot.json is the whole point (it holds the base image);
		// writing it is what must never happen.
		if strings.Contains(code, "OPSROOT.tmp") || strings.Contains(code, `>"$OPSROOT"`) {
			t.Errorf("hotfix.sh must not write opsroot.json: %s", code)
		}
		if strings.HasPrefix(code, "git commit") || strings.Contains(code, " git commit ") {
			t.Errorf("hotfix.sh must not commit: %s", code)
		}
	}
}

// main.go embeds _build.txt at compile time, so writing it after `go build`
// would bake the BASE build string into a hotfix binary — leaving no way to
// tell from /api/version whether the hotfix is actually running. Ordering is
// the entire correctness argument here, so it is pinned rather than commented.
func TestHotfixScriptWritesBuildTxtBeforeCompiling(t *testing.T) {
	content, err := os.ReadFile("hotfix.sh")
	if err != nil {
		t.Fatalf("read hotfix.sh: %s", err)
	}
	hotfix := string(content)

	if !strings.Contains(hotfix, ">_build.txt") {
		t.Fatal("hotfix.sh must write _build.txt with the hotfix tag")
	}

	// Both build modes must call write_build_txt before their first go build.
	for _, mode := range []string{"--buildx)", "--build)"} {
		start := strings.Index(hotfix, "\n"+mode)
		if start < 0 {
			t.Fatalf("hotfix.sh has no %s branch", mode)
		}
		body := hotfix[start:]
		if end := strings.Index(body, "\n    ;;"); end > 0 {
			body = body[:end]
		}
		write := strings.Index(body, "write_build_txt")
		build := strings.Index(body, "go build")
		if write < 0 {
			t.Errorf("%s branch does not write _build.txt", mode)
			continue
		}
		if build < 0 {
			t.Errorf("%s branch does not compile", mode)
			continue
		}
		if write > build {
			t.Errorf("%s branch compiles before writing _build.txt, so the binary "+
				"would report the base build string", mode)
		}
	}
}

// CI must delegate compilation to the build scripts instead of duplicating it
// inline. Two things regress easily: someone re-inlines `go build`, or someone
// "simplifies" the two conditioned steps down to one and silently breaks either
// hotfix tags or normal ones.
func TestWorkflowDelegatesBuildsToScripts(t *testing.T) {
	content, err := os.ReadFile(".github/workflows/images.yml")
	if err != nil {
		t.Fatalf("read images.yml: %s", err)
	}
	workflow := string(content)

	for _, code := range shellCode(workflow) {
		if strings.Contains(code, "go build") {
			t.Errorf("images.yml must not compile inline; build.sh and hotfix.sh own compilation: %s", code)
		}
	}
	for _, want := range []string{
		"bash ./hotfix.sh --buildx",
		"bash ./build.sh --buildx",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("images.yml must call %q", want)
		}
	}
	// Without submodules the hotfix path cannot read opsroot.json at all.
	if !strings.Contains(workflow, "submodules: true") {
		t.Error("images.yml must check out submodules; opsroot.json lives in olaris-bestia")
	}
}

// publish.sh must not touch olaris-bestia on a hotfix tag — not the cd, not the
// commit, not the push.
//
// Asserting only on the push line would pass while a stray `git commit -a`
// still swept unrelated dirty files in that submodule under a message naming
// the hotfix tag. The push is also exactly the unauthorized olaris* push the
// repository guidelines forbid, so the whole block has to sit behind the
// not-a-hotfix guard.
func TestPublishNeverTouchesOpsrootForHotfix(t *testing.T) {
	content, err := os.ReadFile("publish.sh")
	if err != nil {
		t.Fatalf("read publish.sh: %s", err)
	}
	publish := string(content)

	guard := strings.Index(publish, "if $HOTFIX; then")
	if guard < 0 {
		t.Fatal("publish.sh must branch on a hotfix tag before touching olaris-bestia")
	}
	exit := strings.Index(publish[guard:], "exit 0")
	if exit < 0 {
		t.Fatal("publish.sh hotfix branch must exit before the olaris-bestia block")
	}

	// Every olaris-bestia command must live after the hotfix branch has exited.
	limit := guard + exit
	for _, forbidden := range []string{"cd olaris-bestia", "git push origin main"} {
		if at := strings.Index(publish, forbidden); at >= 0 && at < limit {
			t.Errorf("%q is reachable on the hotfix path", forbidden)
		}
	}
}
