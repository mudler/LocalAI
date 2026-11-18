<#
.SYNOPSIS
  LocalAI installer for Windows (amd64).

.DESCRIPTION
  Downloads the Windows release binary, checks it against the checksums
  published with the release, and installs it for the current user. It needs
  no administrator rights. Running it again upgrades the install.

  Safer form, which lets you read the script first:

    irm https://localai.io/install.ps1 -OutFile install.ps1
    Get-Content .\install.ps1
    powershell -ExecutionPolicy Bypass -File .\install.ps1

  One-line form:

    irm https://localai.io/install.ps1 | iex

  The one-line form cannot take parameters. Set environment variables first
  instead, for example:

    $env:LOCALAI_VERSION = 'v4.7.1'; irm https://localai.io/install.ps1 | iex

.PARAMETER Version
  Release to install, for example v4.7.1. The default is the latest release.
  Environment variable: LOCALAI_VERSION.

.PARAMETER InstallDir
  Folder for local-ai.exe. The default is %LOCALAPPDATA%\LocalAI\bin.
  Environment variable: INSTALL_DIR.

.PARAMETER NoPath
  Do not add the install folder to the user PATH.
  Environment variable: LOCALAI_NO_PATH=1.

.PARAMETER InstallBackend
  After the install, download the llama-cpp backend with
  "local-ai backends install llama-cpp" into the backends folder of the
  LocalAI data folder, where "local-ai run" looks for it. Without this
  switch LocalAI downloads a backend the first time a model needs one.
  Environment variable: LOCALAI_INSTALL_BACKEND=1.

.PARAMETER Force
  Install again even when the same build is already present.
  Environment variable: LOCALAI_FORCE=1.

.PARAMETER Uninstall
  Remove local-ai.exe and its PATH entry. Models, backends and settings in
  your data folder (%LOCALAPPDATA%\LocalAI, or LOCALAI_BASE_PATH when it is
  set) are not touched.
  Environment variable: LOCALAI_UNINSTALL=1.

.NOTES
  Works in Windows PowerShell 5.1 and PowerShell 7 or later.
  GITHUB_TOKEN is optional and only used to avoid GitHub API rate limits.
#>
[CmdletBinding()]
param(
    [string]$Version = $env:LOCALAI_VERSION,
    [string]$InstallDir = $env:INSTALL_DIR,
    [switch]$NoPath,
    [switch]$InstallBackend,
    [switch]$Force,
    [switch]$Uninstall
)

# Never call "exit" in this script: with "irm ... | iex" it would close the
# window of the user. Errors are thrown, and the entry function reports them.

function Install-LocalAI {
    [CmdletBinding()]
    param(
        [string]$Version,
        [string]$InstallDir,
        [bool]$NoPath,
        [bool]$InstallBackend,
        [bool]$Force,
        [bool]$Uninstall
    )

    Set-StrictMode -Version 2.0
    $ErrorActionPreference = 'Stop'

    $repo = 'mudler/LocalAI'
    $api = "https://api.github.com/repos/$repo/releases"
    $download = "https://github.com/$repo/releases/download"
    $binName = 'local-ai.exe'

    function Write-Step([string]$Label, [string]$Text) {
        Write-Host ('  {0,-13} {1}' -f $Label, $Text)
    }

    function Test-SamePath([string]$A, [string]$B) {
        $x = $A.TrimEnd('\', '/')
        $y = $B.TrimEnd('\', '/')
        return [string]::Equals($x, $y, [StringComparison]::OrdinalIgnoreCase)
    }

    # --- user PATH ----------------------------------------------------------
    # The user PATH lives in HKCU\Environment. It is read and written through
    # the registry so that a value of type REG_EXPAND_SZ, with entries such as
    # %USERPROFILE%\bin, keeps its type and its unexpanded entries.

    function Get-UserPathEntry {
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
        if ($null -eq $key) { return @() }
        try {
            $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        } finally {
            $key.Close()
        }
        return @($raw.Split(';') | Where-Object { $_ -ne '' })
    }

    function Set-UserPathEntry([string[]]$Entries) {
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
        try {
            $key.SetValue('Path', ($Entries -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
        } finally {
            $key.Close()
        }
        # Writing the registry does not tell running programs about the change.
        # Setting and clearing a throwaway user variable through .NET makes it
        # broadcast WM_SETTINGCHANGE, so new terminals see the new PATH.
        try {
            [Environment]::SetEnvironmentVariable('LOCALAI_PATH_REFRESH', '1', 'User')
            [Environment]::SetEnvironmentVariable('LOCALAI_PATH_REFRESH', $null, 'User')
        } catch {
            Write-Verbose "could not broadcast the PATH change: $($_.Exception.Message)"
        }
    }

    function Add-UserPathEntry([string]$Dir) {
        $entries = @(Get-UserPathEntry)
        foreach ($e in $entries) {
            if (Test-SamePath ([Environment]::ExpandEnvironmentVariables($e)) $Dir) { return $false }
        }
        Set-UserPathEntry ($entries + $Dir)
        return $true
    }

    function Remove-UserPathEntry([string]$Dir) {
        $entries = @(Get-UserPathEntry)
        $kept = @($entries | Where-Object {
                -not (Test-SamePath ([Environment]::ExpandEnvironmentVariables($_)) $Dir)
            })
        if ($kept.Count -eq $entries.Count) { return $false }
        Set-UserPathEntry $kept
        return $true
    }

    # --- platform -----------------------------------------------------------

    function Get-OsArchitecture {
        $arch = $null
        try {
            $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
        } catch {
            $arch = $null
        }
        if ([string]::IsNullOrEmpty($arch)) {
            # Windows PowerShell on an old .NET Framework has no
            # RuntimeInformation. A 32-bit process on a 64-bit system reports
            # the real architecture in PROCESSOR_ARCHITEW6432.
            if ($env:PROCESSOR_ARCHITEW6432) {
                $arch = $env:PROCESSOR_ARCHITEW6432
            } else {
                $arch = $env:PROCESSOR_ARCHITECTURE
            }
        }
        return $arch
    }

    # --- fetching -----------------------------------------------------------

    function Get-RequestHeader {
        $h = @{ 'User-Agent' = 'localai-install-ps1' }
        if ($env:GITHUB_TOKEN) { $h['Authorization'] = "Bearer $($env:GITHUB_TOKEN)" }
        return $h
    }

    function Save-File([string]$Url, [string]$Dest) {
        $lastError = $null
        for ($i = 1; $i -le 3; $i++) {
            try {
                Invoke-WebRequest -Uri $Url -OutFile $Dest -UseBasicParsing -Headers (Get-RequestHeader)
                return
            } catch {
                $lastError = $_
                Start-Sleep -Seconds $i
            }
        }
        throw $lastError
    }

    # --- start --------------------------------------------------------------

    if ($PSVersionTable.PSVersion.Major -lt 5) {
        throw 'This installer needs Windows PowerShell 5.1 or PowerShell 7 or later.'
    }
    if ($PSVersionTable.PSVersion.Major -ge 6 -and -not $IsWindows) {
        throw 'This installer is for Windows. On Linux and macOS use: curl -sSL https://localai.io/install.sh | sh'
    }

    if ([string]::IsNullOrWhiteSpace($InstallDir)) {
        if ([string]::IsNullOrEmpty($env:LOCALAPPDATA)) {
            throw 'The LOCALAPPDATA variable is not set. Pass -InstallDir with a folder you can write to.'
        }
        $InstallDir = Join-Path $env:LOCALAPPDATA 'LocalAI\bin'
    }
    $InstallDir = [IO.Path]::GetFullPath($InstallDir)
    $target = Join-Path $InstallDir $binName

    Write-Host 'LocalAI installer'

    # The folder where local-ai.exe keeps models, backends, data and settings
    # by default: the same rule as the server (LOCALAI_BASE_PATH, else the
    # LocalAI folder in the local application data folder).
    $dataDir = $env:LOCALAI_BASE_PATH
    if ([string]::IsNullOrWhiteSpace($dataDir)) {
        $local = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
        if ([string]::IsNullOrEmpty($local)) { $local = $env:LOCALAPPDATA }
        $dataDir = if ([string]::IsNullOrEmpty($local)) { '' } else { Join-Path $local 'LocalAI' }
    }

    # --- uninstall ----------------------------------------------------------

    if ($Uninstall) {
        $removed = $false
        foreach ($name in @($binName, "$binName.new", "$binName.old")) {
            $p = Join-Path $InstallDir $name
            if (Test-Path -LiteralPath $p) {
                try {
                    Remove-Item -LiteralPath $p -Force
                } catch {
                    throw "Could not remove $p. Stop LocalAI if it is running, then run this again. $($_.Exception.Message)"
                }
                $removed = $true
            }
        }
        if ($removed) { Write-Step 'removed' $target } else { Write-Step 'not found' $target }
        if ((Test-Path -LiteralPath $InstallDir) -and -not (Get-ChildItem -LiteralPath $InstallDir -Force | Select-Object -First 1)) {
            Remove-Item -LiteralPath $InstallDir -Force
        }
        if (Remove-UserPathEntry $InstallDir) { Write-Step 'user PATH' "removed $InstallDir" }
        Write-Host ''
        if ($dataDir) {
            Write-Host "Your models, backends and settings were not removed. They are in $dataDir"
        } else {
            Write-Host 'Your models, backends and settings were not removed.'
        }
        return
    }

    # --- platform check -----------------------------------------------------

    $arch = Get-OsArchitecture
    switch -Regex ($arch) {
        '^(X64|AMD64)$' { }
        '^ARM64$' {
            throw 'LocalAI has no Windows arm64 build, because there is no arm64 backend image. Use WSL2 or a container: docker run -p 8080:8080 localai/localai:latest'
        }
        default {
            throw "Unsupported CPU architecture: $arch. LocalAI publishes a Windows build for amd64 only."
        }
    }
    Write-Step 'platform' 'windows/amd64'

    # TLS 1.2 or later. Windows PowerShell 5.1 can default to older versions.
    try {
        $proto = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        try { $proto = $proto -bor [Net.SecurityProtocolType]12288 } catch { Write-Verbose 'TLS 1.3 is not available here' }
        [Net.ServicePointManager]::SecurityProtocol = $proto
    } catch {
        Write-Verbose "could not set the TLS version: $($_.Exception.Message)"
    }

    # --- version ------------------------------------------------------------

    if ([string]::IsNullOrWhiteSpace($Version)) {
        try {
            $latest = Invoke-RestMethod -Uri "$api/latest" -UseBasicParsing -Headers (Get-RequestHeader)
            $Version = [string]$latest.tag_name
        } catch {
            throw "Could not work out the latest LocalAI release. GitHub may be rate limiting you. Pin a version instead, for example: -Version v4.7.1. $($_.Exception.Message)"
        }
    }
    $Version = $Version.Trim()
    if (-not $Version.StartsWith('v')) { $Version = "v$Version" }
    if ($Version -notmatch '^v\d+\.\d+\.\d+([-.+][0-9A-Za-z.-]+)?$') {
        throw "Not a release version: $Version. Use a tag such as v4.7.1."
    }
    Write-Step 'release' $Version
    Write-Step 'install to' $InstallDir

    $asset = "local-ai-$Version-windows-amd64.exe"
    $sums = "LocalAI-$Version-checksums.txt"

    $work = Join-Path ([IO.Path]::GetTempPath()) ('localai-install-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $work -Force | Out-Null
    $savedProgress = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'   # Windows PowerShell 5.1 is very slow with the progress bar
    try {
        # --- checksum list --------------------------------------------------
        $sumsFile = Join-Path $work $sums
        try {
            Save-File "$download/$Version/$sums" $sumsFile
        } catch {
            throw "Could not download $download/$Version/$sums. Check that the release exists: https://github.com/$repo/releases/tag/$Version. $($_.Exception.Message)"
        }
        $expected = $null
        foreach ($line in (Get-Content -LiteralPath $sumsFile)) {
            $parts = $line.Trim() -split '\s+'
            if ($parts.Count -ge 2 -and ($parts[1] -eq $asset -or $parts[1] -eq "*$asset")) {
                $expected = $parts[0].ToLowerInvariant()
                break
            }
        }
        if (-not $expected) {
            throw "The checksum file of $Version has no entry for $asset. This release may have no Windows build. Nothing was installed."
        }

        # --- already installed? ---------------------------------------------
        $alreadyCurrent = $false
        if ((Test-Path -LiteralPath $target) -and -not $Force) {
            $current = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($current -eq $expected) { $alreadyCurrent = $true }
        }

        if ($alreadyCurrent) {
            Write-Step 'already at' "$Version, nothing to download"
        } else {
            # --- download and verify ----------------------------------------
            Write-Step 'downloading' $asset
            $tmpExe = Join-Path $work $asset
            try {
                Save-File "$download/$Version/$asset" $tmpExe
            } catch {
                throw "Could not download $download/$Version/$asset. Check that the release lists that file. $($_.Exception.Message)"
            }
            if (-not (Test-Path -LiteralPath $tmpExe) -or (Get-Item -LiteralPath $tmpExe).Length -eq 0) {
                throw 'The downloaded file is empty. Try again.'
            }
            $actual = (Get-FileHash -LiteralPath $tmpExe -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($actual -ne $expected) {
                throw "Checksum mismatch for $asset. Expected $expected, got $actual. The download was corrupted or changed, so nothing was installed."
            }
            Write-Step 'checksum' 'ok'

            # Report the Authenticode signature. Releases are signed only when
            # the project has a signing certificate, so an unsigned file is not
            # an error. The checksum above is the integrity check.
            try {
                $sig = Get-AuthenticodeSignature -LiteralPath $tmpExe
                if ($sig.Status -eq 'Valid') {
                    Write-Step 'signature' "valid, $($sig.SignerCertificate.Subject)"
                } elseif ($sig.Status -eq 'NotSigned') {
                    Write-Step 'signature' 'none, this release is not signed'
                } else {
                    throw "The signature of $asset is $($sig.Status): $($sig.StatusMessage). Nothing was installed."
                }
            } catch [System.Management.Automation.CommandNotFoundException] {
                Write-Verbose 'Get-AuthenticodeSignature is not available'
            }

            # --- install ----------------------------------------------------
            New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
            $staged = "$target.new"
            $backup = "$target.old"
            Copy-Item -LiteralPath $tmpExe -Destination $staged -Force
            $hadOld = Test-Path -LiteralPath $target
            if ($hadOld) {
                # Windows cannot overwrite a running program, but it can
                # rename it. So a running local-ai keeps working until it
                # restarts, and a failed swap is rolled back.
                if (Test-Path -LiteralPath $backup) { Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue }
                Move-Item -LiteralPath $target -Destination $backup -Force
            }
            try {
                Move-Item -LiteralPath $staged -Destination $target -Force
            } catch {
                if ($hadOld -and (Test-Path -LiteralPath $backup)) {
                    Move-Item -LiteralPath $backup -Destination $target -Force
                }
                throw "Could not write $target. $($_.Exception.Message)"
            }
            if ($hadOld) { Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue }
            Write-Step 'installed' $target

            $running = @(Get-Process -Name 'local-ai' -ErrorAction SilentlyContinue)
            if ($running.Count -gt 0) {
                Write-Host '  A local-ai process is running. It keeps the old version until you restart it.'
            }
        }
    } finally {
        $ProgressPreference = $savedProgress
        Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
    }

    # --- PATH ---------------------------------------------------------------

    $pathChanged = $false
    if (-not $NoPath) {
        if (Add-UserPathEntry $InstallDir) {
            $pathChanged = $true
            Write-Step 'user PATH' "added $InstallDir"
        } else {
            Write-Step 'user PATH' 'already set'
        }
        $sessionHas = $false
        foreach ($e in ($env:Path -split ';')) {
            if ($e -ne '' -and (Test-SamePath $e $InstallDir)) { $sessionHas = $true }
        }
        if (-not $sessionHas) { $env:Path = "$env:Path;$InstallDir" }
    }

    # --- backend ------------------------------------------------------------

    if ($InstallBackend) {
        Write-Step 'backend' 'llama-cpp (this can take a few minutes)'
        # Run from the data folder, not from the folder this script runs in:
        # a release that keeps its files in the current folder would otherwise
        # put the backend where "local-ai run" never looks, and a current folder
        # with LocalAI files of an earlier test install would get it too.
        if ($dataDir) {
            [void][IO.Directory]::CreateDirectory($dataDir)
            Push-Location -LiteralPath $dataDir
        }
        try {
            & $target backends install llama-cpp
        } finally {
            if ($dataDir) { Pop-Location }
        }
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "The backend install failed (exit code $LASTEXITCODE). LocalAI installs it when a model first needs it, or run: local-ai backends install llama-cpp"
        }
    }

    Write-Host ''
    Write-Host 'Next steps'
    if ($NoPath) {
        Write-Host "  The install folder was not added to your PATH. Run LocalAI with: & '$target'"
    } elseif ($pathChanged) {
        Write-Host '  Open a new terminal so that it sees the new PATH. This terminal already does.'
    }
    Write-Host '  Start the server:'
    Write-Host '    local-ai run'
    Write-Host '  Then open http://localhost:8080 and install a model from there.'
    if ($dataDir) { Write-Host "  Models, backends and settings go to $dataDir" }
    Write-Host '  To use another folder, set LOCALAI_BASE_PATH. See https://localai.io/start/install/windows/'
    if (-not $InstallBackend) {
        Write-Host '  The llama-cpp backend downloads itself the first time a model needs it.'
        Write-Host '  To download it now: local-ai backends install llama-cpp'
    }
    Write-Host '  Documentation: https://localai.io/docs/'
}

if ($env:LOCALAI_NO_PATH -eq '1') { $NoPath = [switch]$true }
if ($env:LOCALAI_INSTALL_BACKEND -eq '1') { $InstallBackend = [switch]$true }
if ($env:LOCALAI_FORCE -eq '1') { $Force = [switch]$true }
if ($env:LOCALAI_UNINSTALL -eq '1') { $Uninstall = [switch]$true }

Install-LocalAI -Version $Version -InstallDir $InstallDir `
    -NoPath ([bool]$NoPath) -InstallBackend ([bool]$InstallBackend) `
    -Force ([bool]$Force) -Uninstall ([bool]$Uninstall)
