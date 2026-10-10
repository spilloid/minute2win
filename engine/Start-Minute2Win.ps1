# minute2win: zero-touch Windows deployment, run inside WinPE. DESTRUCTIVE BY DESIGN: it wipes the target disk without asking.
# The guard is how a machine gets here (an opt-in dnsmasq/USB/iPXE boot entry), plus a WinPE-only check and an abort countdown.
# Files expected next to this script: Minute2Win.psm1, Start-FirstBoot.ps1, manifest.json, unattend-oobe.xml (only with oobe.adminUser).
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSReviewUnusedParameter', 'Yes', Justification = 'Accepted so older kvm-it scripts keep working')]
param(
    [string]$Manifest = (Join-Path $PSScriptRoot 'manifest.json'),
    [int]$Disk = -1,         # power-tool override; never required
    [string]$AdminUser = '', # overrides the manifest's oobe.adminUser (kvm-it's scripts pass it)
    [switch]$NoReboot,
    [switch]$Yes             # accepted for older kvm-it scripts; deployment never prompts
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
Import-Module (Join-Path $PSScriptRoot 'Minute2Win.psm1') -Force

$version = (Get-Content (Join-Path $PSScriptRoot 'VERSION') -ErrorAction SilentlyContinue | Select-Object -First 1)
$logDir = 'X:\minute2win\logs'
New-Item -ItemType Directory -Force $logDir | Out-Null
Start-Transcript -Path (Join-Path $logDir ('deploy-{0:yyyyMMdd-HHmmss}.log' -f (Get-Date))) | Out-Null
$ok = $false
$L = $null   # drive letters, chosen at runtime
# The minute2win target is boot-to-flashed in about a minute, so every phase is timed (timing.json, next to the logs).
$clock = [Diagnostics.Stopwatch]::StartNew()
$timing = [ordered]@{}
$report = @{ url = ''; key = ''; macs = @(); model = ''; serial = '' }
function Send-Progress([string]$Phase, [string]$Status = 'running', [string]$Message = '') {
    Send-M2WReport -Url $report.url -Body @{ key = $report.key; macs = $report.macs; model = $report.model; serial = $report.serial; phase = $Phase; status = $Status; message = $Message; timing = $timing }
}
function Add-Lap([string]$Name) { $timing[$Name] = [math]::Round($clock.Elapsed.TotalSeconds, 1); Send-Progress $Name }
try {
    Write-Host "minute2win $version"
    if (-not (Test-M2WWinPE)) { throw 'Not running in WinPE. minute2win only runs from its boot image; refusing to touch this machine.' }
    if ((Get-M2WFirmwareType) -ne 2) { throw 'Firmware is not UEFI. Only UEFI/GPT is supported.' }
    Enable-M2WTls12

    $m = Get-Content $Manifest -Raw | ConvertFrom-Json
    if ($AdminUser) { $m | Add-Member -Force -NotePropertyName oobe -NotePropertyValue ([pscustomobject]@{ adminUser = $AdminUser }) }
    $problems = Test-M2WManifest $m
    if ($problems.Count) { throw "Manifest $Manifest is invalid:`n  $($problems -join "`n  ")" }
    Write-Host "Deployment: $(Get-M2WValue $m 'deployment.name' '(unnamed)')"

    $hw = Get-M2WHardware
    $hw | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $logDir 'hardware.json') -Encoding UTF8
    Write-Host ("Hardware: {0} {1}, serial {2}" -f $hw.manufacturer, $hw.model, $hw.serial)
    $report.url = Get-M2WValue $m 'report.url' ''
    $report.key = Get-M2WValue $m 'report.key' ''
    $report.macs = @($hw.nics | ForEach-Object { $_.mac } | Where-Object { $_ })
    $report.model = "$($hw.manufacturer) $($hw.model)"; $report.serial = [string]$hw.serial

    $target = Select-M2WTargetDisk -Disks (Get-Disk) -DiskNumber $Disk -MinSizeGB (Get-M2WValue $m 'disk.minSizeGB' 64)
    $msg = "WIPING Disk {0} ({1}, {2:N0} GB, {3}) and installing Windows." -f $target.Number, $target.FriendlyName, ($target.Size / 1GB), $target.BusType
    Write-Host $msg
    $imageBytes = Test-M2WImageReachable $m
    if ($imageBytes -le 0) { $imageBytes = [int64](Get-M2WValue $m 'image.sizeBytes' 0) }
    if ($imageBytes -gt 0) { Write-Host ("Image reachable: {0:N1} GB" -f ($imageBytes / 1GB)) }
    $stagingGB = Get-M2WValue $m 'disk.stagingGB' 12
    Test-M2WCapacity -ImageBytes $imageBytes -StagingGB $stagingGB -DiskBytes $target.Size
    $L = Select-M2WDriveLetters -Used (Get-M2WUsedLetters)
    if (-not (Wait-M2WGuard -Seconds (Get-M2WValue $m 'guard.countdownSeconds' 10) -Message $msg)) { throw 'Aborted by keypress. Nothing was changed.' }

    Add-Lap 'ready'
    Write-Host "Partitioning (system $($L.System):, Windows $($L.Windows):, staging $($L.Staging):)..."
    Get-M2WDiskpartScript -DiskNumber $target.Number -StagingGB $stagingGB -System $L.System -Windows $L.Windows -Staging $L.Staging |
        Set-Content X:\minute2win\diskpart.txt -Encoding ASCII
    diskpart /s X:\minute2win\diskpart.txt
    if ($LASTEXITCODE -ne 0) { throw "diskpart failed ($LASTEXITCODE)" }
    $W = "$($L.Windows):"; $Y = "$($L.Staging):"

    Add-Lap 'partitioned'
    Write-Host 'Downloading and verifying the image...'
    Get-M2WImage -Manifest $m -Destination "$Y\install.wim"
    $idx = Resolve-M2WImageIndex -Images (Get-WindowsImage -ImagePath "$Y\install.wim") -Edition (Get-M2WValue $m 'image.edition') -Index (Get-M2WValue $m 'image.index' 0)

    Add-Lap 'downloaded'
    Write-Host "Applying image index $idx..."
    dism /Apply-Image "/ImageFile:$Y\install.wim" /Index:$idx "/ApplyDir:$W\"
    if ($LASTEXITCODE -ne 0) { throw "DISM apply failed ($LASTEXITCODE)" }
    Add-Lap 'applied'
    & "$W\Windows\System32\bcdboot.exe" "$W\Windows" /s "$($L.System):" /f UEFI
    if ($LASTEXITCODE -ne 0) { throw "bcdboot failed ($LASTEXITCODE)" }

    # OOBE: with oobe.adminUser it is skipped and a local admin is made with the known baseline password, which Windows forces to
    # change at the next logon. The baseline is deliberately weak and public; the manifest never holds a password.
    $user = Get-M2WValue $m 'oobe.adminUser'
    if ($user) {
        $xml = New-M2WUnattend -Template (Get-Content (Join-Path $PSScriptRoot 'unattend-oobe.xml') -Raw) -User $user -Password 'Password1!'
        New-Item -ItemType Directory -Force "$W\Windows\Panther" | Out-Null
        Set-Content "$W\Windows\Panther\unattend.xml" $xml -Encoding UTF8
    }

    Write-Host 'Installing first-boot bootstrap...'
    $pd = "$W\ProgramData\minute2win"
    New-Item -ItemType Directory -Force $pd, "$W\Windows\Setup\Scripts", "$W\Windows\Logs\minute2win" | Out-Null
    Copy-Item (Join-Path $PSScriptRoot 'Start-FirstBoot.ps1') $pd
    Copy-Item $Manifest (Join-Path $pd 'manifest.json')
    Add-M2WSetupComplete "$W\Windows\Setup\Scripts\SetupComplete.cmd"

    Write-Host 'Reclaiming the staging partition...'
    Remove-Item "$Y\install.wim" -Force
    Remove-Partition -DriveLetter $L.Staging -Confirm:$false
    Resize-Partition -DriveLetter $L.Windows -Size (Get-PartitionSupportedSize -DriveLetter $L.Windows).SizeMax
    $timing['done'] = [math]::Round($clock.Elapsed.TotalSeconds, 1)
    Send-Progress 'done' 'done'
    $ok = $true
    Write-Host 'Deployment complete.'
}
catch {
    Write-Host "DEPLOYMENT FAILED: $($_.Exception.Message)" -ForegroundColor Red
    Send-Progress 'failed' 'failed' $_.Exception.Message
    Write-Host "Logs: $logDir. Not rebooting, so the screen shows what happened."
}
finally {
    # Bookkeeping only: nothing here may throw, or a finished deployment would not reboot (or a failure would hide).
    try { $timing.sinceWinPEBoot = [math]::Round(((Get-Date) - (Get-CimInstance Win32_OperatingSystem).LastBootUpTime).TotalSeconds, 1) } catch { $null = $_ }
    try {
        $timing | ConvertTo-Json | Set-Content (Join-Path $logDir 'timing.json') -Encoding UTF8
        Write-Host ("Timing (s): " + (($timing.GetEnumerator() | ForEach-Object { "$($_.Key) $($_.Value)" }) -join ', '))
    }
    catch { Write-Warning "could not write timing.json: $($_.Exception.Message)" }
    try { Stop-Transcript | Out-Null } catch { $null = $_ }
    try { if ($L -and (Test-Path "$($L.Windows):\Windows\Logs\minute2win")) { Copy-Item "$logDir\*" "$($L.Windows):\Windows\Logs\minute2win" -Force } }
    catch { Write-Warning "could not copy logs to the new Windows: $($_.Exception.Message)" }
}
if ($ok -and -not $NoReboot) { Start-Sleep 5; wpeutil reboot }
if (-not $ok) { exit 1 }
