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

function Select-FilePaths([string]$title, [string]$filter) {
    $dialog = New-Object System.Windows.Forms.OpenFileDialog
    $dialog.Title = $title
    $dialog.Filter = $filter
    $dialog.Multiselect = $true
    $dialog.CheckFileExists = $true

    if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
        return @($dialog.FileNames)
    }

    return @()
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
$proxyFile = Join-Path $scriptDir "proxy.txt"

if (-not (Test-Path -LiteralPath $proxyFile)) {
    @(
        "# one proxy per line",
        "# socks5://user:pass@127.0.0.1:1080",
        "# 127.0.0.1:1080"
    ) | Set-Content -LiteralPath $proxyFile -Encoding UTF8
}

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

function New-InputListFile([string[]]$paths) {
    $tempFile = [System.IO.Path]::Combine([System.IO.Path]::GetTempPath(), ("tg-session-check-inputs-" + [guid]::NewGuid().ToString("N") + ".txt"))
    [System.IO.File]::WriteAllLines($tempFile, $paths, [System.Text.Encoding]::UTF8)
    return $tempFile
}

function Get-SafeFolderName([string]$name) {
    $safeName = [string]::IsNullOrWhiteSpace($name) ? "检查结果" : $name.Trim()
    foreach ($char in [System.IO.Path]::GetInvalidFileNameChars()) {
        $safeName = $safeName.Replace($char, "_")
    }
    $safeName = $safeName -replace '\s+', '_'
    $safeName = $safeName.Trim('_', '.')
    if ([string]::IsNullOrWhiteSpace($safeName)) {
        return "检查结果"
    }
    return $safeName
}

function Get-InputBaseName([string]$path) {
    if (-not (Test-Path -LiteralPath $path)) {
        return "检查结果"
    }
    $item = Get-Item -LiteralPath $path
    if ($item.PSIsContainer) {
        return $item.Name
    }
    return [System.IO.Path]::GetFileNameWithoutExtension($item.Name)
}

function Prompt-InputSource() {
    while ($true) {
        Write-Host ""
        Write-Host "------------------------------------------------------------" -ForegroundColor DarkGray
        Write-Host (" " + (zh "MS4g6YCJ5oupIHNlc3Npb24g5paH5Lu25aS5")) -ForegroundColor White
        Write-Host (" " + (zh "Mi4g6YCJ5oup5aSa5Liq5paH5Lu25aS5")) -ForegroundColor White
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
                    $folderPath = Split-Path -Parent $selectedPath
                    return @{
                        Args = @("-input", $folderPath)
                        Display = $folderPath
                        TempFile = $null
                        ResultName = (Get-InputBaseName $folderPath)
                    }
                }
                Write-Host (zh "5L2g5Y+W5raI5LqG6YCJ5oup77yM6YeN5paw5p2l44CC") -ForegroundColor Yellow
            }
            "2" {
                $selectedPaths = Select-FilePaths (zh "6K+36YCJ5oup5aSa5Liq5paH5Lu25aS577yI5q+P5Liq5paH5Lu25aS55Lu75oSP54K55LiA5LiqIC5zZXNzaW9u77yJ") "Session files (*.session)|*.session|All files (*.*)|*.*"
                if ($selectedPaths.Count -gt 0) {
                    $folderMap = @{}
                    foreach ($selectedPath in $selectedPaths) {
                        $folderPath = Split-Path -Parent $selectedPath
                        if (-not [string]::IsNullOrWhiteSpace($folderPath)) {
                            $folderMap[$folderPath] = $true
                        }
                    }
                    $folderPaths = @($folderMap.Keys | Sort-Object)
                    if ($folderPaths.Count -gt 0) {
                        $inputListFile = New-InputListFile $folderPaths
                        return @{
                            Args = @("-input-list", $inputListFile)
                            Display = ((zh "5aSa5Liq5paH5Lu25aS5") + " (" + $folderPaths.Count + " " + (zh "5Liq") + ")")
                            TempFile = $inputListFile
                            ResultName = ((zh "5aSa5Liq5paH5Lu25aS5") + "_" + $folderPaths.Count + (zh "5Liq"))
                        }
                    }
                }
                Write-Host (zh "5L2g5Y+W5raI5LqG6YCJ5oup77yM6YeN5paw5p2l44CC") -ForegroundColor Yellow
            }
            "3" {
                $selectedPath = Select-FilePath (zh "6K+36YCJ5oupIC56aXAg5Y6L57yp5YyF") "Zip files (*.zip)|*.zip|All files (*.*)|*.*"
                if ($selectedPath) {
                    return @{
                        Args = @("-input", $selectedPath)
                        Display = $selectedPath
                        TempFile = $null
                        ResultName = (Get-InputBaseName $selectedPath)
                    }
                }
                Write-Host (zh "5L2g5Y+W5raI5LqG6YCJ5oup77yM6YeN5paw5p2l44CC") -ForegroundColor Yellow
            }
            "4" {
                $manualPath = Read-Host (zh "6K+36L6T5YWl6Lev5b6E")
                if (-not [string]::IsNullOrWhiteSpace($manualPath) -and (Test-Path -LiteralPath $manualPath)) {
                    return @{
                        Args = @("-input", $manualPath)
                        Display = $manualPath
                        TempFile = $null
                        ResultName = (Get-InputBaseName $manualPath)
                    }
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

function New-RunOutputDir([string]$outputRoot, [string]$runName) {
    $runFolder = Get-Date -Format "yyyy-MM-dd_HH-mm-ss"
    $safeRunName = Get-SafeFolderName $runName
    $runOutputDir = Join-Path $outputRoot ($safeRunName + "_" + $runFolder)
    New-Item -ItemType Directory -Path $runOutputDir -Force | Out-Null
    return $runOutputDir
}

while ($true) {
    $modeInfo = Prompt-Mode
    $mode = $modeInfo.Mode
    $modeLabel = $modeInfo.Label

    try {
        $inputInfo = Prompt-InputSource
        $inputArgs = @($inputInfo.Args)
        $inputDisplay = $inputInfo.Display
        $tempInputListFile = $inputInfo.TempFile
        $resultName = $inputInfo.ResultName
        $defaultOutputRoot = Join-Path $scriptDir "output"
        $outputRoot = Prompt-OutputDir $defaultOutputRoot

        if (-not (Test-Path -LiteralPath $outputRoot)) {
            New-Item -ItemType Directory -Path $outputRoot | Out-Null
        }
        $outputDir = New-RunOutputDir $outputRoot $resultName

        Write-Host ""
        Write-Host (zh "5byA5aeL5omn6KGM77ya") -ForegroundColor Cyan
        Write-Host ("  " + (zh "5qih5byP") + ": $modeLabel ($mode)") -ForegroundColor White
        Write-Host ("  " + (zh "6L6T5YWl") + ": $inputDisplay") -ForegroundColor White
        Write-Host ("  " + (zh "6L6T5Ye6") + ": $outputDir") -ForegroundColor White
        Write-Host ("  Workers: " + $workers) -ForegroundColor White
        Write-Host ("  Proxy: " + $proxyFile) -ForegroundColor White
        Write-Host ("  AppID: " + $env:TG_APP_ID) -ForegroundColor White
        Write-Host ""

        $exePath = Join-Path $scriptDir "tg-session-checker-go.exe"
        if (Test-Path -LiteralPath $exePath) {
            & $exePath @inputArgs -mode $mode -workers $workers -proxy-file $proxyFile -out $outputDir
        } else {
            go run . @inputArgs -mode $mode -workers $workers -proxy-file $proxyFile -out $outputDir
        }
    } catch {
        Write-Host ""
        Write-Host ("ERROR: " + $_.Exception.Message) -ForegroundColor Red
    } finally {
        if ($tempInputListFile -and (Test-Path -LiteralPath $tempInputListFile)) {
            Remove-Item -LiteralPath $tempInputListFile -Force -ErrorAction SilentlyContinue
        }
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
