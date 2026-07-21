[CmdletBinding()]
param(
    [string]$WailsPath = ""
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$versionPath = Join-Path $projectRoot "VERSION"
$buildRoot = Join-Path $projectRoot "build"
$buildBin = Join-Path $buildRoot "bin"

if (-not (Test-Path -LiteralPath $versionPath)) {
    throw "Missing VERSION file: $versionPath"
}

$currentVersion = (Get-Content -LiteralPath $versionPath -Raw).Trim()
if ($currentVersion -notmatch '^(\d+)\.(\d+)\.(\d+)\.(\d+)$') {
    throw "VERSION must use four numeric parts, for example 1.0.0.1. Found: $currentVersion"
}

# Desktop releases advance the third segment and reset the final segment to 1.
# Example: 1.0.0.13 -> 1.0.1.1 -> 1.0.2.1. This avoids an unbounded build
# counter being mistaken for the product release number.
$nextVersion = "{0}.{1}.{2}.{3}" -f $Matches[1], $Matches[2], ([int]$Matches[3] + 1), 1
$outputName = "KOVA-Desktop-$nextVersion.exe"
$outputPath = Join-Path $buildRoot $outputName

if ([string]::IsNullOrWhiteSpace($WailsPath)) {
    $command = Get-Command wails -ErrorAction SilentlyContinue
    if ($null -eq $command) {
        throw "Wails CLI was not found. Install a compatible Wails CLI or pass -WailsPath."
    }
    $WailsPath = $command.Source
}

Push-Location $projectRoot
try {
    & $WailsPath build -clean -nopackage -webview2 browser -o $outputName
    if ($LASTEXITCODE -ne 0) {
        throw "Wails build failed with exit code $LASTEXITCODE. VERSION was not changed."
    }

    $builtPath = Join-Path $buildBin $outputName
    if (-not (Test-Path -LiteralPath $builtPath)) {
        throw "Wails reported success but did not create $builtPath. VERSION was not changed."
    }

    # A copied desktop executable needs these tools beside it. Development
    # builds still discover the tracked project root automatically.
    $portableBin = Join-Path $buildRoot "bin"
    New-Item -ItemType Directory -Force -Path $portableBin | Out-Null
    foreach ($tool in @("yt-dlp.exe", "ffmpeg.exe", "ffprobe.exe", "edge-tts.exe")) {
        $sourceTool = Join-Path $projectRoot (Join-Path "bin" $tool)
        if (-not (Test-Path -LiteralPath $sourceTool)) {
            throw "Missing required portable dependency: $sourceTool"
        }
        Copy-Item -LiteralPath $sourceTool -Destination (Join-Path $portableBin $tool) -Force
    }

    # A successful build becomes the sole desktop-app executable in build/.
    Get-ChildItem -LiteralPath $buildRoot -File -Filter 'KOVA-Desktop-*.exe' |
        Remove-Item -Force
    Move-Item -LiteralPath $builtPath -Destination $outputPath -Force
    Set-Content -LiteralPath $versionPath -Value $nextVersion -NoNewline
    Write-Host "Created $outputPath"
}
finally {
    Pop-Location
}
