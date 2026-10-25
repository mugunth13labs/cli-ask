$ErrorActionPreference = "Stop"

$Repo = "mugunth13labs/cli-ask"
Write-Host "Fetching latest release metadata from GitHub..."
$LatestRelease = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
$Asset = $LatestRelease.assets | Where-Object { $_.name -eq "ask-windows-amd64.exe" }

if (-not $Asset) {
    Write-Error "Could not find ask-windows-amd64.exe in the latest release."
    exit 1
}

$InstallDir = "$env:LOCALAPPDATA\Programs\ask"
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$DestPath = Join-Path $InstallDir "ask.exe"
$TempPath = Join-Path $InstallDir "ask.exe.new"
$OldPath  = Join-Path $InstallDir "ask.exe.old"

# Clean up any lingering old/temp files from previous failed runs
if (Test-Path $TempPath) { Remove-Item $TempPath -Force -ErrorAction SilentlyContinue }
if (Test-Path $OldPath)  { Remove-Item $OldPath -Force -ErrorAction SilentlyContinue }

Write-Host "Downloading ask ($($LatestRelease.tag_name)) from $($Asset.browser_download_url)..."
Invoke-WebRequest -Uri $Asset.browser_download_url -OutFile $TempPath

# Atomic in-place swap to bypass Windows file locks on running executables
if (Test-Path $DestPath) {
    try {
        Move-Item -Path $DestPath -Destination $OldPath -Force
    } catch {
        # If moving fails, attempt to stop any orphaned ask processes
        Get-Process -Name "ask" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
        Start-Sleep -Milliseconds 500
        Move-Item -Path $DestPath -Destination $OldPath -Force
    }
}

Move-Item -Path $TempPath -Destination $DestPath -Force
Remove-Item $OldPath -Force -ErrorAction SilentlyContinue

# Ensure directory is on User PATH
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path = "$env:Path;$InstallDir"
    Write-Host "Added $InstallDir to user PATH."
}

Write-Host "Successfully installed ask ($($LatestRelease.tag_name)) to $DestPath!" -ForegroundColor Green
Write-Host "Run 'ask --help' to get started."
