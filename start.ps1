$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.Application]::EnableVisualStyles()

if (-not ("CheckDialogs.FolderPicker" -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;

namespace CheckDialogs
{
    [Flags]
    public enum FOS : uint
    {
        PICKFOLDERS = 0x00000020,
        FORCEFILESYSTEM = 0x00000040,
        ALLOWMULTISELECT = 0x00000200,
        PATHMUSTEXIST = 0x00000800
    }

    public enum SIGDN : uint
    {
        FILESYSPATH = 0x80058000
    }

    [ComImport]
    [Guid("DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7")]
    class FileOpenDialogRCW
    {
    }

    [ComImport]
    [Guid("42f85136-db7e-439c-85f1-e4075d135fc8")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IFileDialog
    {
        [PreserveSig] int Show(IntPtr parent);
        void SetFileTypes(uint cFileTypes, IntPtr rgFilterSpec);
        void SetFileTypeIndex(uint iFileType);
        void GetFileTypeIndex(out uint piFileType);
        void Advise(IntPtr pfde, out uint pdwCookie);
        void Unadvise(uint dwCookie);
        void SetOptions(FOS fos);
        void GetOptions(out FOS pfos);
        void SetDefaultFolder(IShellItem psi);
        void SetFolder(IShellItem psi);
        void GetFolder(out IShellItem ppsi);
        void GetCurrentSelection(out IShellItem ppsi);
        void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string pszName);
        void GetFileName([MarshalAs(UnmanagedType.LPWStr)] out string pszName);
        void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string pszTitle);
        void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string pszText);
        void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string pszLabel);
        void GetResult(out IShellItem ppsi);
        void AddPlace(IShellItem psi, int fdap);
        void SetDefaultExtension([MarshalAs(UnmanagedType.LPWStr)] string pszDefaultExtension);
        void Close(int hr);
        void SetClientGuid(ref Guid guid);
        void ClearClientData();
        void SetFilter(IntPtr pFilter);
    }

    [ComImport]
    [Guid("d57c7288-d4ad-4768-be02-9d969532d960")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IFileOpenDialog : IFileDialog
    {
        [PreserveSig] new int Show(IntPtr parent);
        new void SetFileTypes(uint cFileTypes, IntPtr rgFilterSpec);
        new void SetFileTypeIndex(uint iFileType);
        new void GetFileTypeIndex(out uint piFileType);
        new void Advise(IntPtr pfde, out uint pdwCookie);
        new void Unadvise(uint dwCookie);
        new void SetOptions(FOS fos);
        new void GetOptions(out FOS pfos);
        new void SetDefaultFolder(IShellItem psi);
        new void SetFolder(IShellItem psi);
        new void GetFolder(out IShellItem ppsi);
        new void GetCurrentSelection(out IShellItem ppsi);
        new void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string pszName);
        new void GetFileName([MarshalAs(UnmanagedType.LPWStr)] out string pszName);
        new void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string pszTitle);
        new void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string pszText);
        new void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string pszLabel);
        new void GetResult(out IShellItem ppsi);
        new void AddPlace(IShellItem psi, int fdap);
        new void SetDefaultExtension([MarshalAs(UnmanagedType.LPWStr)] string pszDefaultExtension);
        new void Close(int hr);
        new void SetClientGuid(ref Guid guid);
        new void ClearClientData();
        new void SetFilter(IntPtr pFilter);
        void GetResults(out IShellItemArray ppenum);
        void GetSelectedItems(out IShellItemArray ppsai);
    }

    [ComImport]
    [Guid("43826d1e-e718-42ee-bc55-a1e261c37bfe")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IShellItem
    {
        void BindToHandler(IntPtr pbc, ref Guid bhid, ref Guid riid, out IntPtr ppv);
        void GetParent(out IShellItem ppsi);
        void GetDisplayName(SIGDN sigdnName, out IntPtr ppszName);
        void GetAttributes(uint sfgaoMask, out uint psfgaoAttribs);
        void Compare(IShellItem psi, uint hint, out int piOrder);
    }

    [ComImport]
    [Guid("b63ea76d-1f85-456f-a19c-48159efa858b")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IShellItemArray
    {
        void BindToHandler(IntPtr pbc, ref Guid bhid, ref Guid riid, out IntPtr ppvOut);
        void GetPropertyStore(int flags, ref Guid riid, out IntPtr ppv);
        void GetPropertyDescriptionList(ref IntPtr keyType, ref Guid riid, out IntPtr ppv);
        void GetAttributes(uint attribFlags, uint sfgaoMask, out uint psfgaoAttribs);
        void GetCount(out uint pdwNumItems);
        void GetItemAt(uint dwIndex, out IShellItem ppsi);
        void EnumItems(out IntPtr ppenumShellItems);
    }

    public static class FolderPicker
    {
        public static string[] PickFolders(string title, bool multiSelect)
        {
            IFileOpenDialog dialog = (IFileOpenDialog)new FileOpenDialogRCW();
            FOS options;
            dialog.GetOptions(out options);
            options |= FOS.PICKFOLDERS | FOS.FORCEFILESYSTEM | FOS.PATHMUSTEXIST;
            if (multiSelect)
            {
                options |= FOS.ALLOWMULTISELECT;
            }
            dialog.SetOptions(options);
            dialog.SetTitle(title);

            int hr = dialog.Show(IntPtr.Zero);
            if (hr == unchecked((int)0x800704C7))
            {
                return new string[0];
            }
            Marshal.ThrowExceptionForHR(hr);

            if (multiSelect)
            {
                IShellItemArray results;
                dialog.GetResults(out results);
                uint count;
                results.GetCount(out count);
                List<string> paths = new List<string>();
                for (uint i = 0; i < count; i++)
                {
                    IShellItem item;
                    results.GetItemAt(i, out item);
                    paths.Add(GetPath(item));
                }
                return paths.ToArray();
            }

            IShellItem result;
            dialog.GetResult(out result);
            return new[] { GetPath(result) };
        }

        private static string GetPath(IShellItem item)
        {
            IntPtr pointer = IntPtr.Zero;
            item.GetDisplayName(SIGDN.FILESYSPATH, out pointer);
            try
            {
                return Marshal.PtrToStringUni(pointer);
            }
            finally
            {
                if (pointer != IntPtr.Zero)
                {
                    Marshal.FreeCoTaskMem(pointer);
                }
            }
        }
    }
}
'@
}

function zh([string]$b64) {
    return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b64))
}

function Select-FolderPath([string]$description) {
    $paths = [CheckDialogs.FolderPicker]::PickFolders($description, $false)
    if ($paths.Count -gt 0) {
        return $paths[0]
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

function Select-FolderPaths([string]$title) {
    return @([CheckDialogs.FolderPicker]::PickFolders($title, $true))
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

function Get-ProxyDisplayText([string]$path) {
    if ([string]::IsNullOrWhiteSpace($path)) {
        return (zh "55u06L+e")
    }
    if (-not (Test-Path -LiteralPath $path)) {
        return (zh "55u06L+eICjmnKrmib7liLAgcHJveHkudHh0KQ==")
    }

    try {
        $lines = Get-Content -LiteralPath $path -ErrorAction Stop
    } catch {
        return $path
    }

    foreach ($rawLine in $lines) {
        $line = $rawLine.Trim()
        if ($line -and -not $line.StartsWith("#")) {
            return $path
        }
    }

    return (zh "55u06L+eIChwcm94eS50eHQg5Li656m6KQ==")
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
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllLines($tempFile, $paths, $utf8NoBom)
    return $tempFile
}

function Get-SafeFolderName([string]$name) {
    if ([string]::IsNullOrWhiteSpace($name)) {
        $safeName = "check_result"
    } else {
        $safeName = $name.Trim()
    }
    foreach ($char in [System.IO.Path]::GetInvalidFileNameChars()) {
        $safeName = $safeName.Replace($char, "_")
    }
    $safeName = $safeName -replace '\s+', '_'
    $safeName = $safeName.Trim('_', '.')
    if ([string]::IsNullOrWhiteSpace($safeName)) {
        return "check_result"
    }
    return $safeName
}

function Get-InputBaseName([string]$path) {
    if (-not (Test-Path -LiteralPath $path)) {
        return "check_result"
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
        Write-Host (" " + (zh "MS4g6YCJ5oup5Y2V5LiqIC5zZXNzaW9uIOaWh+S7tg==")) -ForegroundColor White
        Write-Host (" " + (zh "Mi4g6YCJ5oup5LiA5Liq5oiW5aSa5LiqIHNlc3Npb24vdGRhdGEg5paH5Lu25aS5")) -ForegroundColor White
        Write-Host (" " + (zh "My4g6YCJ5oupIC56aXAg5Y6L57yp5YyF")) -ForegroundColor White
        Write-Host (" " + (zh "NC4g5omL5Yqo6L6T5YWl6Lev5b6E77yI5pSv5oyBIC5zZXNzaW9uIC8gdGRhdGEgLyDnm67lvZUgLyAuemlw77yJ")) -ForegroundColor White
        Write-Host "------------------------------------------------------------" -ForegroundColor DarkGray
        Write-Host (" " + (zh "dGRhdGEg5paH5Lu25aS56K+36YCJ56ysIDIg6aG55oiW56ysIDQg6aG5")) -ForegroundColor DarkCyan

        $choice = Read-Host (zh "6K+36YCJ5oup6L6T5YWl57G75Z6LIFsxLTRdICjpu5jorqQgMSk=")
        if ([string]::IsNullOrWhiteSpace($choice)) {
            $choice = "1"
        }

        switch ($choice) {
            "1" {
                $selectedPath = Select-FilePath (zh "6K+36YCJ5oup5LiA5LiqIC5zZXNzaW9uIOaWh+S7tu+8jOaIluaUueeUqOesrCAyLzQg6aG56YCJIHRkYXRhIOaWh+S7tuWkuQ==") "Session files (*.session)|*.session|All files (*.*)|*.*"
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
                $folderPaths = @(Select-FolderPaths (zh "6K+36YCJ5oup5LiA5Liq5oiW5aSa5LiqIHNlc3Npb24vdGRhdGEg5paH5Lu25aS5"))
                if ($folderPaths.Count -gt 0) {
                    $folderMap = @{}
                    foreach ($folderPath in $folderPaths) {
                        if (-not [string]::IsNullOrWhiteSpace($folderPath)) {
                            $folderMap[$folderPath] = $true
                        }
                    }
                    $folderPaths = @($folderMap.Keys | Sort-Object)
                    if ($folderPaths.Count -gt 0) {
                        $inputListFile = New-InputListFile $folderPaths
                        $displayText = ((zh "5aSa5Liq5paH5Lu25aS5") + " (" + $folderPaths.Count + " " + (zh "5Liq") + ")")
                        $resultName = ((zh "5aSa5Liq5paH5Lu25aS5") + "_" + $folderPaths.Count + (zh "5Liq"))
                        if ($folderPaths.Count -eq 1) {
                            $displayText = $folderPaths[0]
                            $resultName = Get-InputBaseName $folderPaths[0]
                        }
                        return @{
                            Args = @("-input-list", $inputListFile)
                            Display = $displayText
                            TempFile = $inputListFile
                            ResultName = $resultName
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

if ($env:TG_CHECKER_SCRIPT_ONLY -eq "1") {
    return
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
        Write-Host ("  Proxy: " + (Get-ProxyDisplayText $proxyFile)) -ForegroundColor White
        Write-Host ("  AppID: " + $env:TG_APP_ID) -ForegroundColor White
        Write-Host ""

        $exePath = Join-Path $scriptDir "tg-session-checker-go.exe"
        $mainGoPath = Join-Path $scriptDir "main.go"
        if (Test-Path -LiteralPath $exePath) {
            $exeInfo = Get-Item -LiteralPath $exePath
            if ($mainGoPath -and (Test-Path -LiteralPath $mainGoPath)) {
                $latestReferenceTime = (Get-Item -LiteralPath $mainGoPath).LastWriteTimeUtc
                if ($exeInfo.LastWriteTimeUtc -lt $latestReferenceTime) {
                    Write-Host "WARNING: exe is older than main.go; core binary may be stale." -ForegroundColor Yellow
                    Write-Host ("  EXE: " + $exeInfo.LastWriteTime.ToString("yyyy-MM-dd HH:mm:ss")) -ForegroundColor DarkYellow
                    Write-Host ("  REF: " + $latestReferenceTime.ToLocalTime().ToString("yyyy-MM-dd HH:mm:ss")) -ForegroundColor DarkYellow
                    Write-Host ""
                }
            }
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
