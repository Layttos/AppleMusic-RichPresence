<#
.SYNOPSIS
    Builds applemusic-rp for Windows and packages it as an installer.

.DESCRIPTION
    Four steps, each of which can be run on its own through the Makefile:

      1. rasterise internal/ui/icon.svg into the PNG and ICO the program embeds
      2. write the executable's own resources — icon, manifest, version — into
         a .syso the Go linker picks up
      3. compile applemusic-rp.exe as a GUI binary, so no console window trails
         behind the tray icon
      4. compile packaging/windows/installer.iss with Inno Setup

    Everything but the last step needs only the Go toolchain. Inno Setup can be
    installed with:  winget install JRSoftware.InnoSetup

.PARAMETER Version
    Version to stamp and to name the installer with. Defaults to the most recent
    git tag, or 1.0.0 when the repository has none.

.PARAMETER SkipInstaller
    Build the executable but stop before Inno Setup.
#>
[CmdletBinding()]
param(
    [string]$Version,
    [switch]$SkipInstaller
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = $PSScriptRoot
$dist = Join-Path $repo 'dist'

function Invoke-Step {
    param([string]$Name, [scriptblock]$Body)
    Write-Host ""
    Write-Host "==> $Name" -ForegroundColor Cyan

    # Go and Inno Setup both report progress on standard error. Windows
    # PowerShell turns those lines into errors whenever the output is
    # redirected, so the exit code is the only thing worth trusting here.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & $Body
    } finally {
        $ErrorActionPreference = $previous
    }

    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE"
    }
}

# --- version ----------------------------------------------------------------

if (-not $Version) {
    # `git tag` rather than `git describe`, which writes to standard error when
    # a repository has no tags at all.
    $tag = $null
    try {
        $tag = & git -C $repo tag --list --sort=-v:refname | Select-Object -First 1
    } catch {
        $tag = $null
    }
    if ($tag) {
        $Version = $tag.Trim().TrimStart('v')
    } else {
        $Version = '1.0.0'
    }
    $global:LASTEXITCODE = 0
}

# Inno Setup and the PE header both want a plain numeric version, so a tag like
# 1.2.0-rc1 is reduced to 1.2.0 for them while the full string stays on show.
$numeric = ($Version -replace '[^0-9.].*$', '').Trim('.')
if (-not $numeric) { $numeric = '0.0.0' }
$parts = @($numeric.Split('.') | Where-Object { $_ -ne '' })
while ($parts.Count -lt 3) { $parts += '0' }
$numeric = ($parts[0..2] -join '.')

Write-Host "Apple Music Rich Presence $Version (file version $numeric)" -ForegroundColor Green

if (-not (Test-Path $dist)) {
    New-Item -ItemType Directory -Path $dist | Out-Null
}

# --- 1. icons ---------------------------------------------------------------

Invoke-Step 'Rasterising the icon' {
    & go run ./tools/genicons internal/ui
}

# --- 2. executable resources ------------------------------------------------

$syso = Join-Path $repo 'resource_windows_amd64.syso'

Invoke-Step 'Writing the executable resources' {
    & go run ./tools/gensyso `
        -ico internal/ui/icon.ico `
        -manifest packaging/windows/app.manifest `
        -o $syso `
        -version $numeric `
        -company 'Layttos' `
        -product 'Apple Music Rich Presence' `
        -description 'Shows the track playing in Apple Music on your Discord profile' `
        -copyright 'MIT licence' `
        -filename 'applemusic-rp.exe'
}

# --- 3. the executable ------------------------------------------------------

# -H windowsgui suppresses the console window, which a tray application must not
# leave sitting behind it. -s -w drop the symbol table and DWARF data, which
# roughly halves the binary and costs nothing at runtime.
Invoke-Step 'Compiling applemusic-rp.exe' {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    & go build -ldflags "-s -w -X main.version=$Version -H windowsgui" -o (Join-Path $dist 'applemusic-rp.exe') .
}

$exe = Join-Path $dist 'applemusic-rp.exe'
Write-Host ("    {0}  ({1:N1} MB)" -f $exe, ((Get-Item $exe).Length / 1MB))

if ($SkipInstaller) {
    Write-Host ""
    Write-Host "Stopped before the installer, as asked." -ForegroundColor Yellow
    return
}

# --- 4. the installer -------------------------------------------------------

$isccCandidates = @(
    (Join-Path $env:LOCALAPPDATA 'Programs\Inno Setup 6\ISCC.exe'),
    (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe'),
    (Join-Path $env:ProgramFiles 'Inno Setup 6\ISCC.exe')
)
$iscc = $isccCandidates | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $iscc) {
    $command = Get-Command 'ISCC.exe' -ErrorAction SilentlyContinue
    if ($command) { $iscc = $command.Source }
}
if (-not $iscc) {
    throw "Inno Setup was not found. Install it with: winget install JRSoftware.InnoSetup"
}

Invoke-Step 'Building the installer' {
    & $iscc `
        "/DAppVersion=$Version" `
        "/DNumericVersion=$numeric" `
        "/DBuildDir=$dist" `
        "/DRepoDir=$repo" `
        (Join-Path $repo 'packaging\windows\installer.iss')
}

$setup = Join-Path $dist "AppleMusicRP-Setup-$Version.exe"
Write-Host ""
Write-Host "Done." -ForegroundColor Green
Write-Host ("    {0}  ({1:N1} MB)" -f $setup, ((Get-Item $setup).Length / 1MB))
Write-Host "    The uninstaller is written into the install folder and listed in Installed apps."
