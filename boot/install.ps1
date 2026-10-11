# minute2win loader, served at <base>/install.ps1 (the path the WinPE's startnet.cmd already fetches). Downloads the engine into
# X:\minute2win and runs it. The manifest is the one iPXE injected (X:\Windows\System32\manifest.json, see minute2win.ipxe) if
# present. An explicit -Manifest allows a static fallback; missing injection otherwise stops before running the engine. Behaviour changes by
# uploading files, not rebuilding WinPE.
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSReviewUnusedParameter', 'Yes', Justification = 'Accepted so older kvm-it scripts keep working')]
param(
    [string]$Base = 'https://boot.spillerstech.us/windows11',
    [string]$Manifest = '',
    [int]$Disk = -1,
    [string]$AdminUser = '',
    [switch]$NoReboot,
    [switch]$Yes
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
$injected = 'X:\Windows\System32\manifest.json'
if (!(Test-Path $injected) -and !$Manifest) {
    throw 'Boot manifest was not injected. Refusing to run the installer without an explicitly selected profile.'
}
$dir = 'X:\minute2win'
New-Item -ItemType Directory -Force $dir | Out-Null
$wc = New-Object Net.WebClient
foreach ($f in 'VERSION', 'Minute2Win.psm1', 'Start-Minute2Win.ps1', 'Start-FirstBoot.ps1', 'unattend-oobe.xml') {
    $wc.DownloadFile("$Base/minute2win/$f", (Join-Path $dir $f))
}
if (Test-Path $injected) { Copy-Item $injected "$dir\manifest.json" -Force }
else { $wc.DownloadFile("$Base/minute2win/manifests/$Manifest.json", "$dir\manifest.json") }

$engineArgs = @{ Manifest = "$dir\manifest.json"; Disk = $Disk; NoReboot = $NoReboot }
if ($AdminUser) { $engineArgs.AdminUser = $AdminUser }
& "$dir\Start-Minute2Win.ps1" @engineArgs
exit $LASTEXITCODE
