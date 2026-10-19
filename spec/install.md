# Repository-root `install`

One file that is both a bash and a PowerShell script (a polyglot), so a single
URL installs Trustant on every host:

    curl -fsSL get.trustant.ai | bash      # macOS, Linux
    irm get.trustant.ai | iex              # Windows

Hosting `get.trustant.ai` (a redirect to the raw file) is out of scope here.

## Polyglot layout

`echo @' ... '@ | out-null` wraps the bash part in a PowerShell here-string, so
PowerShell discards it. The bash part `exit`s before its trailing
`echo > /dev/null <<"out-null"`, a heredoc that would swallow the PowerShell part
anyway. The file is pure ASCII, because Windows PowerShell 5.1 reads BOM-less
scripts as ANSI.

## Common behaviour

- **Target folder**: the installer asks `Install Trustant in [<cwd>]:`,
  defaulting to the current directory (Enter keeps it; `~` is expanded in
  bash). A folder that does not exist is created, and the installer `cd`s
  (`Set-Location`, which also switches drive) into it. The checkout is
  `<target>/trustant`. Bash asks only when a terminal is available (stdin or
  `/dev/tty`); otherwise the default is used.
- `TRUSTANT_DIR` names the checkout directly and skips the question
  (non-interactive installs).
- **Preloaded `dist/`**: if `<target>/trustant` holds only a `dist/` folder,
  `dist/` is set aside as `<target>/.trustant-dist.<pid>`, the clone runs into
  the now-absent `<target>/trustant`, and `dist/` is moved back. If the clone
  fails it is moved back anyway. `start.sh` then finds the `.deb` and ollama
  tarball in its cache and downloads nothing. Any other existing
  `<target>/trustant` makes the installer refuse and say to run
  `start.sh`/`start.ps1` there.
- `TRUSTANT_REF` is an optional branch or tag, passed to `git clone --branch`.
- Clone: `git clone --recurse-submodules https://github.com/trustant/trustant`.
  If the clone fails, the partial folder is removed.
- It then runs `./start.sh` (bash) or `.\start.ps1` (PowerShell) in the folder.

## Bash part

Hosts are told apart by `uname -s`:

- **Darwin**: requires `brew` (otherwise it points to https://brew.sh), then runs
  `brew install lima` if `limactl` is missing and `brew install git` if `git` is
  missing.
- **Linux**: requires `apt-get` and `sudo` (`sudo -v` unless already root), then
  runs `apt-get install -y git` if `git` is missing.
- **MINGW/MSYS/CYGWIN**: tells the user to use PowerShell and exits.
- Anything else: exits.

Under `curl | bash`, stdin is the script itself. Every prompt (sudo, brew, and
`start.sh`) therefore reads `/dev/tty` when stdin is not a terminal.

## PowerShell part

- Refuses on anything other than Windows (`OSVersion.Platform -eq 'Win32NT'`).
- Paths are resolved against the PowerShell location
  (`GetUnresolvedProviderPathFromPSPath`), not the process directory, so a
  relative answer after a `cd D:\` lands on `D:`.
- Refuses if the checkout path contains a space, or is not a drive-letter path
  (any drive: `C:\`, `D:\`...). The folder is used from WSL over
  `/mnt/<drive>`, and a space breaks `.env` and the scripts.
- If `git` is missing it runs `winget install --id Git.Git -e`, then reloads
  `$env:Path` from the Machine and User registry values so the new `git`
  resolves in this session. If `winget` is missing, it points to git-scm.com.
- Runs `start.ps1` in a child process of the same PowerShell executable with
  `-ExecutionPolicy Bypass`, because the default policy may refuse a local `.ps1`.
- It never calls `exit`: under `irm | iex` that would close the user's window.
  The body is a scriptblock that stops with `return`.


## .env

Both parts write `<install>/.env` after the clone and before `start.sh`/`start.ps1`,
then create `<install>/workspace` and `<install>/workbench`. All three are
git-ignored, so the checkout stays clean.

    WORKSPACE_DIR=<absolute-path-of-installation>/workspace
    WORKBENCH_DIR=<absolute-path-of-installation>/workbench
    OLLAMA_ENDPOINT=http://localhost:11434
    AIP_REGISTER_URL=https://api.nuvolaris.io/_register
    AIP_BASE_URL=https://api.nuvolaris.io/api/v2/
    GIT_USER=TrustantUser
    GIT_EMAIL=noreply@example.com

- The hidden flags `ENABLE_LICENSE` and `ENABLE_REGOLO` are never written; the
  code defaults apply (see spec/0-preflight.md).
- **Bash**: the path is `pwd` of the clone. Lima mounts that folder at the same
  path in the VM, so the same path works there.
- **PowerShell**: `.env` is read inside WSL, so the path is the `/mnt/<drive>`
  form of the install folder (`C:\Users\x\trustant` becomes
  `/mnt/c/Users/x/trustant`). The file is written with LF endings and no BOM.
- `.env.dist` also lists `GITHUB_TOKEN`, but this `.env` leaves it out.
  `setup.sh` treats it as optional (see spec/setup.md), so its check that every
  `.env.dist` key is set still passes.
