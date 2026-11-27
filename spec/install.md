# Repository-root `install`

> **Paths.** The `.env` that `install` writes is `<target>/trustant/main/.env`; the server reads it from `main/`.

One file that is both a bash and a PowerShell script (a polyglot), so a single
URL prepares a Trustant checkout on every host: it clones, downloads the big
artifacts into `trustant/dist`, and stops. Starting is left to the user:

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

- **Target folder**: the installer prints

      Trustant will be installed in folder trustant in "<cwd>".
      Press ENTER to accept the proposed path or specify another parent folder.
      Parent folder [<cwd>]:

  The answer is the parent folder: a `trustant` folder is always created
  inside it (Enter keeps the current
  directory; `~` is expanded in bash). The checkout is `<target>/trustant`. Bash asks only when a
  terminal is available (stdin or `/dev/tty`); otherwise the default is used.
- `TRUSTANT_DIR` names the checkout directly and skips the question
  (non-interactive installs); its parent is the target.
- **Path checks**, right after the answer and before anything is installed,
  each exiting with an `ERROR: ...`:
  - the target must be a full path: starting with `/` (bash) or a drive letter
    `X:` (PowerShell);
  - the target folder must already exist - it is never created; only
    `trustant` is created inside it;
  - `<target>/trustant` must not exist; the error suggests removing it
    (`rm -rf <dest>` / `Remove-Item -Recurse -Force <dest>`) or running
    `start.sh`/`start.ps1` there.

  The installer then `cd`s (`Set-Location`, which also switches drive) into
  the target.
- **Requirements**, checked before anything is cloned (bash: after the
  per-host prerequisites; PowerShell: after the path checks). Every failing
  check is reported (`ERROR: ...`), then the installer stops:
  - at least **8 GB of memory**: the threshold is 7.5 GiB, because an "8 GB"
    machine reports a bit less (`hw.memsize`, `/proc/meminfo` `MemTotal`,
    `Win32_ComputerSystem.TotalPhysicalMemory`);
  - at least **4 CPUs** (`hw.ncpu`, `nproc`, `[Environment]::ProcessorCount`);
  - at least **50 GB free** where the checkout goes (`df -Pk` on the nearest
    existing ancestor of `<target>`; on Windows, the drive of the checkout);
  - **`miniops.me` resolves to `127.0.0.1`** (`dscacheutil`, `getent
    ahostsv4`, `[Net.Dns]::GetHostAddresses`). Otherwise the router is
    blocking it, and the user is told to change their DNS to 8.8.8.8 and
    1.1.1.1 or to add an exception for `miniops.me` to the router;
  - **Linux only: ports 80 and 443 are free** (`sudo ss -ltnp`), since k3s runs
    on the host. The listeners are printed and the user is pointed to e.g.
    `sudo systemctl stop nginx`. The check is skipped when the `k3s` unit is
    already active (Trustant's own k3s holds those ports), and only warns when
    `ss` is missing.
- **Preloaded `dist/`**: after the clone, if `<target>/dist` exists its content
  is copied (not moved) into `<target>/trustant/dist`, the cache `start.sh`
  reads, so the `.deb` and ollama tarball are not downloaded again. A failed
  copy only warns: the download step fetches whatever is missing. With
  `TRUSTANT_DIR`, `<target>` is its parent directory.
- `TRUSTANT_REF` is an optional branch or tag, passed to `git clone --branch`.
- Clone: `git clone --recurse-submodules https://github.com/trustant/trustant`.
  If the clone fails, the partial folder is removed.
- **Downloads**, after `.env` is written, into `<target>/trustant/dist` with
  the exact names and URLs `start.sh` uses (`ensure_deb`,
  `ensure_ollama_tarball`), so `start.sh` finds them and downloads nothing:
  - `truserverless_<ver>_<arch>.deb` from
    `https://pub.trustant.ai/truserverless/`, `<ver>` being the first line of
    the checkout's `main/truserverless.txt`. Missing pin or failed download: error,
    stop.
  - `ollama-<ver>-linux-<arch>.tar.zst` from the ollama GitHub release,
    `<ver>` from `ARG OLLAMA_VERSION=` in `image/Dockerfile`. A missing version
    or failed download only warns: `start.sh` then uses the ollama installer.
  - `<arch>` is `arm64` or `amd64`: `dpkg --print-architecture` on Linux,
    `uname -m` on macOS, `RuntimeInformation.OSArchitecture` (falling back to
    `PROCESSOR_ARCHITEW6432`/`PROCESSOR_ARCHITECTURE`) on Windows, since the
    VM and the WSL guest run the host arch.
  - A non-empty file already there (e.g. from the preloaded `dist/`) is kept.
    Downloads go to `<file>.part` and are moved into place only when complete
    (`curl`, `curl.exe` on Windows - much faster than `Invoke-WebRequest`).
- **It does not start Trustant.** It ends with
  `Trustant download is ready, please cd <dir> and type ./start.sh`
  (PowerShell: `... type .\start.ps1`, plus a hint to run
  `powershell -ExecutionPolicy Bypass -File .\start.ps1` if the default policy
  refuses a local `.ps1`, and
  `Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy Bypass` to make it
  persistent). Under `curl | bash` a `start.sh` run
  could leave the terminal in raw mode (staircase output, no Ctrl-C); run
  from a normal shell it does not.

## Bash part

Hosts are told apart by `uname -s`:

- **Darwin**: requires `brew` (otherwise it points to https://brew.sh), then runs
  `brew install lima` if `limactl` is missing and `brew install git` if `git` is
  missing.
- **Linux**: requires `apt-get` and `sudo` (`sudo -v` unless already root; the
  port check below relies on it), then
  runs `apt-get install -y git` if `git` is missing.
- **MINGW/MSYS/CYGWIN**: tells the user to use PowerShell and exits.
- Anything else: exits.

Under `curl | bash`, stdin is the script itself. Every prompt (the path
question, sudo, brew) therefore reads `/dev/tty` when stdin is not a terminal;
`curl` reads `/dev/null`.

## PowerShell part

- Refuses on anything other than Windows (`OSVersion.Platform -eq 'Win32NT'`).
- Paths are resolved against the PowerShell location
  (`GetUnresolvedProviderPathFromPSPath`), not the process directory. A bare
  drive (`D:`) means its root (`D:\`).
- Refuses if the checkout path contains a space, or is not a drive-letter path
  (any drive: `C:\`, `D:\`...). The folder is used from WSL over
  `/mnt/<drive>`, and a space breaks `.env` and the scripts.
- If `git` is missing it runs `winget install --id Git.Git -e`, then reloads
  `$env:Path` from the Machine and User registry values so the new `git`
  resolves in this session. If `winget` is missing, it points to git-scm.com.
- It never calls `exit`: under `irm | iex` that would close the user's window.
  The body is a scriptblock that stops with `return`.


## .env

Both parts write `<install>/.env` after the clone and before the downloads,
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
