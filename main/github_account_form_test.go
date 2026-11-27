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
	"os"
	"regexp"
	"strings"
	"testing"
)

// The managed GitHub account is the primary way Trustant authenticates against
// a repository: the backend pushes and clones over HTTPS with it and only falls
// back to the dedicated SSH key when no account is connected
// (see spec/github.md). Every flow that needs repo access must therefore offer
// the connect form itself rather than handing the user a key to paste into
// GitHub, which is what all three used to do.
//
// These assertions guard the markup, because the regression is easy to
// reintroduce (one `classList.remove('hidden')` on an SSH banner) and invisible
// to the Go tests otherwise.

func readWebFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %s", name, err)
	}
	return string(data)
}

// htmlCode strips HTML and line comments so assertions match what the page
// actually renders and runs. The comments deliberately explain the SSH-key
// fallback, which would otherwise satisfy the checks that require the GitHub
// form to be the thing on the page.
var (
	htmlCommentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)
	jsCommentPattern   = regexp.MustCompile(`(?m)^\s*//.*$`)
)

func htmlCode(page string) string {
	return jsCommentPattern.ReplaceAllString(htmlCommentPattern.ReplaceAllString(page, ""), "")
}

// The connect form exists once and is mounted by every surface that needs it.
// Copies would drift: the Configure page's copy was the only one that existed
// for a while, which is why the other flows could only offer an SSH key.
func TestGitHubAccountFormIsSharedComponent(t *testing.T) {
	module := readWebFile(t, "web/js/github-account.js")
	for _, fragment := range []string{
		"/api/github/status",
		"/api/github/login",
		"/api/github/login/cancel",
		"/api/github/logout",
		"renderGitHubAccountForm",
	} {
		if !strings.Contains(module, fragment) {
			t.Errorf("web/js/github-account.js is missing %q", fragment)
		}
	}

	for _, page := range []string{"web/applist.html", "web/configure.html"} {
		source := readWebFile(t, page)
		if !strings.Contains(source, `src="js/github-account.js"`) {
			t.Errorf("%s does not load the shared GitHub account form", page)
		}
		// The device-code flow belongs to the component. A page re-declaring it
		// is a copy that will drift out of sync.
		if strings.Contains(htmlCode(source), "'/api/github/login/cancel'") {
			t.Errorf("%s re-implements the GitHub login flow instead of mounting the shared form", page)
		}
	}
}

// All three applist surfaces that need repository access must mount the form.
func TestApplistOffersGitHubLoginWhereverRepoAccessIsNeeded(t *testing.T) {
	page := htmlCode(readWebFile(t, "web/applist.html"))
	surfaces := map[string]struct{ container, mount string }{
		"Git Push modal":         {"gitPushGithubPanel", "gitPushGithubForm"},
		"Add App panel":          {"addAppGithubPanel", "addAppGithubForm"},
		"My Application Starter": {"ownStarterGithubPanel", "ownStarterGithubForm"},
	}
	for name, ids := range surfaces {
		if !strings.Contains(page, `id="`+ids.container+`"`) {
			t.Errorf("%s has no GitHub connect panel (%s)", name, ids.container)
		}
		if !strings.Contains(page, "'"+ids.mount+"'") {
			t.Errorf("%s never mounts the shared GitHub form (%s)", name, ids.mount)
		}
	}
}

// The own-starter modal used to instruct the user to reach their repo with the
// ssh key shown below it. That sentence contradicts the connect form now in the
// modal, so it must not come back.
func TestOwnStarterModalDoesNotDirectUsersToTheSSHKey(t *testing.T) {
	page := htmlCode(readWebFile(t, "web/applist.html"))
	if strings.Contains(page, "access the repo with the ssh key") {
		t.Error("the My Application Starter modal still tells the user to use the ssh key")
	}
	if !strings.Contains(page, "Connect your GitHub account below to access it") {
		t.Error("the My Application Starter modal does not point the user at the GitHub connect form")
	}
}

// The SSH key survives as a fallback for installations that never connect an
// account, but it must be behind a disclosure rather than presented up front.
func TestSSHKeyRemainsAvailableAsACollapsedFallback(t *testing.T) {
	page := readWebFile(t, "web/applist.html")
	for _, fragment := range []string{
		"Use an SSH deploy key instead",
		"Use an SSH key instead",
		"/api/sshkey",
	} {
		if !strings.Contains(page, fragment) {
			t.Errorf("the SSH fallback lost %q", fragment)
		}
	}
}
