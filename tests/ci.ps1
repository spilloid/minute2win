# Shared by tests/run.sh (podman) and .github/workflows/ci.yml.
$ErrorActionPreference = 'Stop'
foreach ($mod in @{ Name = 'Pester'; MinimumVersion = '5.5.0' }, @{ Name = 'PSScriptAnalyzer'; MinimumVersion = '1.22.0' }) {
    if (-not (Get-Module -ListAvailable $mod.Name | Where-Object Version -ge $mod.MinimumVersion)) {
        Install-Module @mod -Scope CurrentUser -Force -SkipPublisherCheck
    }
}
$lint = Invoke-ScriptAnalyzer -Path . -Recurse -Settings ./PSScriptAnalyzerSettings.psd1
$lint | Format-Table -AutoSize | Out-String | Write-Host
$r = Invoke-Pester -Path tests -Output Detailed -PassThru
$bad = @($lint | Where-Object Severity -in 'Error', 'Warning')
if ($bad.Count) { Write-Host "Lint: $($bad.Count) warning/error finding(s)"; exit 1 }
if ($r.FailedCount -gt 0) { exit 1 }
