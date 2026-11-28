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

# mcp.ps1 - Windows twin of util/mcp.sh: connect Claude Code, Claude Desktop
# and Codex to the Trustant MCP server (spec/22-mcpserver.md).
#
#   .\util\mcp.ps1 add [-Claude] [-ClaudeDesktop] [-Codex] [-Url URL] [-Token T] [-DryRun]
#   .\util\mcp.ps1 remove | refresh | show
#
# The token lives in %APPDATA%\Trustant\token.mcp. It is generated when
# missing, pushed with PUT /api/v1/token, and only then written into the
# agents' config. Default URL: http://trustant.127.0.0.1.nip.io:8910 (the
# trudev WSL distro is reached through WSL localhost forwarding).

param(
    [Parameter(Position = 0)][ValidateSet('add', 'remove', 'refresh', 'show', 'help')][string]$Command = 'help',
    [switch]$Claude,
    [switch]$ClaudeDesktop,
    [switch]$Codex,
    [string]$Url = '',
    [string]$Token = '',
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

# Always run from the repo root, wherever this is invoked from.
Set-Location (Split-Path -Parent $PSScriptRoot)

function Ok($m)   { Write-Host "OK  $m" -ForegroundColor Green }
function Warn($m) { Write-Host "!!  $m" -ForegroundColor Yellow }
function Fail($m) { Write-Host "ERR $m" -ForegroundColor Red; exit 1 }

if ($Command -eq 'help') {
    Get-Content $PSCommandPath | Select-Object -Skip 15 -First 11 | ForEach-Object { $_ -replace '^# ?', '' }
    exit 0
}

$Name = 'trustant'
$SupportDir = Join-Path $env:APPDATA 'Trustant'
$TokenFile = Join-Path $SupportDir 'token.mcp'
$DesktopConfig = Join-Path $env:APPDATA 'Claude\claude_desktop_config.json'
$CodexHome = if ($env:CODEX_HOME) { $env:CODEX_HOME } else { Join-Path $env:USERPROFILE '.codex' }
$CodexConfig = Join-Path $CodexHome 'config.toml'
$CodexBegin = '# >>> trustant mcp >>>'
$CodexEnd = '# <<< trustant mcp <<<'
$Utf8 = New-Object System.Text.UTF8Encoding($false)

if ($Url) {
    $Base = $Url.TrimEnd('/') -replace '/mcp$', ''
} else {
    $ip = '127.0.0.1'
    $ipFile = Join-Path $SupportDir 'current.ip'
    if (Test-Path $ipFile) { $v = (Get-Content $ipFile -Raw).Trim(); if ($v) { $ip = $v } }
    $Base = "http://trustant.$ip.nip.io:8910"
}
$McpUrl = "$Base/mcp"

function Has-Claude  { [bool](Get-Command claude -ErrorAction SilentlyContinue) }
function Has-Desktop { Test-Path (Split-Path -Parent $DesktopConfig) }
function Has-Codex   { [bool](Get-Command codex -ErrorAction SilentlyContinue) -or (Test-Path $CodexHome) }

if (-not ($Claude -or $ClaudeDesktop -or $Codex)) {
    $Claude = Has-Claude; $ClaudeDesktop = Has-Desktop; $Codex = Has-Codex
    if ($Command -ne 'show' -and -not ($Claude -or $ClaudeDesktop -or $Codex)) {
        Fail 'no agent found (claude, Claude Desktop, codex); pass -Claude/-ClaudeDesktop/-Codex'
    }
}

function New-Token {
    $bytes = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    -join ($bytes | ForEach-Object { $_.ToString('x2') })
}

function Test-Token($t) { $t.Length -ge 32 -and $t.Length -le 256 -and $t -match '^[A-Za-z0-9_-]+$' }

function Read-Token { if (Test-Path $TokenFile) { (Get-Content $TokenFile -Raw).Trim() } else { '' } }

function Save-Token($t) {
    if ($DryRun) { Write-Host "[dry-run] write token to $TokenFile"; return }
    New-Item -ItemType Directory -Force -Path $SupportDir | Out-Null
    [System.IO.File]::WriteAllText($TokenFile, "$t`n", $Utf8)
}

function Get-Fingerprint($t) {
    $sha = [System.Security.Cryptography.SHA256]::Create()
    $hash = $sha.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($t))
    (-join ($hash | ForEach-Object { $_.ToString('x2') })).Substring(0, 8)
}

function Put-Token($new, $current) {
    if (-not $current) { $current = $new }
    if ($DryRun) { Write-Host "[dry-run] PUT $Base/api/v1/token (fingerprint $(Get-Fingerprint $new))"; return }
    try {
        Invoke-RestMethod -Method Put -Uri "$Base/api/v1/token" -TimeoutSec 15 `
            -Headers @{ Authorization = "Bearer $current" } -ContentType 'application/json' `
            -Body (@{ token = $new } | ConvertTo-Json) | Out-Null
    } catch {
        $code = $null
        if ($_.Exception.Response) { $code = [int]$_.Exception.Response.StatusCode }
        if ($code -eq 401) { Fail "Trustant refused the token: local login is enabled. Run 'refresh' from a machine holding the current token, or set it from the UI." }
        if ($code) { Fail "PUT $Base/api/v1/token failed: HTTP $code" }
        Fail "Trustant is not answering at $Base (is it running? use -Url)"
    }
    Ok "token pushed to $Base (fingerprint $(Get-Fingerprint $new))"
}

function Claude-Add($t) {
    if (-not (Has-Claude)) { Warn 'claude CLI not found, skipping Claude Code'; return }
    if ($DryRun) { Write-Host "[dry-run] claude mcp add --scope user --transport http $Name $McpUrl --header 'Authorization: Bearer <token>'"; return }
    & claude mcp remove --scope user $Name 2>$null | Out-Null
    & claude mcp add --scope user --transport http $Name $McpUrl --header "Authorization: Bearer $t"
    if ($LASTEXITCODE -ne 0) { Fail 'claude mcp add failed' }
    Ok "Claude Code: $Name -> $McpUrl"
}

function Claude-Remove {
    if (-not (Has-Claude)) { return }
    if ($DryRun) { Write-Host "[dry-run] claude mcp remove --scope user $Name"; return }
    & claude mcp remove --scope user $Name 2>$null | Out-Null
    Ok "Claude Code: $Name removed"
}

function Read-DesktopConfig {
    if ((Test-Path $DesktopConfig) -and (Get-Item $DesktopConfig).Length -gt 0) {
        return Get-Content $DesktopConfig -Raw | ConvertFrom-Json
    }
    return [pscustomobject]@{}
}

function Write-DesktopConfig($cfg) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $DesktopConfig) | Out-Null
    if (Test-Path $DesktopConfig) { Copy-Item $DesktopConfig "$DesktopConfig.bak" -Force }
    [System.IO.File]::WriteAllText($DesktopConfig, ($cfg | ConvertTo-Json -Depth 32), $Utf8)
}

# Claude Desktop only launches local commands: go through mcp-remote. The
# header is passed via env because launchers mangle a space inside an argument.
function Desktop-Add($t) {
    $npxArgs = @('-y', 'mcp-remote', $McpUrl, '--header', 'Authorization:${TRUSTANT_AUTH_HEADER}')
    if ($McpUrl.StartsWith('http://')) { $npxArgs += '--allow-http' }
    if ($DryRun) { Write-Host "[dry-run] set mcpServers.$Name in ${DesktopConfig}: npx $($npxArgs -join ' ')"; return }
    $cfg = Read-DesktopConfig
    if (-not $cfg.PSObject.Properties['mcpServers']) { $cfg | Add-Member -NotePropertyName mcpServers -NotePropertyValue ([pscustomobject]@{}) }
    $entry = [pscustomobject]@{ command = 'npx'; args = $npxArgs; env = [pscustomobject]@{ TRUSTANT_AUTH_HEADER = "Bearer $t" } }
    if ($cfg.mcpServers.PSObject.Properties[$Name]) { $cfg.mcpServers.PSObject.Properties.Remove($Name) }
    $cfg.mcpServers | Add-Member -NotePropertyName $Name -NotePropertyValue $entry
    Write-DesktopConfig $cfg
    Ok "Claude Desktop: $Name -> $McpUrl (restart Claude Desktop; backup in claude_desktop_config.json.bak)"
}

function Desktop-Remove {
    if (-not (Test-Path $DesktopConfig)) { return }
    if ($DryRun) { Write-Host "[dry-run] delete mcpServers.$Name from $DesktopConfig"; return }
    $cfg = Read-DesktopConfig
    if ($cfg.PSObject.Properties['mcpServers'] -and $cfg.mcpServers.PSObject.Properties[$Name]) {
        $cfg.mcpServers.PSObject.Properties.Remove($Name)
        Write-DesktopConfig $cfg
    }
    Ok "Claude Desktop: $Name removed"
}

function Codex-Strip {
    if (-not (Test-Path $CodexConfig)) { return }
    $out = New-Object System.Collections.Generic.List[string]
    $skip = $false
    foreach ($line in Get-Content $CodexConfig) {
        if ($line -eq $CodexBegin) { $skip = $true; continue }
        if ($line -eq $CodexEnd) { $skip = $false; continue }
        if (-not $skip) { $out.Add($line) }
    }
    [System.IO.File]::WriteAllText($CodexConfig, (($out -join "`n") + "`n"), $Utf8)
    if (Get-Command codex -ErrorAction SilentlyContinue) {
        & codex mcp remove $Name 2>$null | Out-Null
        # codex rewrites the file and can leave a stray marker behind.
        $clean = Get-Content $CodexConfig | Where-Object { $_ -ne $CodexBegin -and $_ -ne $CodexEnd }
        [System.IO.File]::WriteAllText($CodexConfig, (($clean -join "`n") + "`n"), $Utf8)
    }
}

function Codex-Add($t) {
    $block = "$CodexBegin`n[mcp_servers.$Name]`nurl = `"$McpUrl`"`nhttp_headers = { Authorization = `"Bearer $t`" }`n$CodexEnd`n"
    if ($DryRun) { Write-Host "[dry-run] write to ${CodexConfig}:"; Write-Host ($block -replace [regex]::Escape("Bearer $t"), 'Bearer <token>'); return }
    New-Item -ItemType Directory -Force -Path $CodexHome | Out-Null
    if (-not (Test-Path $CodexConfig)) { [System.IO.File]::WriteAllText($CodexConfig, '', $Utf8) }
    Codex-Strip
    $existing = [System.IO.File]::ReadAllText($CodexConfig)
    if ($existing.Trim().Length -eq 0) { $existing = '' } elseif (-not $existing.EndsWith("`n")) { $existing += "`n" }
    [System.IO.File]::WriteAllText($CodexConfig, $existing + $block, $Utf8)
    Ok "Codex: $Name -> $McpUrl"
}

function Codex-Remove {
    if ($DryRun) { Write-Host "[dry-run] remove $Name from $CodexConfig"; return }
    Codex-Strip
    Ok "Codex: $Name removed"
}

function Configure-All($t) {
    if ($Claude) { Claude-Add $t }
    if ($ClaudeDesktop) { Desktop-Add $t }
    if ($Codex) { Codex-Add $t }
}

switch ($Command) {
    'add' {
        $t = $Token
        if (-not $t) { $t = Read-Token }
        if (-not $t) { $t = New-Token; Ok "generated a new token in $TokenFile" }
        if (-not (Test-Token $t)) { Fail 'token must be 32-256 characters of [A-Za-z0-9_-]' }
        Save-Token $t
        Put-Token $t $t
        Configure-All $t
    }
    'refresh' {
        $old = Read-Token
        $t = if ($Token) { $Token } else { New-Token }
        if (-not (Test-Token $t)) { Fail 'token must be 32-256 characters of [A-Za-z0-9_-]' }
        Put-Token $t $old
        Save-Token $t
        Configure-All $t
    }
    'remove' {
        if ($Claude) { Claude-Remove }
        if ($ClaudeDesktop) { Desktop-Remove }
        if ($Codex) { Codex-Remove }
    }
    'show' {
        $t = Read-Token
        Write-Host "MCP URL:      $McpUrl"
        Write-Host "Token file:   $TokenFile"
        if ($t) { Write-Host "Fingerprint:  $(Get-Fingerprint $t) (local)" } else { Write-Host 'Fingerprint:  - (no local token: run add)' }
        try { $s = Invoke-RestMethod -Uri "$Base/api/v1/token" -TimeoutSec 5; Write-Host "Server:       $($s.fingerprint)" }
        catch { Write-Host "Server:       not answering at $Base" }
        if (Has-Claude) {
            & claude mcp get $Name 2>$null | Out-Null
            if ($LASTEXITCODE -eq 0) { Write-Host 'Claude Code:  configured' } else { Write-Host 'Claude Code:  not configured' }
        }
        if (Test-Path $DesktopConfig) {
            $cfg = Read-DesktopConfig
            if ($cfg.PSObject.Properties['mcpServers'] -and $cfg.mcpServers.PSObject.Properties[$Name]) { Write-Host 'Claude Desktop: configured' } else { Write-Host 'Claude Desktop: not configured' }
        }
        if (Test-Path $CodexConfig) {
            if (Select-String -Path $CodexConfig -Pattern "^\[mcp_servers\.$Name\]" -Quiet) { Write-Host 'Codex:        configured' } else { Write-Host 'Codex:        not configured' }
        }
    }
}
