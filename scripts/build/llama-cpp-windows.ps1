#!/usr/bin/env pwsh

# PowerShell entry point for the native windows/amd64 llama-cpp backend build,
# driven from the Makefile on native Windows hosts (and on Windows CI via the
# msys2 shell, where powershell.exe is still reachable):
#
#     make backends/llama-cpp-windows
#
# GNU make on Windows runs recipe lines through sh (Git for Windows), where
# /ucrt64/bin does not resolve and the mingw toolchain does not exist. This
# script hands the build to a real MSYS2 UCRT64 bash instead. It locates the
# MSYS2 install (MSYS2_ROOT, the standard install dirs, or any PATH entry that
# is really an MSYS2 root), installs MSYS2 + the toolchain on first use -- only
# after asking for confirmation -- and aborts with a non-zero status when the
# user declines, so the Makefile recipe stops before the OCI-image packaging
# step. The sh script's own re-exec dispatch (scripts/build/llama-cpp-windows.sh)
# is bypassed deliberately: starting under the real MSYS2 bash makes the
# /ucrt64/bin check take the direct branch.

[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

# scripts/build -> repo root
$RepoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))

# Toolchain package set, mirroring the CI setup in
# .github/workflows/backend_build_windows.yml. A fresh winget MSYS2 is a bare
# base system: pacman must install these before the script's first configure.
$ToolchainPackages = @(
  'git', 'make', 'cmake', 'mingw-w64-ucrt-x86_64-cmake', 'ninja', 'pkg-config',
  'perl', 'patch', 'unzip', 'curl',
  'mingw-w64-ucrt-x86_64-gcc', 'mingw-w64-ucrt-x86_64-gcc-libs',
  'mingw-w64-ucrt-x86_64-binutils', 'mingw-w64-ucrt-x86_64-ccache',
  'mingw-w64-ucrt-x86_64-vulkan-headers', 'mingw-w64-ucrt-x86_64-vulkan-loader',
  'mingw-w64-ucrt-x86_64-spirv-headers', 'mingw-w64-x86_64-shaderc'
)

function ConvertTo-WindowsPath {
  param([Parameter(Mandatory = $true)][string]$Path)
  $Path = $Path.Trim().Trim('"')
  # MSYS-style /c/Program Files/... (as exported by the Makefile's sh shell)
  # has no drive letter for .NET; rewrite to C:\Program Files\...
  if ($Path -match '^/([a-zA-Z])/(.+)$') {
    $Path = "$($matches[1]):\$($matches[2])"
  }
  $Path = $Path -replace '/', '\'
  return $Path.TrimEnd('\')
}

function Test-PathSafe {
  param([Parameter(Mandatory = $true)][string]$Path)
  try { return (Test-Path -LiteralPath $Path) }
  catch { return $false }
}

function Find-Msys2Root {
  # A bare usr\bin\bash.exe is not enough of a discriminator: Git for Windows
  # ships one too, and its "root" has no pacman.conf. Requiring etc/pacman.conf
  # keeps the PATH scan from dispatching the build into Git bash.
  $candidates = New-Object System.Collections.Generic.List[string]
  if ($env:MSYS2_ROOT) { $candidates.Add((ConvertTo-WindowsPath $env:MSYS2_ROOT)) }
  foreach ($p in @('C:\msys64', 'C:\Program Files\MSYS2')) { $candidates.Add($p) }
  foreach ($dir in ($env:PATH -split ';')) {
    if ([string]::IsNullOrWhiteSpace($dir)) { continue }
    $nd = ConvertTo-WindowsPath $dir
    if (Test-PathSafe ([System.IO.Path]::Combine($nd, 'bash.exe'))) {
      # usr\bin\bash.exe -> root = two levels up from the PATH entry.
      $candidates.Add([System.IO.Path]::GetFullPath([System.IO.Path]::Combine($nd, '..', '..')))
    }
  }
  foreach ($c in $candidates) {
    if ((Test-PathSafe (Join-Path $c 'usr\bin\bash.exe')) -and (Test-PathSafe (Join-Path $c 'etc\pacman.conf'))) {
      return $c
    }
  }
  return $null
}

function Invoke-BashInMsys {
  param(
    [Parameter(Mandatory = $true)][string]$MsysRoot,
    [Parameter(Mandatory = $true)][string]$Command
  )
  # MSYSTEM selects the UCRT64 toolchain; a non-login invocation keeps the
  # inherited Windows PATH (Go, Git) and the working directory, mirroring the
  # sh script's own re-exec behavior.
  $env:MSYSTEM = 'UCRT64'
  $env:MSYS2_PATH_TYPE = 'inherit'
  & (Join-Path $MsysRoot 'usr\bin\bash.exe') -lc $Command
  $code = $LASTEXITCODE
  if ($code -ne 0) {
    Write-Host "ERROR: command failed under MSYS2 (exit $code)" -ForegroundColor Red
    exit $code
  }
}

function Test-ToolchainComplete {
  param([Parameter(Mandatory = $true)][string]$MsysRoot)
  $pkgs = $ToolchainPackages -join ' '
  $env:MSYSTEM = 'UCRT64'
  $env:MSYS2_PATH_TYPE = 'inherit'
  & (Join-Path $MsysRoot 'usr\bin\bash.exe') -lc "pacman -Q $pkgs >/dev/null 2>&1"
  return ($LASTEXITCODE -eq 0)
}

function Install-Toolchain {
  param([Parameter(Mandatory = $true)][string]$MsysRoot)
  $pkgs = $ToolchainPackages -join ' '
  Invoke-BashInMsys $MsysRoot "pacman -Sy --needed --noconfirm $pkgs"
  if (-not (Test-ToolchainComplete $MsysRoot)) {
    Write-Host "ERROR: MSYS2 toolchain install finished but packages are still missing" -ForegroundColor Red
    exit 1
  }
}

function Install-Msys2 {
  $answer = Read-Host 'MSYS2 was not found. Install MSYS2 and the mingw-w64 UCRT64 toolchain now (hundreds of MB)? [y/N]'
  if ($answer -notmatch '^[Yy]$') {
    Write-Host "Aborted. Install MSYS2 manually (winget install --id MSYS2.MSYS2) and retry 'make backends/llama-cpp-windows'."
    exit 1
  }
  if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
    Write-Host 'ERROR: winget is not available; install MSYS2 manually from https://www.msys2.org/ and retry.' -ForegroundColor Red
    exit 1
  }
  Write-Host '==> Installing MSYS2 via winget ...'
  winget install --id MSYS2.MSYS2 --exact --accept-package-agreements --accept-source-agreements --disable-interactivity
  if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: winget install of MSYS2 failed (exit $LASTEXITCODE)" -ForegroundColor Red
    exit 1
  }
  $root = Find-Msys2Root
  if (-not $root) {
    Write-Host 'ERROR: MSYS2 was installed but its root could not be located; install it manually and retry.' -ForegroundColor Red
    exit 1
  }
  return $root
}

function Invoke-Build {
  param([Parameter(Mandatory = $true)][string]$MsysRoot)
  # forward-slash form keeps MSYS2 bash from reading the Windows backslashes as
  # escape sequences.
  $repo = $RepoRoot -replace '\\', '/'
  Invoke-BashInMsys $MsysRoot "cd '$repo' && exec ./scripts/build/llama-cpp-windows.sh"
}

$MsysRoot = Find-Msys2Root
if (-not $MsysRoot) {
  $MsysRoot = Install-Msys2
}

if (-not (Test-ToolchainComplete $MsysRoot)) {
  $answer = Read-Host 'MSYS2 found but the mingw-w64 UCRT64 toolchain is missing. Download and install the build packages (hundreds of MB)? [y/N]'
  if ($answer -notmatch '^[Yy]$') {
    Write-Host 'Aborted. Install the toolchain packages manually and retry.'
    exit 1
  }
  Install-Toolchain $MsysRoot
}

Invoke-Build $MsysRoot