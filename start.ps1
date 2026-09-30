$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.Application]::EnableVisualStyles()

function zh([string]$b64) {
    return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b64))
}

function Select-FolderPath([string]$description) {
    $dialog = New-Object System.Windows.Forms.FolderBrowserDialog
    $dialog.Description = $description
    $dialog.ShowNewFolderButton = $false

    if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
        return $dialog.SelectedPath
    }

    return $null
}

function Select-FilePath([string]$title, [string]$filter) {
    $dialog = New-Object System.Windows.Forms.OpenFileDialog
    $dialog.Title = $title
    $dialog.Filter = $filter
    $dialog.Multiselect = $false
    $dialog.CheckFileExists = $true

    if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
        return $dialog.FileName
    }

    return $null
}

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $scriptDir

if (-not $env:TG_APP_ID) {
    $env:TG_APP_ID = "2040"
}
if (-not $env:TG_APP_HASH) {
    $env:TG_APP_HASH = "b18441a1ff607e10a989891a5462e627"
}
$workers = 100

function Prompt-Mode() {
    Write-Host "============================================================" -ForegroundColor DarkGray
    Write-Host " tg-session-checker-go" -ForegroundColor Cyan
    Write-Host (" Telegram Session " + (zh "5om56YeP562b5Y+35bel5YW3")) -ForegroundColor White
    Write-Host "============================================================" -ForegroundColor DarkGray
    Write-Host (" 1. " + (zh "5Y+q562b5a2Y5rS7")) -ForegroundColor Green
    Write-Host (" 2. " + (zh "5Y+q6LeRIFNwYW1Cb3Q=")) -ForegroundColor Yellow
    Write-Host (" 3. " + (zh "5a2Y5rS7ICsgU3BhbUJvdA==")) -ForegroundColor Magenta
    Write-Host "============================================================" -ForegroundColor DarkGray

    while ($true) {
        $choice = Read-Host (zh "6K+36YCJ5oup5qih5byPIFsxLTNdICjpu5jorqQgMSk=")
        if ([string]::IsNullOrWhiteSpace($choice)) {
            $choice = "1"
        }

        switch ($choice) {
            "1" { return @{ Mode = "alive"; Label = (zh "5Y+q562b5a2Y5rS7") } }
            "2" { return @{ Mode = "spam"; Label = (zh "5Y+q6LeRIFNwYW1Cb3Q=") } }
            "3" { return @{ Mode = "both"; Label = (zh "5a2Y5rS7ICsgU3BhbUJvdA==") } }
            default {
                Write-Host (zh "6L6T5YWl5LiN5a+577yM5aGrIDEgLyAyIC8gM+OAgg==") -ForegroundColor Yellow
            }
        }
    }
}

function Prompt-InputPath() {
    while ($true) {
        Write-Host ""
        Write-Host "------------------------------------------------------------" -ForegroundColor DarkGray
        Write-Host (" " + (zh "MS4g6YCJ5oupIHNlc3Npb24g5paH5Lu25aS5")) -ForegroundColor White
        Write-Host (" " + (zh "Mi4g6YCJ5oup5Y2V5LiqIC5zZXNzaW9uIOaWh+S7tg==")) -ForegroundColor White
        Write-Host (" " + (zh "My4g6YCJ5oupIC56aXAg5Y6L57yp5YyF")) -ForegroundColor White
        Write-Host (" " + (zh "NC4g5omL5Yqo6L6T5YWl6Lev5b6E")) -ForegroundColor White
        Write-Host "------------------------------------------------------------" -ForegroundColor DarkGray

        $choice = Read-Host (zh "6K+36YCJ5oup6L6T5YWl57G75Z6LIFsxLTRdICjpu5jorqQgMSk=")
        if ([string]::IsNullOrWhiteSpace($choice)) {
            $choice = "1"
        }

        switch ($choice) {
            "1" {
                $selectedPath = Select-FilePath (zh "6K+36YCJ5oup6L+Z5Liq5paH5Lu25aS56YeM55qE5Lu75oSP5LiA5LiqIC5zZXNzaW9uIOaWh+S7tg==") "Session files (*.session)|*.session|All files (*.*)|*.*"
                if ($selectedPath) {
                    return Split-Path -Parent $selectedPath
                }
                Write-Host (zh "5L2g5Y+W5raI5LqG6YCJ5oup77yM6YeN5paw5p2l44CC") -ForegroundColor Yellow
            }
            "2" {
                $selectedPath = Select-FilePath (zh "6K+36YCJ5oupIC5zZXNzaW9uIOaWh+S7tg==") "Session files (*.session)|*.session|All files (*.*)|*.*"
                if ($selectedPath) {
                    return $selectedPath
                }
                Write-Host (zh "5L2g5Y+W5raI5LqG6YCJ5oup77yM6YeN5paw5p2l44CC") -ForegroundColor Yellow
            }
            "3" {
                $selectedPath = Select-FilePath (zh "6K+36YCJ5oupIC56aXAg5Y6L57yp5YyF") "Zip files (*.zip)|*.zip|All files (*.*)|*.*"
                if ($selectedPath) {
                    return $selectedPath
                }
                Write-Host (zh "5L2g5Y+W5raI5LqG6YCJ5oup77yM6YeN5paw5p2l44CC") -ForegroundColor Yellow
            }
            "4" {
                $manualPath = Read-Host (zh "6K+36L6T5YWl6Lev5b6E")
                if (-not [string]::IsNullOrWhiteSpace($manualPath) -and (Test-Path -LiteralPath $manualPath)) {
                    return $manualPath
                }
                Write-Host (zh "6Lev5b6E5LiN5a2Y5Zyo77yM6YeN5paw5p2l44CC") -ForegroundColor Yellow
            }
            default {
                Write-Host (zh "6L6T5YWl5LiN5a+577yM5aGrIDEgLyAyIC8gMyAvIDTjgII=") -ForegroundColor Yellow
            }
        }
    }
}

function Prompt-OutputDir([string]$defaultOutput) {
    $choice = Read-Host ((zh "5Zue6L2m55u05o6l55So6buY6K6k77yM6L6T5YWlIDEg6YCJ5oup5YW25LuW6L6T5Ye655uu5b2V") + " (default $defaultOutput)")
    if ($choice -eq "1") {
        while ($true) {
            $selectedPath = Select-FolderPath (zh "6K+36YCJ5oup6L6T5Ye655uu5b2V")
            if ($selectedPath) {
                return $selectedPath
            }
            Write-Host (zh "5L2g5Y+W5raI5LqG6YCJ5oup77yM6YeN5paw5p2l44CC") -ForegroundColor Yellow
        }
    }

    return $defaultOutput
}

function New-RunOutputDir([string]$outputRoot) {
    $runFolder = Get-Date -Format "yyyy-MM-dd_HH-mm-ss"
    $runOutputDir = Join-Path $outputRoot $runFolder
    New-Item -ItemType Directory -Path $runOutputDir -Force | Out-Null
    return $runOutputDir
}

while ($true) {
    $modeInfo = Prompt-Mode
    $mode = $modeInfo.Mode
    $modeLabel = $modeInfo.Label

    try {
        $inputPath = Prompt-InputPath
        $defaultOutputRoot = Join-Path $scriptDir "output"
        $outputRoot = Prompt-OutputDir $defaultOutputRoot

        if (-not (Test-Path -LiteralPath $outputRoot)) {
            New-Item -ItemType Directory -Path $outputRoot | Out-Null
        }
        $outputDir = New-RunOutputDir $outputRoot

        Write-Host ""
        Write-Host (zh "5byA5aeL5omn6KGM77ya") -ForegroundColor Cyan
        Write-Host ("  " + (zh "5qih5byP") + ": $modeLabel ($mode)") -ForegroundColor White
        Write-Host ("  " + (zh "6L6T5YWl") + ": $inputPath") -ForegroundColor White
        Write-Host ("  " + (zh "6L6T5Ye6") + ": $outputDir") -ForegroundColor White
        Write-Host ("  Workers: " + $workers) -ForegroundColor White
        Write-Host ("  AppID: " + $env:TG_APP_ID) -ForegroundColor White
        Write-Host ""

        $exePath = Join-Path $scriptDir "tg-session-checker-go.exe"
        if (Test-Path -LiteralPath $exePath) {
            & $exePath -input $inputPath -mode $mode -workers $workers -out $outputDir
        } else {
            go run . -input $inputPath -mode $mode -workers $workers -out $outputDir
        }
    } catch {
        Write-Host ""
        Write-Host ("ERROR: " + $_.Exception.Message) -ForegroundColor Red
    }

    Write-Host ""
    Write-Host "------------------------------------------------------------" -ForegroundColor DarkGray
    Write-Host (" " + (zh "MS4g57un57ut5qOA5p+l5YW25LuW5paH5Lu2")) -ForegroundColor White
    Write-Host (" " + (zh "Mi4g6YCA5Ye6")) -ForegroundColor White
    Write-Host "------------------------------------------------------------" -ForegroundColor DarkGray

    $nextAction = Read-Host (zh "6K+36YCJ5oup5LiL5LiA5q2lIFsxLTJdICjpu5jorqQgMik=")
    if ([string]::IsNullOrWhiteSpace($nextAction)) {
        $nextAction = "2"
    }
    if ($nextAction -ne "1") {
        break
    }
}
