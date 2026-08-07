# start.ps1 - bring up the Trustable development environment on a Windows host.
#
# The Windows counterpart of start.sh's macOS half: where start.sh provisions a
# Lima VM, this provisions a WSL2 Ubuntu-24.04 distribution, mirrors the current
# Windows user into it with passwordless sudo and bash as the login shell, and
# then runs THIS repo's ./start.sh inside it over the /mnt/<drive> mount - so
# the sources stay on Windows and only the Linux environment lives in WSL.
#
# start.sh then takes over on its native-Linux path (uname -s = Linux): it
# installs the Trustable .deb (k3s), ollama, kubefwd, gh, the host-rewrite proxy,
# writes the support files and runs ./setup.sh. Nothing in start.sh is
# Windows-aware; this script only has to hand it an Ubuntu that satisfies its
# preflight (Ubuntu/Debian, amd64/arm64, passwordless sudo, systemd).
#
#   .\start.ps1                 provision the distro and run ./start.sh in it
#   .\start.ps1 -NoStart        provision only, do not run ./start.sh
#   .\start.ps1 -Stop           terminate the distro (keeps it; re-run to restart)
#   .\start.ps1 -Destroy        unregister the distro - DELETES its filesystem
#   .\start.ps1 -User bob       mirror a different user name (default: $env:USERNAME)
#   .\start.ps1 -Distro trudev  use a different WSL distribution name
#
# NOTE: this file is deliberately pure ASCII. Windows PowerShell 5.1 reads a
# BOM-less .ps1 as ANSI, so any non-ASCII character here would be mangled.
#
#Requires -Version 5.1
param(
    [string]$Distro = 'Ubuntu-24.04',
    [string]$User = '',
    [switch]$NoStart,
    [switch]$Stop,
    [switch]$Destroy
)

$ErrorActionPreference = 'Stop'

# The Trustable VM sizing from start.sh's Lima config, reused for the WSL2 VM.
$VM_CPUS = 4
$VM_MEMORY = '8GB'
$VM_SWAP = '8GB'
# What start.sh's native path shells out to and the base image may not have.
# zstd and unzip are needed to unpack what the flow downloads (the Trustable
# .deb payload and the zipped release archives); iptables is what the package's
# postinst installs its firewall dropin with.
$BASE_PACKAGES = 'sudo curl ca-certificates git iproute2 iptables zstd unzip'

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

# --- lifecycle: -Stop / -Destroy --------------------------------------------
# These mirror ./start.sh -s and ./start.sh -k. They only ever touch the WSL
# distribution, never the Windows host.
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
    # written below already carries the values Trustable wants.
    if ((Get-Content $WslConfig -Raw) -match 'trustable-app/(start|setup)\.ps1') {
        Write-Ok ".wslconfig already written by start.ps1 ($WslConfig)"
    } else {
        Write-Ok ".wslconfig already present at $WslConfig (left untouched)"
        Write-Warn "Trustable wants at least: memory=$VM_MEMORY, processors=$VM_CPUS, swap=$VM_SWAP"
    }
} else {
    Write-Step "Writing $WslConfig"
    $cfg = @"
# Written by trustable-app/start.ps1. Trustable runs a full k3s stack inside
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
if (Test-DistroExists $Distro) {
    Write-Ok "WSL distribution '$Distro' already exists"
} else {
    Write-Step "Installing WSL distribution '$Distro' (this downloads the Ubuntu image)"
    # --no-launch keeps the interactive first-run account wizard out of the way:
    # the account is created below, with the name and sudo rights we want.
    & wsl.exe --install --distribution $Distro --no-launch
    if ($LASTEXITCODE -ne 0) {
        Fail "wsl --install --distribution $Distro failed. Check the available names with: wsl --list --online"
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

$tmpScript = Join-Path ([System.IO.Path]::GetTempPath()) 'trustable-wsl-bootstrap.sh'
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
    param([string]$Heading)
    Write-Host ''
    Write-Host "=== $Heading ===" -ForegroundColor Green
    Write-Host "  distro:  $Distro (user $User, bash)"
    Write-Host "  mount:   $RepoWin  ->  $RepoWsl"
    Write-Host ''
    Write-Host '  Enter the VM and start the dev loop:' -ForegroundColor Cyan
    Write-Host ''
    Write-Host "      wsl -d $Distro --cd $RepoWsl"
    Write-Host "      ./run.sh"
    Write-Host ''
    Write-Host "  (a bare 'wsl -d $Distro' lands in ~; cd $RepoWsl first)"
    Write-Host "  stop:    .\start.ps1 -Stop      destroy: .\start.ps1 -Destroy"
}

if ($NoStart) {
    Show-NextSteps 'WSL environment ready (start.sh not run)'
    Write-Host ''
    Write-Warn "start.sh has NOT run yet - ./run.sh needs it first: wsl -d $Distro --cd $RepoWsl -- ./start.sh"
    Restore-Console
    exit 0
}

# --- run start.sh inside the distribution, on the mount ----------------------
# `bash ./start.sh` rather than `./start.sh`: the exec bit on a Windows-hosted
# file depends on the automount options, and this does not care.
Write-Step "Running ./start.sh in '$Distro' as '$User'"
Write-Warn 'start.sh downloads a ~3.6GB package and installs k3s inside the distribution'
& wsl.exe -d $Distro -u $User --cd $RepoWsl -- bash ./start.sh
$startExit = $LASTEXITCODE
Restore-Console
if ($startExit -ne 0) { Fail "start.sh failed inside '$Distro' (exit $startExit)" }

Show-NextSteps 'Trustable development environment ready'
exit 0
