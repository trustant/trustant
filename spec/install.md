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

- `TRUSTANT_DIR` is the target folder (default `~/trustant`). If it already
  exists the installer refuses and says to run `start.sh`/`start.ps1` there.
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
- If `git` is missing it runs `winget install --id Git.Git -e`, then reloads
  `$env:Path` from the Machine and User registry values so the new `git`
  resolves in this session. If `winget` is missing, it points to git-scm.com.
- Runs `start.ps1` in a child process of the same PowerShell executable with
  `-ExecutionPolicy Bypass`, because the default policy may refuse a local `.ps1`.
- It never calls `exit`: under `irm | iex` that would close the user's window.
  The body is a scriptblock that stops with `return`.
