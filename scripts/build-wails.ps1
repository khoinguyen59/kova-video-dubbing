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

# The final segment is a decimal build counter: 1.0.1.1 through 1.0.1.9,
# then 1.0.2.0, 1.0.2.1, and so on. A legacy value above 9 is normalised once
# by advancing the third segment and continuing at build 1.
$major = [int]$Matches[1]
$minor = [int]$Matches[2]
$release = [int]$Matches[3]
$build = [int]$Matches[4]

if ($build -gt 9) {
    $release++
    $build = 1
} elseif ($build -eq 9) {
    $release++
    $build = 0
} else {
    $build++
}

$nextVersion = "{0}.{1}.{2}.{3}" -f $major, $minor, $release, $build
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
    # The committed frontend/wailsjs bindings are typechecked by the frontend
    # build. Skipping regeneration here avoids Wails recursively inspecting the
    # retired Fyne desktop package, which is unrelated to the KOVA Wails shell
    # and can stall a production build on Windows.
    & $WailsPath build -clean -nopackage -webview2 browser -skipbindings -o $outputName
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

    # Visual OCR is intentionally an external Python bridge, not a bundled
    # Paddle runtime. Ship the small KOVA bridge next to the executable so a
    # portable desktop build can use the user's configured Python environment.
    $portableScripts = Join-Path $buildRoot "scripts"
    New-Item -ItemType Directory -Force -Path $portableScripts | Out-Null
    $ocrBridge = Join-Path $projectRoot "scripts\kova_visual_ocr.py"
    if (-not (Test-Path -LiteralPath $ocrBridge)) {
        throw "Missing required Visual OCR bridge: $ocrBridge"
    }
    Copy-Item -LiteralPath $ocrBridge -Destination (Join-Path $portableScripts "kova_visual_ocr.py") -Force

    # Keep the most recent prior desktop build alongside the new one. This
    # permits a release while its predecessor is running (Windows locks that
    # executable), but never leaves more than two release EXEs in build/.
    # We deliberately never try to delete the newest existing build because it
    # is the most likely executable to be running.
    $previousBuilds = Get-ChildItem -LiteralPath $buildRoot -File -Filter 'KOVA-Desktop-*.exe' |
        Sort-Object Name -Descending
    $buildsToRemove = @($previousBuilds | Select-Object -Skip 1)
    foreach ($previousBuild in $buildsToRemove) {
        Remove-Item -LiteralPath $previousBuild.FullName -Force
    }
    Move-Item -LiteralPath $builtPath -Destination $outputPath -Force
    Set-Content -LiteralPath $versionPath -Value $nextVersion -NoNewline
    Write-Host "Created $outputPath"
}
finally {
    Pop-Location
}
