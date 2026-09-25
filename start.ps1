# start.ps1 - bring up the Trustant development environment on a Windows host.
# Copyright 2025-2026 Nuvolaris Inc
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.
#
# The Windows counterpart of start.sh's macOS half: where start.sh provisions a
# Lima VM named 'trudev', this provisions a WSL2 distribution of the same name
# from the Ubuntu-24.04 image, mirrors the current Windows user into it with
# passwordless sudo and bash as the login shell, and then runs THIS repo's
# ./start.sh inside it over the /mnt/<drive> mount - so the sources stay on
# Windows and only the Linux environment lives in WSL.
#
# start.sh then takes over on its native-Linux path (uname -s = Linux): it
# installs the Trustant .deb (k3s), ollama, kubefwd, gh, the host-rewrite proxy,
# writes the support files and runs ./setup.sh. The .deb and the ollama build are
# cached under dist\ on the Windows side of the mount, so destroying the distro
# and re-running re-downloads neither. Nothing in start.sh is
# Windows-aware; this script only has to hand it an Ubuntu that satisfies its
# preflight (Ubuntu/Debian, amd64/arm64, passwordless sudo, systemd).
#
#   .\start.ps1                 provision, run ./start.sh, then ./run.sh in it
#   .\start.ps1 -v              ... but open VS Code on the mount instead of run.sh
#   .\start.ps1 -n              ... but finish without run.sh and without VS Code
#   .\start.ps1 -NoStart        provision only, do not run ./start.sh
#   .\start.ps1 -s / -Stop      terminate the distro (keeps it; re-run to restart)
#   .\start.ps1 -k / -Destroy   unregister the distro - DELETES its filesystem
#   .\start.ps1 -User bob       mirror a different user name (default: $env:USERNAME)
#   .\start.ps1 -Distro name    use a different WSL instance name (default: trudev)
#   .\start.ps1 -BaseDistro X   install from a different image (default: Ubuntu-24.04)
#
# NOTE: this file is deliberately pure ASCII. Windows PowerShell 5.1 reads a
# BOM-less .ps1 as ANSI, so any non-ASCII character here would be mangled.
#
#Requires -Version 5.1
# Every flag carries start.sh's short form as an alias, so the same muscle
# memory works on both hosts: -v, -n, -s, -k. The script has no
# [CmdletBinding()], so there are no common parameters and -v cannot collide
# with -Verbose. All four bind by exact alias match, which PowerShell resolves
# ahead of any prefix match - so -n is -NoRun (not the -NoStart prefix) and -s
# is -Stop.
#
# $Distro is the INSTANCE name and $BaseDistro the IMAGE it is installed from -
# the same split start.sh makes on macOS, where the Lima instance is called
# 'trudev' (VM_NAME) whatever the base image is. Naming the instance after the
# Store distribution would mean adopting - and, on -k, unregistering - a
# general-purpose Ubuntu the user created themselves.
param(
    [string]$Distro = 'trudev',
    [string]$BaseDistro = 'Ubuntu-24.04',
    [string]$User = '',
    [Alias('v')][switch]$VSCode,
    [Alias('n')][switch]$NoRun,
    [switch]$NoStart,
    [Alias('s')][switch]$Stop,
    [Alias('k')][switch]$Destroy
)

$ErrorActionPreference = 'Stop'

# The Trustant VM sizing from start.sh's Lima config, reused for the WSL2 VM.
$VM_CPUS = 4
$VM_MEMORY = '8GB'
$VM_SWAP = '8GB'
# What start.sh's native path shells out to and the base image may not have.
# zstd and unzip are needed to unpack what the flow downloads (the Trustant
# .deb payload and the zipped release archives); iptables is what the package's
# postinst installs its firewall dropin with. gh is here as belt and braces: the
# bootstrap runs as root before start.sh is invoked at all, so the distro has
# the CLI whatever start.sh's own internal ordering does - it needs it for the
# git credential helper and for cloning the private submodules.
$BASE_PACKAGES = 'sudo curl ca-certificates git iproute2 iptables zstd unzip gh'

function Write-Ok   { param([string]$m) Write-Host "OK  $m" -ForegroundColor Green }
function Write-Warn { param([string]$m) Write-Host "!!  $m" -ForegroundColor Yellow }
function Write-Step { param([string]$m) Write-Host "--- $m ---" -ForegroundColor Cyan }
function Fail       { param([string]$m) Write-Host "XX  $m" -ForegroundColor Red; exit 1 }

# wsl.exe emits UTF-16LE unless WSL_UTF8 is set, which turns every `--list`
# parse into garbage. Set it for this process and read the console as UTF-8.
$env:WSL_UTF8 = '1'
$prevOutputEncoding = [Console]::OutputEncoding
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)

function Invoke-WslText {
    # Run wsl.exe and return its output as an array of clean lines.
    $out = & wsl.exe @args
    if ($null -eq $out) { return @() }
    return @($out | ForEach-Object { ($_ -replace "`0", '').Trim() } | Where-Object { $_ -ne '' })
}

function Get-WslDistros {
    $lines = Invoke-WslText --list --quiet
    if ($LASTEXITCODE -ne 0) { return @() }
    return $lines
}

function Test-DistroExists {
    param([string]$Name)
    $found = Get-WslDistros | Where-Object { $_ -eq $Name }
    return [bool]$found
}

function Test-WslInstallSupportsName {
    # Does this wsl.exe accept `--install ... --name <name>`?
    #
    # Matched on the help TEXT and never on the exit code: `wsl --help` prints
    # the full, correct help and still exits non-zero (255), so an exit-code
    # test would reject a wsl.exe that is perfectly capable. `wsl --install
    # --help` is not a valid command line at all on current builds (2.9.4
    # answers Wsl/E_INVALIDARG), which is why the install options are read out
    # of `wsl --help` instead - it lists them in the --install section.
    #
    # Scoped to that section rather than grepping the whole text: `--name` also
    # appears under `--mount`, which has carried it far longer, so a bare match
    # would pass on a wsl.exe that cannot name a distribution at install time.
    # Read raw, NOT through Invoke-WslText: the section is delimited by
    # indentation (top-level commands are less indented than their options) and
    # Invoke-WslText trims it away.
    $lines = @(& wsl.exe --help) | ForEach-Object { ($_ -replace "`0", '').TrimEnd() }
    $sectionIndent = -1
    foreach ($line in $lines) {
        if ($line -notmatch '^(\s*)--(\S+)') { continue }
        $indent = $Matches[1].Length
        $flag = $Matches[2]
        if ($sectionIndent -lt 0) {
            if ($flag -eq 'install') { $sectionIndent = $indent }
            continue
        }
        # Back out to the command level: the --install section has ended.
        if ($indent -le $sectionIndent) { break }
        if ($flag -eq 'name') { return $true }
    }
    return $false
}

function Restore-Console {
    [Console]::OutputEncoding = $prevOutputEncoding
}

# --- resolve the mirrored user name -----------------------------------------
# start.sh mirrors the macOS user into the VM so the mounted worktree keeps the
# host's ownership. Same idea here: default to the Windows account name, folded
# to a valid Linux user name.
if ([string]::IsNullOrWhiteSpace($User)) {
    $User = ($env:USERNAME -replace '[^A-Za-z0-9_-]', '').ToLower()
}
if ($User -notmatch '^[a-z_][a-z0-9_-]*$') {
    Fail "'$User' is not a usable Linux user name - pass one with -User <name>"
}

# --- preflight: WSL itself ---------------------------------------------------
if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) {
    Fail 'wsl.exe not found. Open an Administrator terminal, run: wsl --install, reboot, then re-run this script.'
}
$null = Invoke-WslText --version
if ($LASTEXITCODE -ne 0) {
    Fail 'the Store version of WSL is required (wsl --version failed). Run "wsl --update" in an Administrator terminal, then re-run this script.'
}
Write-Ok 'WSL is available'

# --- lifecycle: -s / -Stop and -k / -Destroy ---------------------------------
# These mirror ./start.sh -s and ./start.sh -k, short forms included, and take
# the same shape: -s keeps the distribution so a later run restarts it with no
# reinstall, -k unregisters it and its filesystem goes with it. They only ever
# touch the WSL distribution, never the Windows host. Both exit before the -v
# preflight and before any provisioning: tearing a distro down must not need an
# editor, a mount, or a repository.
if ($Stop) {
    if (Test-DistroExists $Distro) {
        Write-Step "Terminating '$Distro' (keeping it)"
        & wsl.exe --terminate $Distro | Out-Null
        Write-Ok "'$Distro' terminated - run .\start.ps1 to start it again (no reinstall)"
    } else {
        Write-Warn "'$Distro' does not exist - nothing to do"
    }
    Restore-Console
    exit 0
}

if ($Destroy) {
    if (-not (Test-DistroExists $Distro)) {
        Write-Warn "'$Distro' does not exist - nothing to do"
        Restore-Console
        exit 0
    }
    Write-Warn "This unregisters '$Distro' and PERMANENTLY DELETES its Linux filesystem."
    Write-Warn "This repository lives on Windows and is not affected."
    $answer = Read-Host "Type the distribution name to confirm"
    if ($answer -ne $Distro) { Fail 'aborted - nothing was deleted' }
    Write-Step "Unregistering '$Distro'"
    & wsl.exe --unregister $Distro
    if ($LASTEXITCODE -ne 0) { Fail "failed to unregister '$Distro'" }
    Write-Ok "'$Distro' destroyed"
    Restore-Console
    exit 0
}

# --- -v preflight: `code` on the WINDOWS PATH --------------------------------
# Checked here, before anything is provisioned, for the same reason start.sh
# checks it before touching the VM: a run that downloads a ~4GB package must
# not end by discovering the editor is missing. It sits after -Stop/-Destroy,
# which exit above - tearing a distro down never needs VS Code.
if ($VSCode) {
    if ($NoRun) { Write-Warn '-v and -n given together: opening VS Code (-v wins)' }
    if (-not (Get-Command code -ErrorAction SilentlyContinue)) {
        Fail "'code' not found on the Windows PATH. Install VS Code on Windows (not inside the distro) with the WSL extension, or re-run with -n."
    }
}

# --- preflight: `wsl --install --name` -----------------------------------------
# Installing under our own instance name is what keeps this distro separate from
# whatever else is registered on the machine, and it only works if wsl.exe can be
# told a name at install time; older builds cannot. Checked here, before anything
# is provisioned, and only when the distro has to be created: a reused '$Distro'
# never calls --install, and an old wsl.exe must not block a run that would not
# have needed the flag. It sits after -Stop/-Destroy, which exit above - tearing
# a distro down never installs one.
$DistroExists = Test-DistroExists $Distro
if (-not $DistroExists) {
    if (-not (Test-WslInstallSupportsName)) {
        Fail "this WSL is too old: 'wsl --install --name' is not supported. Run ""wsl --update"" in an Administrator terminal, then re-run this script."
    }
    Write-Ok "wsl --install supports --name"
}

# A distro left behind by an earlier start.ps1, which installed under the base
# image's own name. Reported only - this script must never unregister a
# distribution the user did not name.
if ($Distro -eq 'trudev' -and (Test-DistroExists 'Ubuntu-24.04')) {
    Write-Warn "'Ubuntu-24.04' is registered. Earlier versions of start.ps1 used that name for the"
    Write-Warn "Trustant dev distro. If it is ours and no longer wanted, remove it with:"
    Write-Warn "    .\start.ps1 -k -Distro Ubuntu-24.04"
}

# --- the repository this script sits in, which is what gets mounted ----------
$RepoWin = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($RepoWin)) { $RepoWin = (Get-Location).Path }
if (-not (Test-Path (Join-Path $RepoWin 'start.sh'))) {
    Fail "start.sh not found next to this script ($RepoWin) - run start.ps1 from the trustable-app checkout"
}

# bash cannot run a CRLF script: it dies with "syntax error near unexpected
# token $'in\r'". start.sh, setup.sh and run.sh are all executed from the mount,
# i.e. straight off the Windows filesystem, so the checkout itself has to be LF.
# The repo ships a .gitattributes with "* text=auto eol=lf" for exactly this; a
# checkout made before it existed still has CRLF and must be refreshed.
if ([System.IO.File]::ReadAllBytes((Join-Path $RepoWin 'start.sh')) -contains 13) {
    Write-Warn 'start.sh in this checkout has Windows CRLF line endings, which bash cannot run.'
    Write-Warn 'Refresh the working tree to LF (this repo ships .gitattributes with eol=lf):'
    Write-Warn "    git -C `"$RepoWin`" add --renormalize ."
    Write-Warn "    git -C `"$RepoWin`" checkout --force -- ."
    Fail 'CRLF line endings in start.sh'
}
Write-Ok 'the checkout has LF line endings'

# --- .wslconfig: give the WSL2 VM the same shape as the Lima VM --------------
# .wslconfig is global to every distribution on this machine, so it is only
# written when absent; an existing one is reported, never rewritten.
$WslConfig = Join-Path $env:USERPROFILE '.wslconfig'
$WroteWslConfig = $false
if (Test-Path $WslConfig) {
    # Only nag about the sizing when this is somebody else's .wslconfig; the one
    # written below already carries the values Trustant wants.
    if ((Get-Content $WslConfig -Raw) -match 'trustable-app/(start|setup)\.ps1') {
        Write-Ok ".wslconfig already written by start.ps1 ($WslConfig)"
    } else {
        Write-Ok ".wslconfig already present at $WslConfig (left untouched)"
        Write-Warn "Trustant wants at least: memory=$VM_MEMORY, processors=$VM_CPUS, swap=$VM_SWAP"
    }
} else {
    Write-Step "Writing $WslConfig"
    $cfg = @"
# Written by trustable-app/start.ps1. Trustant runs a full k3s stack inside
# WSL, so it needs the same headroom start.sh gives the Lima VM on macOS.
[wsl2]
memory=$VM_MEMORY
processors=$VM_CPUS
swap=$VM_SWAP
localhostForwarding=true
"@
    [System.IO.File]::WriteAllText($WslConfig, ($cfg -replace "`r`n", "`n"), (New-Object System.Text.UTF8Encoding($false)))
    $WroteWslConfig = $true
    Write-Ok "created .wslconfig (memory=$VM_MEMORY, processors=$VM_CPUS, swap=$VM_SWAP)"
}

# --- create the distribution -------------------------------------------------
& wsl.exe --set-default-version 2 | Out-Null
if ($DistroExists) {
    Write-Ok "WSL distribution '$Distro' already exists"
} else {
    Write-Step "Installing WSL distribution '$Distro' from '$BaseDistro' (this downloads the Ubuntu image)"
    # --no-launch keeps the interactive first-run account wizard out of the way:
    # the account is created below, with the name and sudo rights we want.
    # --name registers the instance under OUR name, so nothing here can adopt or
    # later destroy a '$BaseDistro' the user installed for themselves.
    & wsl.exe --install --distribution $BaseDistro --name $Distro --no-launch
    if ($LASTEXITCODE -ne 0) {
        Fail "wsl --install --distribution $BaseDistro --name $Distro failed. Check the available images with: wsl --list --online"
    }
    if (-not (Test-DistroExists $Distro)) {
        Fail "'$Distro' did not register. Check: wsl --list --verbose"
    }
    Write-Ok "'$Distro' installed"
}

# A WSL1 distribution has no real kernel and cannot run k3s.
$verbose = Invoke-WslText --list --verbose
$row = $verbose | Where-Object { $_ -match "(^|\s)$([regex]::Escape($Distro))\s" }
if ($row -and ($row -match '\s1\s*$')) {
    Write-Step "Converting '$Distro' to WSL2"
    & wsl.exe --set-version $Distro 2
    if ($LASTEXITCODE -ne 0) { Fail "could not convert '$Distro' to WSL2 - k3s needs a WSL2 kernel" }
    Write-Ok "'$Distro' is now WSL2"
}

# --- bootstrap the distribution ---------------------------------------------
# Everything below is plain Ubuntu bash and is written to a temp file rather
# than passed inline: piping into `wsl` from PowerShell rewrites the line
# endings, and bash refuses a script with CRLFs.
$bootstrap = @'
#!/bin/bash
set -euo pipefail
: "${TRU_USER:?TRU_USER is not set}"
export DEBIAN_FRONTEND=noninteractive

# 1. Packages start.sh's native path assumes are present (it shells out to
#    dpkg, curl, git and `ip route`). Only touch apt when something is missing.
missing=""
for p in $TRU_PACKAGES; do
  dpkg -s "$p" >/dev/null 2>&1 || missing="$missing $p"
done
if [ -n "$missing" ]; then
  echo "installing:$missing"
  apt-get update -qq
  # shellcheck disable=SC2086
  apt-get install -y -qq $missing
fi

# 2. Mirror the Windows user. UID 1000 when it is free, so files created in the
#    distro and files on the /mnt mount agree on ownership.
if ! id "$TRU_USER" >/dev/null 2>&1; then
  if getent passwd 1000 >/dev/null 2>&1; then
    useradd -m -s /bin/bash "$TRU_USER"
  else
    useradd -m -s /bin/bash -u 1000 "$TRU_USER"
  fi
  echo "created user $TRU_USER ($(id -u "$TRU_USER"))"
fi
usermod -aG sudo "$TRU_USER"
# bash, always: setup.sh writes PATH exports into ~/.bashrc and run.sh is a bash
# script, so a distro that reused an account with a different shell would break
# the login environment the rest of the flow relies on. Only call usermod when
# it would actually change something - it is noisy on stderr otherwise.
login_shell="$(getent passwd "$TRU_USER" | cut -d: -f7)"
if [ "$login_shell" != /bin/bash ]; then
  usermod -s /bin/bash "$TRU_USER"
  login_shell=/bin/bash
fi
echo "shell for $TRU_USER: $login_shell"

# start.sh's preflight_native, setup.sh and run.sh all require `sudo -n`.
printf '%s ALL=(ALL) NOPASSWD:ALL\n' "$TRU_USER" > "/etc/sudoers.d/90-$TRU_USER"
chmod 0440 "/etc/sudoers.d/90-$TRU_USER"

# 3. /etc/wsl.conf.
#    systemd            - k3s is a systemd service; preflight_native refuses a
#                         distro without it.
#    default user       - so `wsl -d <distro>` lands as the mirrored user.
#    automount metadata - keeps unix modes on /mnt/<drive>, so the shell scripts
#                         in the mounted worktree stay executable and setup.sh
#                         can chmod what it generates.
#    appendWindowsPath  - OFF. setup.sh installs go, node, uv and npm into the
#                         distro; a Windows PATH leaking in makes `node`/`go`
#                         resolve to Windows .exe files inside Linux builds.
cat >/etc/wsl.conf <<CONF
[boot]
systemd=true

[user]
default=$TRU_USER

[automount]
enabled=true
options="metadata"

[interop]
enabled=true
appendWindowsPath=false
CONF
echo "bootstrap complete"
'@

$tmpScript = Join-Path ([System.IO.Path]::GetTempPath()) 'trustant-wsl-bootstrap.sh'
[System.IO.File]::WriteAllText($tmpScript, ($bootstrap -replace "`r`n", "`n"), (New-Object System.Text.UTF8Encoding($false)))

function ConvertTo-WslPath {
    param([string]$WindowsPath)
    # wslpath understands forward slashes; backslashes would be eaten as escapes
    # on the way through the wsl.exe command line.
    $p = $WindowsPath -replace '\\', '/'
    $converted = (& wsl.exe -d $Distro -u root -- wslpath -a "$p") | Select-Object -First 1
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($converted)) {
        Fail "could not map '$WindowsPath' into '$Distro' (wslpath failed)"
    }
    return ($converted -replace "`0", '').Trim()
}

Write-Step "Bootstrapping '$Distro' (user '$User', sudo, bash, systemd)"
$tmpScriptWsl = ConvertTo-WslPath $tmpScript
& wsl.exe -d $Distro -u root -- env "TRU_USER=$User" "TRU_PACKAGES=$BASE_PACKAGES" bash $tmpScriptWsl
if ($LASTEXITCODE -ne 0) { Fail "bootstrap failed inside '$Distro'" }
Remove-Item $tmpScript -ErrorAction SilentlyContinue
Write-Ok "user '$User' ready (sudo, /bin/bash)"

# --- restart so /etc/wsl.conf (and .wslconfig) take effect -------------------
if ($WroteWslConfig) {
    Write-Step 'Shutting down WSL to apply .wslconfig (this stops every WSL distribution)'
    & wsl.exe --shutdown | Out-Null
} else {
    Write-Step "Restarting '$Distro' to apply /etc/wsl.conf"
    & wsl.exe --terminate $Distro | Out-Null
}

# --- verify what start.sh's preflight is about to check ----------------------
Write-Step 'Waiting for systemd inside the distribution'
$systemdOk = $false
for ($i = 0; $i -lt 30; $i++) {
    & wsl.exe -d $Distro -u $User -- test -d /run/systemd/system
    if ($LASTEXITCODE -eq 0) { $systemdOk = $true; break }
    Start-Sleep -Seconds 2
}
if (-not $systemdOk) {
    Fail "systemd is not running in '$Distro'. Check that /etc/wsl.conf has [boot] systemd=true, then run: wsl --shutdown"
}
Write-Ok 'systemd is running'

& wsl.exe -d $Distro -u $User -- sudo -n true
if ($LASTEXITCODE -ne 0) { Fail "passwordless sudo does not work for '$User' in '$Distro'" }
Write-Ok "passwordless sudo works for '$User'"

# --- the mount ---------------------------------------------------------------
# No copy, no clone: the worktree stays on the Windows filesystem and the distro
# sees it through the automount, the same way the Lima VM sees the macOS folder
# through virtiofs. start.sh runs from there and writes back to Windows.
$RepoWsl = ConvertTo-WslPath $RepoWin
& wsl.exe -d $Distro -u $User -- test -f "$RepoWsl/start.sh"
if ($LASTEXITCODE -ne 0) {
    Fail "the mount does not expose start.sh at $RepoWsl - is drive $($RepoWin.Substring(0,2)) automounted in WSL?"
}
Write-Ok "mount: $RepoWin  ->  $RepoWsl"

# The closing hand-off, printed on both paths: how to get a shell in the distro
# and what to run there. `wsl -d <distro>` alone lands in the Linux home, so the
# form worth copying is the one that lands on the mount.
function Show-NextSteps {
    param([string]$Heading, [switch]$InVSCode)
    Write-Host ''
    Write-Host "=== $Heading ===" -ForegroundColor Green
    Write-Host "  distro:  $Distro (user $User, bash)"
    Write-Host "  mount:   $RepoWin  ->  $RepoWsl"
    Write-Host ''
    if ($InVSCode) {
        # The integrated terminal of a WSL window is already the mirrored user's
        # bash on the mount, so there is no wsl.exe hop to copy here.
        Write-Host '  In the VS Code terminal (bash in the distro, on the mount):' -ForegroundColor Cyan
        Write-Host ''
        Write-Host "      ./run.sh"
        Write-Host ''
    } else {
        Write-Host '  Enter the VM and start the dev loop:' -ForegroundColor Cyan
        Write-Host ''
        Write-Host "      wsl -d $Distro --cd $RepoWsl"
        Write-Host "      ./run.sh"
        Write-Host ''
        Write-Host "  (a bare 'wsl -d $Distro' lands in ~; cd $RepoWsl first)"
    }
    Write-Host "  stop:    .\start.ps1 -s        destroy: .\start.ps1 -k"
}

# --- final step: ./run.sh (default), VS Code (-v), or neither (-n) ------------
# The Windows counterpart of start.sh's finish block. start.sh cannot take this
# step itself: its native-Linux path - the one WSL takes - deliberately disables
# both ("if $NATIVE_LINUX; then OPEN_VSCODE=0; RUN_APP=0; fi"), because on a
# native host it has no VM to shell into. So the choice is made here instead,
# and start.sh stays Windows-unaware.

# Opens VS Code on the mounted worktree over the WSL remote authority. There is
# no Remote-SSH hop as there is on macOS: the sources are on the Windows
# filesystem and the distro reaches them through the automount, so the remote to
# attach is wsl+<distro>, with the LINUX path. The launch has to come from
# Windows rather than `code .` inside the distro, because /etc/wsl.conf sets
# appendWindowsPath=false and `code` is therefore not on the PATH in there.
function Open-VSCode {
    Write-Step "Opening VS Code on wsl+$Distro$RepoWsl"
    & code --remote "wsl+$Distro" $RepoWsl
    if ($LASTEXITCODE -ne 0) {
        Fail 'code --remote failed - is the WSL extension (ms-vscode-remote.remote-wsl) installed?'
    }
    Write-Ok 'VS Code opening - the first connect installs the WSL server, give it a moment'
}

if ($NoStart) {
    Show-NextSteps 'WSL environment ready (start.sh not run)'
    Write-Host ''
    Write-Warn "start.sh has NOT run yet - ./run.sh needs it first: wsl -d $Distro --cd $RepoWsl -- ./start.sh"
    # -NoStart wins over the final-step flags: there is nothing to finish yet.
    if ($VSCode) { Write-Warn '-NoStart given: VS Code was not opened' }
    Restore-Console
    exit 0
}

# --- run start.sh inside the distribution, on the mount ----------------------
# `bash ./start.sh` rather than `./start.sh`: the exec bit on a Windows-hosted
# file depends on the automount options, and this does not care.
Write-Step "Running ./start.sh in '$Distro' as '$User'"
# Both payloads are cached under dist/ on the Windows side of the mount, so this
# cost is paid once: a later -k / re-run reinstalls from those files. Say so, or
# the warning reads as a per-run price and invites destroying the cache.
Write-Warn 'start.sh downloads a ~4GB package plus a ~1.5GB ollama build and installs k3s inside the distribution'
Write-Warn 'both are cached in dist\ on this drive - later runs reuse them and do not re-download'
& wsl.exe -d $Distro -u $User --cd $RepoWsl -- bash ./start.sh
$startExit = $LASTEXITCODE
if ($startExit -ne 0) {
    Restore-Console
    Fail "start.sh failed inside '$Distro' (exit $startExit)"
}

# --- take the final step ------------------------------------------------------
if ($VSCode) {
    Open-VSCode
    Show-NextSteps 'Trustant development environment ready' -InVSCode
    Restore-Console
    exit 0
}

if ($NoRun) {
    Show-NextSteps 'Trustant development environment ready'
    Restore-Console
    exit 0
}

# The default finish, and the whole point of a start: the dev server up on
# :8910. It stays in the foreground - run.sh owns kubefwd and `air`, so Ctrl-C
# here is how you stop the dev server. `bash ./run.sh` rather than `./run.sh`
# for the same reason as start.sh above: the exec bit on a Windows-hosted file
# depends on the automount options, and this does not care. No arguments:
# run.sh takes none.
Write-Step "Running ./run.sh in '$Distro' as '$User' (Ctrl-C to stop)"
& wsl.exe -d $Distro -u $User --cd $RepoWsl -- bash ./run.sh
$runExit = $LASTEXITCODE

Show-NextSteps 'Trustant development environment ready'
Restore-Console
exit $runExit
