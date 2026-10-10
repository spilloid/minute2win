# minute2win first boot. Runs as SYSTEM from SetupComplete.cmd, once Windows Setup finishes and before the first logon.
# Idempotent: a marker file, written only on success, makes reruns a no-op; a failed run is retried next time.
# Never fails the machine: problems are logged and recorded in the result file.
$ErrorActionPreference = 'Stop'
$root = Join-Path $env:SystemDrive 'ProgramData\minute2win'
$logs = Join-Path $env:SystemRoot 'Logs\minute2win'
$marker = Join-Path $root 'firstboot.done'
New-Item -ItemType Directory -Force $logs | Out-Null
if (Test-Path $marker) { Write-Output "minute2win first boot already ran ($(Get-Content $marker)); nothing to do."; return }

Start-Transcript -Path (Join-Path $logs 'firstboot.log') -Append | Out-Null
$result = [ordered]@{ started = (Get-Date).ToString('o'); steps = @() }
try {
    $m = Get-Content (Join-Path $root 'manifest.json') -Raw | ConvertFrom-Json
    $os = Get-CimInstance Win32_OperatingSystem
    $result.os = "$($os.Caption) $($os.Version)"
    $result.computerName = $env:COMPUTERNAME

    $join = 'none'
    if ($m.PSObject.Properties['tenant'] -and $m.tenant.PSObject.Properties['joinMethod']) { $join = $m.tenant.joinMethod }
    switch ($join) {
        'none' { $result.steps += 'join: none' }
        default { $result.steps += "join: '$join' is not implemented yet; skipped"; Write-Warning "joinMethod '$join' is not implemented yet" }
    }
    $result.status = 'ok'
}
catch {
    $result.status = 'error'
    $result.error = $_.Exception.Message
    Write-Warning $_.Exception.Message
}
finally {
    $result.finished = (Get-Date).ToString('o')
    $result | ConvertTo-Json -Depth 3 | Set-Content (Join-Path $logs 'firstboot-result.json') -Encoding UTF8
    if ($result.status -eq 'ok') { Set-Content $marker $result.finished }
    Stop-Transcript | Out-Null
}
