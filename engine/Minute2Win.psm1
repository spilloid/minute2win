# minute2win engine functions. Pure where possible (no disk or network access) so tests/Minute2Win.Tests.ps1 can cover them.
# Runs in WinPE, which ships Windows PowerShell 5.1: no ternary, no ??, no ConvertFrom-Json -AsHashtable.

# Read a dotted path ('image.sha256') from a ConvertFrom-Json object; $Default when any segment is missing.
function Get-M2WValue {
    param($Object, [string]$Path, $Default = $null)
    $cur = $Object
    foreach ($seg in $Path.Split('.')) {
        if ($null -eq $cur) { return $Default }
        $p = $cur.PSObject.Properties[$seg]
        if (-not $p) { return $Default }
        $cur = $p.Value
    }
    if ($null -eq $cur) { return $Default }
    return $cur
}

# Returns a list of problems; empty means the manifest can drive a deployment.
function Test-M2WManifest {
    param($Manifest)
    $errors = New-Object System.Collections.Generic.List[string]
    if ((Get-M2WValue $Manifest 'schemaVersion') -ne 1) { $errors.Add('schemaVersion must be 1') }
    $src = Get-M2WValue $Manifest 'image.source'
    if ($src -ne 'https') { $errors.Add("image.source '$src' is not supported (only 'https' is implemented)") }
    $uri = [string](Get-M2WValue $Manifest 'image.uri' '')
    if ($src -eq 'https' -and -not $uri.StartsWith('https://')) { $errors.Add('image.uri must be an https:// URL') }
    if ([string](Get-M2WValue $Manifest 'image.sha256' '') -notmatch '^[0-9a-fA-F]{64}$') { $errors.Add('image.sha256 must be a 64-character hex SHA-256') }
    $edition = Get-M2WValue $Manifest 'image.edition'
    $index = Get-M2WValue $Manifest 'image.index'
    if (-not $edition -and -not $index) { $errors.Add('image.edition (name) or image.index is required') }
    $staging = Get-M2WValue $Manifest 'disk.stagingGB' 12
    if ($staging -lt 1) { $errors.Add('disk.stagingGB must be at least 1') }
    $user = Get-M2WValue $Manifest 'oobe.adminUser'
    if ($user -and $user -notmatch '^[A-Za-z0-9._-]{1,20}$') { $errors.Add('oobe.adminUser must be 1-20 letters, digits, . _ -') }
    $join = Get-M2WValue $Manifest 'tenant.joinMethod' 'none'
    if ($join -notin @('none', 'ad', 'entra', 'hybrid')) { $errors.Add("tenant.joinMethod '$join' is not one of none, ad, entra, hybrid") }
    $rep = [string](Get-M2WValue $Manifest 'report.url' '')
    if ($rep -and $rep -notmatch '^https?://') { $errors.Add('report.url must be an http:// or https:// URL') }
    $size = Get-M2WValue $Manifest 'image.sizeBytes' 0
    if ($size -lt 0) { $errors.Add('image.sizeBytes must be a positive byte count') }
    $cd = Get-M2WValue $Manifest 'guard.countdownSeconds' 10
    if ($cd -lt 0) { $errors.Add('guard.countdownSeconds must be 0 or more') }
    return , $errors.ToArray()
}

# Zero-touch disk choice: never prompts. Eligible = not USB or virtual-file, at least MinSizeGB. SD/MMC (card readers, eMMC) sort
# after real fixed disks; then lowest disk number. $DiskNumber overrides the choice but must still be eligible.
function Select-M2WTargetDisk {
    param([object[]]$Disks, [int]$DiskNumber = -1, [int]$MinSizeGB = 64)
    $eligible = @($Disks | Where-Object {
            $_.BusType -notin @('USB', 'File Backed Virtual') -and $_.Size -ge ([int64]$MinSizeGB * 1GB)
        })
    if ($DiskNumber -ge 0) {
        $pick = $eligible | Where-Object Number -eq $DiskNumber
        if (-not $pick) { throw "Disk $DiskNumber is not an eligible target (missing, USB, or under $MinSizeGB GB)." }
        return $pick
    }
    $pick = $eligible | Sort-Object @{ Expression = { if ($_.BusType -in @('SD', 'MMC')) { 1 } else { 0 } } }, Number | Select-Object -First 1
    if (-not $pick) { throw "No eligible target disk (need a non-USB disk of at least $MinSizeGB GB)." }
    return $pick
}

# GPT/UEFI layout: ESP, MSR, Windows, and a temporary staging partition at the end that is deleted after the apply.
# Letters come from Select-M2WDriveLetters, so they are free before 'clean' runs (diskpart's assign fails after the wipe otherwise).
function Get-M2WDiskpartScript {
    param([int]$DiskNumber, [int]$StagingGB = 12, [string]$System = 'S', [string]$Windows = 'W', [string]$Staging = 'Y')
    @(
        "select disk $DiskNumber"
        'clean'
        'convert gpt'
        'create partition efi size=260'
        'format quick fs=fat32 label="System"'
        "assign letter=$System"
        'create partition msr size=16'
        'create partition primary'
        "shrink desired=$($StagingGB * 1024)"
        'format quick fs=ntfs label="Windows"'
        "assign letter=$Windows"
        'create partition primary'
        'format quick fs=ntfs label="Deploy"'
        "assign letter=$Staging"
        'exit'
    ) -join "`r`n"
}

# Every letter in use: partitions, optical drives, network and subst drives all count, since any of them makes 'assign' fail.
function Get-M2WUsedLetters {
    $used = @()
    $used += Get-CimInstance Win32_LogicalDisk | ForEach-Object { $_.DeviceID }
    $used += Get-CimInstance Win32_Volume | Where-Object DriveLetter | ForEach-Object { $_.DriveLetter }
    $used += Get-PSDrive -PSProvider FileSystem | ForEach-Object { $_.Name }
    @($used | ForEach-Object { ([string]$_).Substring(0, 1).ToUpperInvariant() } | Sort-Object -Unique)
}

# Picks three distinct free letters, preferring S (system), W (Windows) and Y (staging). A and B are never used.
function Select-M2WDriveLetters {
    param([string[]]$Used)
    $taken = @($Used | ForEach-Object { $_.ToUpperInvariant() }) + 'A', 'B'
    $prefer = [ordered]@{ System = 'S'; Windows = 'W'; Staging = 'Y' }
    $pool = 'R', 'T', 'U', 'V', 'Q', 'P', 'O', 'N', 'M', 'L', 'K', 'J', 'I', 'H', 'G', 'F', 'E', 'Z'
    $pick = [ordered]@{}
    foreach ($role in $prefer.Keys) { if ($prefer[$role] -notin $taken) { $pick[$role] = $prefer[$role] } }
    $taken += @($pick.Values)
    foreach ($role in $prefer.Keys) {
        if ($pick.Contains($role)) { continue }
        $free = $pool | Where-Object { $_ -notin $taken } | Select-Object -First 1
        if (-not $free) { throw 'No free drive letters for the layout.' }
        $pick[$role] = $free
        $taken += $free
    }
    $pick = [ordered]@{ System = $pick.System; Windows = $pick.Windows; Staging = $pick.Staging }
    [pscustomobject]$pick
}

# Before the wipe: the image must fit the staging partition and leave Windows room. An unknown size (<= 0) fails closed: set
# image.sizeBytes in the manifest when the image server reports no size.
function Test-M2WCapacity {
    param([int64]$ImageBytes, [int]$StagingGB, [int64]$DiskBytes, [int]$MinWindowsGB = 32)
    $staging = [int64]$StagingGB * 1GB
    if ($ImageBytes -le 0) { throw 'Image size unknown (the server reported none and the manifest has no image.sizeBytes); nothing was changed.' }
    if ($ImageBytes + 512MB -gt $staging * 0.98) {
        throw ("Staging partition too small, nothing was changed: image {0:N1} GB needs disk.stagingGB of at least {1}" -f ($ImageBytes / 1GB), [math]::Ceiling(($ImageBytes + 512MB) / 0.98 / 1GB))
    }
    if ($DiskBytes - $staging - 300MB -lt [int64]$MinWindowsGB * 1GB) {
        throw ("Disk too small for Windows plus {0} GB staging, nothing was changed" -f $StagingGB)
    }
}

# Images: objects with ImageIndex and ImageName (Get-WindowsImage). Edition indices differ between ISOs, so match by name first.
function Resolve-M2WImageIndex {
    param([object[]]$Images, [string]$Edition, [int]$Index = 0)
    if ($Edition) {
        $hit = @($Images | Where-Object { $_.ImageName.Trim() -eq $Edition })
        if ($hit.Count -eq 0) { throw "Edition '$Edition' not found in image. Present: $(($Images | ForEach-Object ImageName) -join ', ')" }
        return [int]$hit[0].ImageIndex
    }
    if (-not ($Images | Where-Object ImageIndex -eq $Index)) { throw "Index $Index not found in image." }
    return $Index
}

# Fills the OOBE answer-file template. The password is escaped for XML; the result holds it in clear, as Windows requires.
function New-M2WUnattend {
    param([string]$Template, [string]$User, [string]$Password)
    $Template.Replace('__USER__', $User).Replace('__PASSWORD__', [Security.SecurityElement]::Escape($Password))
}

# SetupComplete.cmd runs as SYSTEM once Windows Setup finishes, before the first logon. Keep any lines already there.
function Get-M2WSetupCompleteLine {
    '%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%SystemDrive%\ProgramData\minute2win\Start-FirstBoot.ps1" >> "%SystemRoot%\Logs\minute2win\setupcomplete.log" 2>&1'
}

function Get-M2WHardware {
    $cs = Get-CimInstance Win32_ComputerSystem
    $bios = Get-CimInstance Win32_BIOS
    [ordered]@{
        manufacturer = $cs.Manufacturer
        model        = $cs.Model
        serial       = $bios.SerialNumber
        memoryGB     = [math]::Round($cs.TotalPhysicalMemory / 1GB, 1)
        disks        = @(Get-Disk | ForEach-Object { [ordered]@{ number = $_.Number; name = $_.FriendlyName; bus = [string]$_.BusType; sizeGB = [math]::Round($_.Size / 1GB) } })
        nics         = @(Get-CimInstance Win32_NetworkAdapter -Filter 'PhysicalAdapter=True' | ForEach-Object { [ordered]@{ name = $_.Name; mac = $_.MACAddress } })
    }
}

function Test-M2WWinPE { Test-Path 'HKLM:\SYSTEM\CurrentControlSet\Control\MiniNT' }

# 1 = BIOS, 2 = UEFI (set by wpeinit / wpeutil UpdateBootInfo).
function Get-M2WFirmwareType { (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control' -Name PEFirmwareType -ErrorAction SilentlyContinue).PEFirmwareType }

# The blade guard: a visible countdown any key aborts. Zero-touch either way; 0 seconds disables it.
function Wait-M2WGuard {
    param([int]$Seconds, [string]$Message)
    if ($Seconds -le 0) { return $true }
    for ($i = $Seconds; $i -gt 0; $i--) {
        Write-Host ("`r{0} Starting in {1,2}s, press any key to abort." -f $Message, $i) -NoNewline
        $until = (Get-Date).AddSeconds(1)
        while ((Get-Date) -lt $until) {
            try { if ([Console]::KeyAvailable) { [void][Console]::ReadKey($true); Write-Host ''; return $false } }
            catch { $null = $_ } # no interactive console (redirected): the countdown still runs, it just cannot be aborted
            Start-Sleep -Milliseconds 100
        }
    }
    Write-Host ''
    return $true
}

function Enable-M2WTls12 {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

# Before the wipe: a dead or mistyped image URL must leave the machine untouched, not wiped and unbootable. A one-byte ranged
# GET (not HEAD, which a presigned GET URL refuses) also returns the total size, for Test-M2WCapacity. Returns -1 if unknown.
function Test-M2WImageReachable {
    param($Manifest)
    $uri = Get-M2WValue $Manifest 'image.uri'
    try {
        $req = [Net.HttpWebRequest]::Create($uri)
        $req.AddRange(0, 0); $req.Timeout = 15000
        $resp = $req.GetResponse()
        $total = [int64]-1
        $range = $resp.Headers['Content-Range']
        if ($range -match '/(\d+)$') { $total = [int64]$Matches[1] }
        elseif ([int]$resp.StatusCode -eq 200) { $total = $resp.ContentLength }
        $resp.Close()
        return $total
    }
    catch { throw "Image not reachable, nothing was changed: $uri ($($_.Exception.Message))" }
}

# SetupComplete.cmd runs as SYSTEM once Windows Setup finishes. Prepend our line as bytes, so an existing script keeps its exact
# encoding and a trailing "exit" in it cannot skip us.
function Add-M2WSetupComplete {
    param([string]$Path)
    $line = [Text.Encoding]::ASCII.GetBytes((Get-M2WSetupCompleteLine) + "`r`n")
    $old = [byte[]]@()
    if (Test-Path $Path) { $old = [IO.File]::ReadAllBytes($Path) }
    [IO.File]::WriteAllBytes($Path, [byte[]]($line + $old))
}

# Only https is implemented; an 'smb' source belongs behind this same function when it exists.
function Get-M2WImage {
    param($Manifest, [string]$Destination)
    $src = Get-M2WValue $Manifest 'image.source'
    switch ($src) {
        'https' { (New-Object Net.WebClient).DownloadFile((Get-M2WValue $Manifest 'image.uri'), $Destination) }
        default { throw "image.source '$src' is not implemented" }
    }
    $want = ([string](Get-M2WValue $Manifest 'image.sha256')).ToLowerInvariant()
    $got = (Get-FileHash $Destination -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -ne $want) { throw "Image checksum mismatch: got $got, expected $want" }
}

# Progress for minute2win-server's board. Best effort: a short timeout and never an error, so a missing server can't stop a deploy.
function Send-M2WReport {
    param([string]$Url, [hashtable]$Body)
    if (-not $Url) { return }
    try {
        $req = [Net.WebRequest]::Create($Url)
        $req.Method = 'POST'; $req.ContentType = 'application/json'; $req.Timeout = 3000
        $bytes = [Text.Encoding]::UTF8.GetBytes(($Body | ConvertTo-Json -Depth 4 -Compress))
        $s = $req.GetRequestStream(); $s.Write($bytes, 0, $bytes.Length); $s.Close()
        $req.GetResponse().Close()
    }
    catch { Write-Verbose "report to $Url failed: $($_.Exception.Message)" }
}

Export-ModuleMember -Function *-M2W*
