[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$InputVideo,

    [Parameter(Mandatory = $true)]
    [string]$OutputVideo,

    [string]$Ffmpeg = 'bin/ffmpeg.exe',
    [double]$StartSeconds = 9.12,
    [double]$EndSeconds = 141.84,
    [string]$VisualAss
)

$ErrorActionPreference = 'Stop'

# This is intentionally scoped to the 1920x1080 flashcard template in the
# supplied learning video. The Vietnamese title and example already exist in
# the centre/right of each slide; the masks remove their duplicate English
# title, IPA, and left-column example without touching the Vietnamese dubbing
# subtitle at the bottom of the frame.
$input = (Resolve-Path -LiteralPath $InputVideo).Path
$outputParent = Split-Path -Parent $OutputVideo
if ($outputParent) { [System.IO.Directory]::CreateDirectory($outputParent) | Out-Null }
$enabled = "between(t,$StartSeconds,$EndSeconds)"
$filter = @(
    "drawbox=x=350:y=120:w=1220:h=370:color=0xFFFFFF:t=fill:enable='$enabled'",
    "drawbox=x=180:y=735:w=625:h=210:color=0xFDFBFC:t=fill:enable='$enabled'"
) -join ','
if (-not [string]::IsNullOrWhiteSpace($VisualAss)) {
    $assPath = [System.IO.Path]::GetFullPath($VisualAss).Replace('\', '/').Replace(':', '\:')
    $filter = "$filter,ass=filename='$assPath'"
}

& $Ffmpeg -y -i $input -vf $filter -c:v libx264 -preset medium -crf 18 -c:a copy $OutputVideo
if ($LASTEXITCODE -ne 0) { throw "ffmpeg failed with exit code $LASTEXITCODE" }
