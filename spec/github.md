# Managed personal GitHub account

This specification owns `github.go` and the `/api/github/*` backend surface.

## Boundary

Trustant supports one `github.com` account per single-user installation. The
backend invokes the pinned official `gh` CLI. The browser receives only
sanitized account metadata, repository metadata, device login URL/code, and
bounded lifecycle state.

Managed state lives under:

- `$WORKSPACE_DIR/.trustant/github/gh` as `GH_CONFIG_DIR`;
- `$WORKSPACE_DIR/.trustant/github/gitconfig` as `GIT_CONFIG_GLOBAL`;
- `$WORKSPACE_DIR/.trustant/github/home` as the managed command `HOME`.

Directories use mode `0700`; credential-bearing files use `0600`. Trustant
strips inherited `GH_TOKEN`, `GITHUB_TOKEN`, and enterprise token variables
before every managed command. The managed environment is attached only to
backend-owned `gh` and Git subprocesses. It is never added to the server
process, application env files, Pi/TruACP, MCP servers, browser storage, logs,
or API responses.

## API

- `GET /api/github/status`: returns `available`, `authenticated`, `hostname`,
  `protocol`, and authenticated `login`. It never returns scopes or tokens
  obtained through a token-printing command.
- `POST /api/github/login`: starts one bounded
  `gh auth login --hostname github.com --git-protocol https --web
  --skip-ssh-key` process. A second active login returns conflict.
- `GET /api/github/login`: returns only `idle`, `connecting`, `connected`,
  `cancelled`, or `failed`, plus the device URL/code while connecting and a
  sanitized error when failed. An authenticated account is authoritative:
  stale terminal login state is reconciled to `connected` and cannot be
  returned alongside a valid authenticated status.
- `POST /api/github/login/cancel`: cancels the active login process.
- `POST /api/github/logout`: invokes managed `gh auth logout`, then removes
  only Trustant-owned GitHub state.
- `GET /api/github/repos`: requires authentication and returns a bounded page
  of `name`, `visibility`, and `default_branch`.

Login has a finite ten-minute deadline. Ordinary status and repository commands
have finite deadlines. Raw `gh` output is not logged or returned. Login output
is parsed only for the official device URL and one-time code.

After successful login, `gh auth setup-git --hostname github.com` configures the
credential helper through the isolated `GIT_CONFIG_GLOBAL`. If the device-flow
process exits non-zero after GitHub has already persisted a valid account,
Trustant treats the authenticated status as authoritative and still completes
credential-helper setup. Setup succeeds only when the isolated file contains
the official `gh auth git-credential` helper. A transient `setup-git` process
failure is retried exactly once with a fresh bounded command after a short
delay. Trustant does not retry the Git clone, pull, or push operation itself.

## Presentation

The connect form is **one shared component**, `web/js/github-account.js`. It
renders the status message, the device panel (one-time code, Copy Code, Open
GitHub), the error line, and the Connect / Cancel / Disconnect actions, and owns
the polling loop against the endpoints above. Each mount prefixes its own
element ids so several copies can coexist on one page, and publishes its
instance on `window` under a caller-supplied name, because the rendered markup
drives it through inline `onclick` attributes.

It is mounted by every surface that needs repository access:

- the Configure page's **GitHub Account** card, which supplies its own status
  pill through `badgeId` and keeps its Git User card coupling through the
  `onStatus` callback;
- the **Git Push** popup, when a push needs a production repository;
- the **Add Application** modal, in "My Application Starter" mode;
- the **My Application Starter** warning dialog, where connecting is what grants
  access to the user's own private starter repository.

Connecting is the primary authentication path in all four, because managed
HTTPS is what the backend prefers. The dedicated SSH key is a **fallback**: each
applist surface offers it behind a collapsed "Use an SSH key instead"
disclosure, and only while no account is connected. No surface may present the
SSH key as the primary path or instruct the user to reach a repository with it
while the connect form is available. Hosts hang their own behaviour off the
`onStatus` / `onAuthenticated` callbacks rather than reimplementing the flow;
`github_account_form_test.go` guards these invariants.

## Repository operations

Authenticated onboarding validates the requested `org/repo`, obtains its
sanitized HTTPS clone URL and real default branch, and uses the managed Git
environment. Disconnected onboarding preserves SSH-key and public-HTTPS
fallback behavior.

Pull, normal push, and force push follow the bare workspace repository's
symbolic default branch. Managed HTTPS is selected only while the account is
authenticated; otherwise Trustant uses its dedicated SSH key. GitHub
authentication never changes OpenServerless publishing authorization.
Before cloning, pulling, or pushing through managed HTTPS, Trustant verifies
the isolated credential helper and repairs it with `gh auth setup-git` when it
is absent. This makes accounts created by an interrupted or older login flow
self-healing without requiring the user to authenticate again.

GitHub Enterprise, multiple account profiles, and exposing credentials to agent
shells are out of scope.
