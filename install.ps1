$ErrorActionPreference = "Stop"

$Repo = "mugunth13labs/cli-ask"
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
Write-Host "Downloading ask from $($Asset.browser_download_url)..."
Invoke-WebRequest -Uri $Asset.browser_download_url -OutFile $DestPath

# Ensure directory is on User PATH
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path = "$env:Path;$InstallDir"
    Write-Host "Added $InstallDir to user PATH."
}

Write-Host "Successfully installed ask to $DestPath!" -ForegroundColor Green
Write-Host "Run 'ask --help' to get started."
