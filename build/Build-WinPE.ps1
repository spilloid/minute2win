# Build the minute2win WinPE boot.wim. Run on Windows in an elevated "Deployment and Imaging Tools Environment"
# (Windows ADK + WinPE add-on; install both from Microsoft's links, winget's ADK manifest is stale).
# Output: <Work>\out\boot.wim, BCD, boot.sdi, to upload next to wimboot.
# -Drivers: a folder of .inf drivers (NIC/storage) to inject, as imaging-server-winpe's Update-WinPEImage.ps1 did.
# -Manual: startnet.cmd fetches the loader but leaves a prompt instead of deploying (for debugging a boot image).
param(
    [string]$Work = 'C:\WinPE_minute2win',
    [string]$Base = 'https://boot.spillerstech.us/windows11',
    [string]$Drivers = '',
    [switch]$Manual
)
$ErrorActionPreference = 'Stop'

if (Test-Path $Work) { Remove-Item $Work -Recurse -Force }
copype amd64 $Work
$mount = "$Work\mount"
Dism /Mount-Image /ImageFile:"$Work\media\sources\boot.wim" /Index:1 /MountDir:$mount
if ($LASTEXITCODE -ne 0) { throw 'mount failed' }

$oc = "${env:ProgramFiles(x86)}\Windows Kits\10\Assessment and Deployment Kit\Windows Preinstallation Environment\amd64\WinPE_OCs"
# order matters: dependencies first
foreach ($p in 'WinPE-WMI', 'WinPE-NetFX', 'WinPE-Scripting', 'WinPE-PowerShell', 'WinPE-StorageWMI', 'WinPE-DismCmdlets') {
    foreach ($cab in "$oc\$p.cab", "$oc\en-us\${p}_en-us.cab") {
        Dism /Image:$mount /Add-Package /PackagePath:"$cab"
        if ($LASTEXITCODE -ne 0) { Dism /Unmount-Image /MountDir:$mount /Discard; throw "adding $cab failed ($LASTEXITCODE)" }
    }
}
if ($Drivers) {
    Dism /Image:$mount /Add-Driver /Driver:"$Drivers" /Recurse
    if ($LASTEXITCODE -ne 0) { Dism /Unmount-Image /MountDir:$mount /Discard; throw 'driver injection failed' }
}

$run = "powershell -NoProfile -ExecutionPolicy Bypass -File X:\install.ps1 -Base `"$Base`""
if ($Manual) { $run = "echo To deploy:  $run" }
@"
@echo off
wpeinit
wpeutil UpdateBootInfo >nul
echo minute2win: waiting for network...
powershell -NoProfile -ExecutionPolicy Bypass -Command "[Net.ServicePointManager]::SecurityProtocol=[Net.ServicePointManager]::SecurityProtocol -bor 3072; `$i=0; while(-not (Test-Connection 1.1.1.1 -Count 1 -Quiet) -and `$i -lt 30){Start-Sleep 2;`$i++}; Invoke-WebRequest '$Base/install.ps1' -OutFile X:\install.ps1 -UseBasicParsing"
$run
cmd
"@ | Set-Content "$mount\Windows\System32\startnet.cmd" -Encoding ASCII

Dism /Unmount-Image /MountDir:$mount /Commit
if ($LASTEXITCODE -ne 0) { throw 'unmount failed' }
New-Item -ItemType Directory -Force "$Work\out" | Out-Null
Copy-Item "$Work\media\sources\boot.wim", "$Work\media\Boot\BCD", "$Work\media\Boot\boot.sdi" "$Work\out"
Write-Host "Done: $Work\out"
