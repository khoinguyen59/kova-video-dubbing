[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$InputSrt,

    [Parameter(Mandatory = $true)]
    [string]$OutputSrt,

    [string]$BaseUrl = 'https://ollama.com/api/chat',
    [string]$Model = 'gpt-oss:20b-cloud',
    [int]$BatchSize = 8
)

$ErrorActionPreference = 'Stop'

# Windows PowerShell 5 does not load System.Net.Http by default, while
# PowerShell 7 does. Load it explicitly for both hosts.
Add-Type -AssemblyName System.Net.Http

if ([string]::IsNullOrWhiteSpace($env:OLLAMA_API_KEY)) {
    throw 'OLLAMA_API_KEY must be set in the current process environment.'
}
if ($BatchSize -lt 1 -or $BatchSize -gt 20) {
    throw 'BatchSize must be between 1 and 20.'
}

function Invoke-OllamaChat([string]$Prompt) {
    $payload = @{
        model    = $Model
        messages = @(@{ role = 'user'; content = $Prompt })
        stream   = $false
        think    = $false
    } | ConvertTo-Json -Compress -Depth 6

    # Some intermediary endpoints omit a response charset. Invoke-RestMethod
    # then selects the Windows ANSI code page, corrupting Vietnamese before it
    # reaches the TTS/SRT pipeline. Decode the raw HTTP response as UTF-8.
    $client = [System.Net.Http.HttpClient]::new()
    try {
        $client.Timeout = [TimeSpan]::FromSeconds(300)
        $client.DefaultRequestHeaders.Authorization = [System.Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $env:OLLAMA_API_KEY)
        $body = [System.Text.UTF8Encoding]::new($false).GetBytes($payload)
        $httpContent = [System.Net.Http.ByteArrayContent]::new($body)
        $httpContent.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::Parse('application/json; charset=utf-8')
        $response = $client.PostAsync($BaseUrl, $httpContent).GetAwaiter().GetResult()
        $rawResponse = $response.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
        $decodedResponse = [System.Text.Encoding]::UTF8.GetString($rawResponse)
        if (-not $response.IsSuccessStatusCode) {
            throw "Ollama request failed with HTTP $([int]$response.StatusCode): $decodedResponse"
        }
        $content = [string](ConvertFrom-Json -InputObject $decodedResponse).message.content
    } finally {
        if ($null -ne $client) { $client.Dispose() }
    }
    if ([string]::IsNullOrWhiteSpace($content)) {
        throw 'Ollama returned an empty message.content.'
    }
    return $content.Trim()
}

function Get-ResidualEnglishWords([string]$Source, [string]$Candidate) {
    $allowed = @('api', 'url', 'http', 'https', 'openai', 'chatgpt', 'youtube', 'github', 'tiktok', 'google')
    $sourceWords = [regex]::Matches($Source.ToLowerInvariant(), "[a-z][a-z'-]{2,}") | ForEach-Object { $_.Value }
    $targetWords = [regex]::Matches($Candidate.ToLowerInvariant(), "[a-z][a-z'-]{2,}") | ForEach-Object { $_.Value }
    $targetSet = @{}
    foreach ($word in $targetWords) { $targetSet[$word] = $true }
    return @($sourceWords | Where-Object { $targetSet.ContainsKey($_) -and $_ -notin $allowed } | Select-Object -Unique)
}

$raw = [System.IO.File]::ReadAllText((Resolve-Path -LiteralPath $InputSrt), [System.Text.Encoding]::UTF8)
$records = @()
foreach ($part in [regex]::Split($raw.Trim(), "\r?\n\s*\r?\n")) {
    $lines = @($part -split "\r?\n" | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne '' })
    if ($lines.Count -lt 3 -or $lines[1] -notmatch '-->') {
        throw "Invalid SRT block: $part"
    }
    $records += [pscustomobject]@{
        Index = [int]$lines[0]
        Timestamp = $lines[1]
        Source = (($lines | Select-Object -Skip 2) -join ' ').Trim()
        Text = ''
    }
}

for ($start = 0; $start -lt $records.Count; $start += $BatchSize) {
    $end = [Math]::Min($start + $BatchSize, $records.Count)
    $batch = @($records[$start..($end - 1)])
    $sourceList = for ($i = 0; $i -lt $batch.Count; $i++) {
        "{0}. {1}" -f ($i + 1), $batch[$i].Source
    }

    $prompt = @"
Translate these English learning-video subtitles into concise, natural Vietnamese.
Return ONLY this JSON: {"translations":[{"index":1,"text":"..."}]}.
Rules: preserve the number and order of items; translate every ordinary English word; retain Latin text only for a genuine proper name, brand, acronym, URL, code, or number; no notes, markdown, bilingual text, or explanations. Use short spoken Vietnamese that fits the existing subtitle timing.

$($sourceList -join "`n")
"@

    $answer = Invoke-OllamaChat $prompt
    $jsonText = $answer -replace '^```(?:json)?\s*', '' -replace '\s*```$', ''
    try {
        $translations = @((ConvertFrom-Json -InputObject $jsonText).translations)
    } catch {
        throw "Batch starting at subtitle $($batch[0].Index) returned invalid JSON: $answer"
    }
    if ($translations.Count -ne $batch.Count) {
        throw "Batch starting at subtitle $($batch[0].Index) returned $($translations.Count) translations; expected $($batch.Count)."
    }

    for ($i = 0; $i -lt $batch.Count; $i++) {
        $translation = $translations | Where-Object { [int]$_.index -eq ($i + 1) } | Select-Object -First 1
        if ($null -eq $translation -or [string]::IsNullOrWhiteSpace([string]$translation.text)) {
            throw "Missing translation $($i + 1) in batch starting at subtitle $($batch[0].Index)."
        }
        $batch[$i].Text = ([string]$translation.text -replace '\s+', ' ').Trim()
    }
}

# Repair any ordinary source-English word which survived the initial batch.
foreach ($record in $records) {
    $residual = @(Get-ResidualEnglishWords $record.Source $record.Text)
    if ($residual.Count -eq 0) { continue }
    $repairPrompt = @"
Rewrite the Vietnamese subtitle below. Output ONLY the corrected Vietnamese text, no quotes or explanations.
Source: "$($record.Source)"
Candidate: "$($record.Text)"
Ordinary English words that must be translated: $($residual -join ', ')
Keep only genuine proper names, brands, acronyms, URLs, code, and numbers in Latin text. Keep it concise for dubbing.
"@
    $record.Text = ((Invoke-OllamaChat $repairPrompt) -replace '\s+', ' ').Trim()
    $remaining = @(Get-ResidualEnglishWords $record.Source $record.Text)
    # A handful of common Vietnamese loanwords are routinely returned in Latin
    # script despite the strict prompt. For a full-Vietnamese deliverable,
    # replace only the unambiguous vocabulary terms before declaring failure.
    $mandatoryVietnamese = @{
        'sofa' = 'ghế trường kỷ'
        'terminal' = 'nhà ga'
    }
    foreach ($word in @($remaining)) {
        if ($mandatoryVietnamese.ContainsKey($word)) {
            $record.Text = [regex]::Replace($record.Text, "(?i)\b$([regex]::Escape($word))\b", $mandatoryVietnamese[$word])
        }
    }
    $remaining = @(Get-ResidualEnglishWords $record.Source $record.Text)
    if ($remaining.Count -gt 0) {
        throw "Subtitle $($record.Index) still contains ordinary English after repair: $($remaining -join ', ')"
    }
}

$outputDir = Split-Path -Parent $OutputSrt
if ($outputDir) { [System.IO.Directory]::CreateDirectory($outputDir) | Out-Null }
$builder = [System.Text.StringBuilder]::new()
foreach ($record in $records) {
    [void]$builder.AppendLine($record.Index)
    [void]$builder.AppendLine($record.Timestamp)
    [void]$builder.AppendLine($record.Text)
    [void]$builder.AppendLine()
}
[System.IO.File]::WriteAllText($OutputSrt, $builder.ToString(), [System.Text.UTF8Encoding]::new($false))

[pscustomobject]@{
    input = (Resolve-Path -LiteralPath $InputSrt).Path
    output = (Resolve-Path -LiteralPath $OutputSrt).Path
    model = $Model
    translated_subtitles = $records.Count
} | ConvertTo-Json
