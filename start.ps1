$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

function zh([string]$b64) {
    return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b64))
}

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $scriptDir

if (-not $env:TG_APP_ID) {
    $env:TG_APP_ID = "2040"
}
if (-not $env:TG_APP_HASH) {
    $env:TG_APP_HASH = "b18441a1ff607e10a989891a5462e627"
}

Write-Host "============================================================"
Write-Host " tg-session-checker-go"
Write-Host (" Telegram Session " + (zh "5om56YeP562b5Y+35bel5YW3"))
Write-Host "============================================================"
Write-Host (" 1. " + (zh "5Y+q562b5a2Y5rS7"))
Write-Host (" 2. " + (zh "5Y+q6LeRIFNwYW1Cb3Q="))
Write-Host (" 3. " + (zh "5a2Y5rS7ICsgU3BhbUJvdA=="))
Write-Host "============================================================"

$mode = $null
$modeLabel = $null
while (-not $mode) {
    $choice = Read-Host (zh "6K+36YCJ5oup5qih5byPIFsxLTNdICjpu5jorqQgMSk=")
    if ([string]::IsNullOrWhiteSpace($choice)) {
        $choice = "1"
    }

    switch ($choice) {
        "1" {
            $mode = "alive"
            $modeLabel = zh "5Y+q562b5a2Y5rS7"
        }
        "2" {
            $mode = "spam"
            $modeLabel = zh "5Y+q6LeRIFNwYW1Cb3Q="
        }
        "3" {
            $mode = "both"
            $modeLabel = zh "5a2Y5rS7ICsgU3BhbUJvdA=="
        }
        default {
            Write-Host (zh "6L6T5YWl5LiN5a+577yM5aGrIDEgLyAyIC8gM+OAgg==") -ForegroundColor Yellow
        }
    }
}

while ($true) {
    $inputPath = Read-Host (zh "6K+36L6T5YWlIHNlc3Npb24g55uu5b2VIC8g5Y2V5LiqIC5zZXNzaW9uIC8gLnppcCDot6/lvoQ=")
    if (-not [string]::IsNullOrWhiteSpace($inputPath) -and (Test-Path -LiteralPath $inputPath)) {
        break
    }
    Write-Host (zh "6Lev5b6E5LiN5a2Y5Zyo77yM6YeN5paw6L6T5YWl44CC") -ForegroundColor Yellow
}

$defaultOutput = Join-Path $scriptDir "output"
$outputPrompt = (zh "6L6T5Ye655uu5b2V") + " (default $defaultOutput)"
$outputDir = Read-Host $outputPrompt
if ([string]::IsNullOrWhiteSpace($outputDir)) {
    $outputDir = $defaultOutput
}

if (-not (Test-Path -LiteralPath $outputDir)) {
    New-Item -ItemType Directory -Path $outputDir | Out-Null
}

Write-Host ""
Write-Host (zh "5byA5aeL5omn6KGM77ya")
Write-Host ("  " + (zh "5qih5byP") + ": $modeLabel ($mode)")
Write-Host ("  " + (zh "6L6T5YWl") + ": $inputPath")
Write-Host ("  " + (zh "6L6T5Ye6") + ": $outputDir")
Write-Host ("  AppID: " + $env:TG_APP_ID)
Write-Host ""

$exePath = Join-Path $scriptDir "tg-session-checker-go.exe"
if (Test-Path -LiteralPath $exePath) {
    & $exePath -input $inputPath -mode $mode -out $outputDir
} else {
    go run . -input $inputPath -mode $mode -out $outputDir
}

Write-Host ""
Read-Host (zh "5omn6KGM5a6M5oiQ77yM5oyJ5Zue6L2m6YCA5Ye6") | Out-Null
