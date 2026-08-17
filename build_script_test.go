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
